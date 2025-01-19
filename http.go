package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/porjo/ytdl-web/internal/jobs"
	"github.com/porjo/ytdl-web/internal/util"
	"github.com/porjo/ytdl-web/internal/ytworker"
	"github.com/tmaxmax/go-sse"
)

const (
	// timeout opus stream if no new data read from file in this time
	StreamSourceTimeout = 30 * time.Second

	// FIXME: need a better way of detecting and timing out slow clients
	// http response deadline (slow reading clients)
	HTTPWriteTimeout = 1800 * time.Second

	WSPingInterval = 10 * time.Second
	WSWriteWait    = 2 * time.Second

	// default content expiry in seconds
	DefaultExpiry = 24 * time.Hour
)

type Request struct {
	URL        string
	DeleteURLs []string `json:"delete_urls"`
}

type dlHandler struct {
	WebRoot    string
	OutPath    string
	RemoteAddr string
	FFProbeCmd string
	Dispatcher *jobs.Dispatcher
	Downloader *ytworker.Download

	Logger *slog.Logger

	SSE *sse.Server
}

func (dl *dlHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	id, outCh := dl.Downloader.Subscribe()
	defer dl.Downloader.Unsubscribe(id)

	dl.RemoteAddr = r.RemoteAddr
	logger := dl.Logger.With("dl", id)
	logger.Info("client connected")

	wg := sync.WaitGroup{}

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer logger.Debug("serveHTTP outCh read loop, done")

		for {
			select {
			case <-ctx.Done():
				return
			case m, open := <-outCh:
				if !open {
					return
				}
				sseM := &sse.Message{}
				j, _ := m.JSON()
				sseM.AppendData(string(j))

				if m.Key == ytworker.KeyCompleted {
					// on completion, also send recent URLs
					gruCtx, _ := context.WithTimeout(ctx, 10*time.Second)
					recentURLs, err := GetRecentURLs(gruCtx, dl.WebRoot, dl.OutPath, dl.FFProbeCmd)
					if err != nil {
						logger.Error("GetRecentURLS error", "error", err)
						return
					}
					m := util.Msg{Key: "recent", Value: recentURLs}
					j, _ = m.JSON()
					sseM.AppendData(string(j))
				}
				dl.SSE.Publish(sseM)
			}
		}
	}()

	sseM := &sse.Message{}
	// Send recently retrieved URLs
	gruCtx, _ := context.WithTimeout(ctx, 10*time.Second)
	recentURLs, err := GetRecentURLs(gruCtx, dl.WebRoot, dl.OutPath, dl.FFProbeCmd)
	if err != nil {
		logger.Error("GetRecentURLS error", "error", err)
		return
	}
	m := util.Msg{Key: "recent", Value: recentURLs}
	j, _ = m.JSON()
	dl.SSE.Publish(sseM)

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer logger.Debug("ending ws read goroutine")
		defer conn.Close()
		for {
			msgType, raw, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					logger.Error("ReadMessage error", "error", err)
				}
				return
			}

			logger.Debug("websocket read", "msg", string(raw))

			switch msgType {
			case websocket.TextMessage:
				var req Request
				err = json.Unmarshal(raw, &req)
				if err != nil {
					logger.Error("json unmarshal error", "error", err)
					return
				}

				err := dl.msgHandler(req)
				if err != nil {
					logger.Error("msgHandler error", "error", err)
					m := util.Msg{Key: "error", Value: err.Error()}
					conn.writeMsg(m)
				}
			default:
				logger.Error("unknown message type - close websocket\n")
				return
			}
		}
	}()

	logger.Debug("serveHTTP waitgroup wait")
	wg.Wait()
	logger.Debug("serveHTTP end")
}

func (c *Conn) writeMsg(val interface{}) error {
	c.Lock()
	defer c.Unlock()
	j, err := json.Marshal(val)
	if err != nil {
		return err
	}
	slog.Debug("websocket write", "ws", c.RemoteAddr(), "msg", string(j))
	if err = c.WriteMessage(websocket.TextMessage, j); err != nil {
		return err
	}

	return nil
}

func (ws *dlHandler) msgHandler(req Request) error {

	if req.URL == "" && len(req.DeleteURLs) == 0 {
		return fmt.Errorf("unknown parameters")
	}

	if len(req.DeleteURLs) > 0 {
		err := DeleteFiles(req.DeleteURLs, ws.WebRoot)
		if err != nil {
			return err
		}
	} else if req.URL != "" {

		job := &jobs.Job{Payload: req.URL}
		ws.Dispatcher.Enqueue(job)
	}

	return nil
}

// ServeStream sends the file data to the client as a stream
//
// Because we are reading a file that is growing as we read it, we can't use normal FileServer as
// that would send a Content-Length header with a value smaller than the ultimate filesize.
//
// By copying the raw bytes into ResponseWriter it causes the response to be sent using
// HTTP chunked encoding so the client will continue to request more data until the server signals the end.
//
// There are a couple of challenges to overcome:
//   - how to know when the encoding has finished? The current solution is to wait StreamSourceTimeoutSec and
//     end the handler if no data is copied in that time. Is that the best approach?
//   - how to handle clients that delay requesting more data? In this case ResponseWriter blocks the
//     Copy operation.
//
// I think the only solution is to set WriteTimeout on http.Server
func ServeStream(webRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dir := http.Dir(webRoot)

		filename := strings.Replace(path.Clean(r.URL.Path), "stream/", "", 1)

		f, err := dir.Open(filename)
		if err != nil {
			msg, code := toHTTPError(err)
			http.Error(w, msg, code)
			return
		}
		defer f.Close()

		lastData := time.Now()
		for {
			// io.Copy doesn't return error on EOF
			i, err := io.Copy(w, f)
			if err != nil {
				slog.Info("servestream copy error", "error", err)
				return
			}
			if i == 0 {
				if time.Since(lastData) > StreamSourceTimeout {
					slog.Info("servestream timeout", "timeout", StreamSourceTimeout)
					return
				}
				time.Sleep(time.Second)
			} else {
				lastData = time.Now()
			}
		}
	}
}

func toHTTPError(err error) (msg string, httpStatus int) {
	if errors.Is(err, fs.ErrNotExist) {
		return "404 page not found", http.StatusNotFound
	}
	if errors.Is(err, fs.ErrPermission) {
		return "403 Forbidden", http.StatusForbidden
	}
	// Default:
	return "500 Internal Server Error", http.StatusInternalServerError
}

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
	iws "github.com/porjo/ytdl-web/internal/websocket"
	"github.com/porjo/ytdl-web/internal/ytworker"
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

var (
	upgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}
)

type Request struct {
	URL        string
	DeleteURLs []string `json:"delete_urls"`
}

type Conn struct {
	sync.Mutex
	*websocket.Conn
}

type wsHandler struct {
	WebRoot    string
	OutPath    string
	RemoteAddr string
	FFProbeCmd string
	Dispatcher *jobs.Dispatcher
	Downloader *ytworker.Download

	Logger *slog.Logger
}

func (ws *wsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	gconn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error(err.Error())
		return
	}
	ws.RemoteAddr = gconn.RemoteAddr().String()
	logger := ws.Logger.With("ws", ws.RemoteAddr)
	logger.Info("client connected")

	// wrap Gorilla conn with our conn so we can extend functionality
	conn := Conn{sync.Mutex{}, gconn}

	wg := sync.WaitGroup{}

	wg.Add(1)
	// setup ping/pong to keep connection open
	go func() {
		ticker := time.NewTicker(WSPingInterval)
		defer ticker.Stop()
		defer wg.Done()

		for {
			select {
			case <-ctx.Done():
				logger.Info("ping, context done")
				return
			case <-ticker.C:
				//slog.Debug("ping")
				// WriteControl can be called concurrently
				if err := conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(WSWriteWait)); err != nil {
					logger.Error("ping client error", "error", err)
					cancel()
					return
				}
			}
		}
	}()

	errCh := make(chan error)
	defer close(errCh)
	// wait for goroutines to return before closing channels (defers are last in first out)
	defer func() {
		logger.Debug("serveHTTP waitgroup wait")
		wg.Wait()
		logger.Debug("serveHTTP end")
	}()

	id, outCh := ws.Downloader.Subscribe()
	defer ws.Downloader.Unsubscribe(id)

	wg.Add(1)
	go func() {
		defer wg.Done()

		for {
			select {
			case <-ctx.Done():
				return
			case m, open := <-outCh:
				if !open {
					return
				}
				err := conn.writeMsg(m)
				if err != nil {
					errCh <- err
				}
				if m.Key == ytworker.KeyCompleted {
					// on completion, also send recent URLs
					gruCtx, _ := context.WithTimeout(ctx, 10*time.Second)
					recentURLs, err := GetRecentURLs(gruCtx, ws.WebRoot, ws.OutPath, ws.FFProbeCmd)
					if err != nil {
						logger.Error("GetRecentURLS error", "error", err)
						return
					}
					m := iws.Msg{Key: "recent", Value: recentURLs}
					conn.writeMsg(m)
				}
			case err := <-errCh:
				if err != nil {
					m := iws.Msg{Key: "error", Value: err.Error()}
					conn.writeMsg(m)
				}
				return
			}
		}
	}()

	// Send recently retrieved URLs
	gruCtx, _ := context.WithTimeout(ctx, 10*time.Second)
	recentURLs, err := GetRecentURLs(gruCtx, ws.WebRoot, ws.OutPath, ws.FFProbeCmd)
	if err != nil {
		logger.Error("GetRecentURLS error", "error", err)
		return
	}
	m := iws.Msg{Key: "recent", Value: recentURLs}
	conn.writeMsg(m)

	for {
		msgType, raw, err := conn.ReadMessage()
		if err != nil {
			logger.Error("ReadMessage error", "error", err)
			return
		}

		logger.Debug("read message", "msg", string(raw))

		switch msgType {
		case websocket.TextMessage:
			var req Request
			err = json.Unmarshal(raw, &req)
			if err != nil {
				logger.Error("json unmarshal error", "error", err)
				return
			}

			err := ws.msgHandler(req)
			if err != nil {
				logger.Error("error", "error", err)
				errCh <- err
			}
		default:
			logger.Error("unknown message type - close websocket\n")
			conn.Close()
			return
		}
	}
}

func (c *Conn) writeMsg(val interface{}) error {
	c.Lock()
	defer c.Unlock()
	j, err := json.Marshal(val)
	if err != nil {
		return err
	}
	slog.Debug("write message", "ws", c.RemoteAddr(), "msg", string(j))
	if err = c.WriteMessage(websocket.TextMessage, j); err != nil {
		return err
	}

	return nil
}

func (ws *wsHandler) msgHandler(req Request) error {

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

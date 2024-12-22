package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
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

	YtdlpSocketTimeoutSec = 10

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
	WebRoot          string
	SponsorBlock     bool
	SponsorBlockCats string
	OutPath          string
	RemoteAddr       string
	MaxProcessTime   time.Duration
}

func (ws *wsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	gconn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}
	ws.RemoteAddr = gconn.RemoteAddr().String()
	log.Printf("WS %s: Client connected\n", ws.RemoteAddr)

	// wrap Gorilla conn with our conn so we can extend functionality
	conn := Conn{sync.Mutex{}, gconn}

	wg := sync.WaitGroup{}

	wg.Add(1)
	// setup ping/pong to keep connection open
	go func(remoteAddr string) {
		ticker := time.NewTicker(WSPingInterval)
		defer ticker.Stop()
		defer wg.Done()

		for {
			select {
			case <-ctx.Done():
				log.Printf("WS %s: ping, context done\n", remoteAddr)
				return
			case <-ticker.C:
				// WriteControl can be called concurrently
				if err := conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(WSWriteWait)); err != nil {
					log.Printf("WS %s: ping client, err %s\n", remoteAddr, err)
					cancel()
					return
				}
			}
		}
	}(ws.RemoteAddr)

	outCh := make(chan iws.Msg)
	defer close(outCh)
	errCh := make(chan error)
	defer close(errCh)
	// wait for goroutines to return before closing channels (defers are last in first out)
	defer wg.Wait()

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
			case err := <-errCh:
				if err != nil {
					m := iws.Msg{Key: "error", Value: err.Error()}
					conn.writeMsg(m)
				}
				return
			}
		}
	}()

	dl := ytworker.NewDownload(ws.MaxProcessTime)
	dispatcher := jobs.NewDispatcher(dl, 10)
	go func() {
		log.Printf("starting job dispatcher")
		dispatcher.Start(ctx)
	}()

	// Send recently retrieved URLs
	gruCtx, _ := context.WithTimeout(ctx, 10*time.Second)
	recentURLs, err := GetRecentURLs(gruCtx, ws.WebRoot, ws.OutPath)
	if err != nil {
		log.Printf("WS %s: GetRecentURLS err %s\n", ws.RemoteAddr, err)
		return
	}
	m := iws.Msg{Key: "recent", Value: recentURLs}
	conn.writeMsg(m)

	for {
		msgType, raw, err := conn.ReadMessage()
		if err != nil {
			log.Printf("WS %s: ReadMessage err %s\n", ws.RemoteAddr, err)
			return
		}

		log.Printf("WS %s: read message %s\n", ws.RemoteAddr, string(raw))

		if msgType == websocket.TextMessage {
			wg.Add(1)
			go func() {
				defer wg.Done()
				var req Request
				err = json.Unmarshal(raw, &req)
				if err != nil {
					log.Printf("json unmarshal error: %s\n", err)
					return
				}

				err := ws.msgHandler(ctx, outCh, req)
				if err != nil {
					log.Printf("Error '%s'\n", err)
					errCh <- err
				}
			}()
		} else {
			log.Printf("unknown message type - close websocket\n")
			conn.Close()
			return
		}
		log.Printf("WS %s: end main loop\n", ws.RemoteAddr)
	}
}

func (c *Conn) writeMsg(val interface{}) error {
	c.Lock()
	defer c.Unlock()
	j, err := json.Marshal(val)
	if err != nil {
		return err
	}
	log.Printf("WS %s: write message %s\n", c.RemoteAddr(), string(j))
	if err = c.WriteMessage(websocket.TextMessage, j); err != nil {
		return err
	}

	return nil
}

func (ws *wsHandler) msgHandler(ctx context.Context, outCh chan<- iws.Msg, req Request) error {

	if req.URL == "" && len(req.DeleteURLs) == 0 {
		return fmt.Errorf("unknown parameters")
	}

	if len(req.DeleteURLs) > 0 {
		err := DeleteFiles(req.DeleteURLs, ws.WebRoot)
		if err != nil {
			return err
		}
	} else if req.URL != "" {
		url, err := url.Parse(req.URL)
		if err != nil {
			return err
		}

		if url.String() == "" {
			return fmt.Errorf("url was empty")
		}

		restartCh := make(chan bool)
		ctx1, cancel1 := context.WithCancel(ctx)
		defer cancel1()
		err = ws.ytDownload(ctx1, outCh, restartCh, url)
		if err != nil {
			return err
		}

		select {
		case <-restartCh:
			ctx2, cancel2 := context.WithCancel(ctx)
			defer cancel2()
			cancel1()
			var restartCh2 chan bool
			err = ws.ytDownload(ctx2, outCh, restartCh2, url)
			if err != nil {
				return err
			}
		default:
		}
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
//
// end the handler if no data is copied in that time. Is that the best approach?
//   - how to handle clients that delay requesting more data? In this case ResponseWriter blocks the Copy operation.
//
// I think the only solution is to set WriteTimeout on http.Sever
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
				log.Printf("servestream copy err %s\n", err)
				return
			}
			if i == 0 {
				if time.Since(lastData) > time.Duration(StreamSourceTimeout) {
					log.Printf("servestream timeout\n")
					return
				}
				time.Sleep(time.Duration(1 * time.Second))
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

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/porjo/braid"
)

// default process timeout in seconds (if not explicitly set via flag)
const DefaultProcessTimeout = 300
const ClientJobs = 5

const PingInterval = 30 * time.Second
const WriteWait = 10 * time.Second

// default content expiry in seconds
const DefaultExpiry = 7200

// maximum file size to download, in megabytes
const MaxFileSizeMB = 100

// filename sanitization
// swap specific characters
var filenameReplacer = strings.NewReplacer("(", "_", ")", "_", "&", "+", "'", ",", "—", "-")

// remove all remaining non-allowed characters
var filenameRegexp = regexp.MustCompile("[^0-9A-Za-z_. +,-]+")

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

type Msg struct {
	Key   string
	Value interface{}
}

type Meta struct {
	Id      string
	Title   string
	Formats []MetaFormat
}

type MetaFormat struct {
	FileSize  int
	Vcodec    string
	Acodec    string
	Extension string `json:"ext"`
	URL       string
}

type Info struct {
	Title       string
	FileSize    int
	DownloadURL string
}

type Progress struct {
	Pct string
	ETA string
}
type Conn struct {
	*websocket.Conn
}

type wsHandler struct {
	Timeout time.Duration
	WebRoot string
	YTCmd   string
	OutPath string
}

func (ws *wsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if ws.Timeout == 0 {
		ws.Timeout = time.Duration(DefaultProcessTimeout) * time.Second
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	gconn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}
	log.Printf("Client connected %s\n", gconn.RemoteAddr())

	// wrap Gorilla conn with our conn so we can extend functionality
	conn := Conn{gconn}

	// setup ping/pong to keep connection open
	go func() {
		c := time.Tick(PingInterval)
		for {
			select {
			case <-ctx.Done():
				log.Printf("ping: context done\n")
				return
			case <-c:
				// WriteControl can be called concurrently
				if err := conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(WriteWait)); err != nil {
					log.Printf("WS: ping client, err %s\n", err)
					return
				}
			}
		}
	}()

	for {
		msgType, raw, err := conn.ReadMessage()
		if err != nil {
			log.Printf("WS: ReadMessage err %s\n", err)
			return
		}

		log.Printf("WS: read message %s\n", string(raw))

		if msgType == websocket.TextMessage {
			var msg Msg
			err = json.Unmarshal(raw, &msg)
			if err != nil {
				log.Printf("json unmarshal error: %s\n", err)
				return
			}

			if msg.Key == "url" {
				outCh := make(chan Msg)
				errCh := make(chan error)
				go func() {
					err := ws.msgHandler(ctx, outCh, msg)
					if err != nil {
						errCh <- err
					}
				}()

			loop:
				for {
					select {
					case <-ctx.Done():
						break loop
					case m, open := <-outCh:
						if !open {
							break loop
						}
						err := conn.writeMsg(m)
						if err != nil {
							errCh <- err
						}
					case err := <-errCh:
						m := Msg{Key: "error", Value: err.Error()}
						conn.writeMsg(m)
						break loop
					}
				}
			}
		} else {
			log.Printf("unknown message type - close websocket\n")
			conn.Close()
			return
		}
		log.Printf("WS end main loop\n")
	}
}

func (c *Conn) writeMsg(val interface{}) error {
	j, err := json.Marshal(val)
	if err != nil {
		return err
	}
	log.Printf("WS: write message %s\n", string(j))
	if err = c.WriteMessage(websocket.TextMessage, j); err != nil {
		return err
	}

	return nil
}

func (ws *wsHandler) msgHandler(ctx context.Context, outCh chan<- Msg, msg Msg) error {
	url, err := url.Parse(msg.Value.(string))
	if err != nil {
		return err
	}

	if url.String() == "" {
		return fmt.Errorf("url was empty")
	}

	ctxHandler, cancel := context.WithCancel(ctx)
	defer cancel()

	webFileName := ws.OutPath + "/ytdl-"
	diskFileName := ws.WebRoot + "/" + webFileName

	errCh := make(chan error)

	var meta Meta

	rPipe, wPipe := io.Pipe()
	defer rPipe.Close()

	go func() {
		dec := json.NewDecoder(rPipe)
		err := dec.Decode(&meta)
		if err != nil {
			errCh <- err
		}

		durl := ""
		fileSize := 0
		ext := ""
		for _, f := range meta.Formats {
			if fileSize == 0 ||
				(f.FileSize < fileSize && f.FileSize > 0 && f.Vcodec == "none") {
				durl = f.URL
				ext = f.Extension
				fileSize = f.FileSize
			}
		}

		sanitizedTitle := filenameReplacer.Replace(meta.Title)
		sanitizedTitle = filenameRegexp.ReplaceAllString(sanitizedTitle, "")
		sanitizedTitle = strings.Join(strings.Fields(sanitizedTitle), " ") // remove double spaces
		diskFileName += sanitizedTitle + "." + ext
		webFileName += sanitizedTitle + "." + ext

		i := Info{Title: meta.Title, FileSize: fileSize}
		outCh <- Msg{Key: "info", Value: i}

		var r *braid.Request
		r, err = braid.NewRequest()
		if err != nil {
			errCh <- err
		}
		r.SetJobs(ClientJobs)
		braid.SetLogger(log.Printf)
		go func() {
			GetProgress(ctxHandler, outCh, r)
		}()
		var file *os.File

		tCtx, cancel := context.WithTimeout(ctxHandler, ws.Timeout)
		defer cancel()
		file, err = r.FetchFile(tCtx, durl, diskFileName)
		if err != nil {
			errCh <- err
		}
		file.Close()

		m := Msg{Key: "link", Value: Info{DownloadURL: webFileName}}
		outCh <- m

		errCh <- nil
	}()

	go func() {
		log.Printf("Fetching url %s\n", url.String())
		args := []string{
			"-j", // write json to stdout only
			url.String(),
		}
		err = RunCommandCh(ctxHandler, wPipe, ws.YTCmd, args...)
		if err != nil {
			errCh <- err
		}
	}()

	return <-errCh
}

func GetProgress(ctx context.Context, outCh chan<- Msg, r *braid.Request) {
	ticker := time.Tick(time.Millisecond * 500)

	start := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker:
			stats := r.Stats()

			pct := float64(stats.ReadBytes) / float64(stats.TotalBytes) * 100
			if pct == 0 {
				pct = 0.1
			}
			dur := time.Now().Sub(start)

			rem := float64(dur) / pct * (100 - pct)
			eta := time.Duration(rem).Round(time.Second).String()

			m := Msg{}
			m.Key = "progress"
			p := Progress{
				Pct: strconv.FormatFloat(pct, 'f', 1, 64),
				ETA: eta,
			}
			m.Value = p
			outCh <- m
		}
	}
}

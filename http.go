package main

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
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

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

// capture progress output e.g '73.2% of 6.25MiB ETA 00:01'
//var progressRe = regexp.MustCompile(`([\d.]+)% of ([\d.]+)(?:.*ETA ([\d:]+))?`)
// [#c7c8f2 11MiB/30MiB(37%) CN:4 DL:1.1MiB ETA:16s]
//var progressRe = regexp.MustCompile(`(?:[\d.]+)[^ ]+\/([\d.]+)[^ ]+\(([0-9]+)%\).*ETA:([0-9]+)`)
var progressRe = regexp.MustCompile(`\[#\w+ \d+\.?\d*\w+\/(\d+\.?\d*)\w+\((\d+)%\) CN:\d DL:\d+\.?\d*\w+(?: ETA:(\d+\w))?\]`)

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
	Filesize  int
	Extension string `json:"ext"`
	URL       string
}
type Progress struct {
	Pct string
	MiB string
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

				log.Printf("enter loop\n")
			loop:
				for {
					select {
					case m, open := <-outCh:
						if !open {
							log.Printf("break loop\n")
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
	defer close(outCh)
	url, err := url.Parse(msg.Value.(string))
	if err != nil {
		return err
	}

	if url.String() == "" {
		return fmt.Errorf("url was empty")
	}

	// filename is md5 sum of URL
	urlSum := md5.Sum([]byte(url.String()))
	fileName := fmt.Sprintf("%x", urlSum)
	webFileName := ws.OutPath + "/ytdl-" + fileName
	diskFileNameNoExt := ws.WebRoot + "/" + webFileName
	ytFileName := diskFileNameNoExt

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
		smallest := 0
		for _, f := range meta.Formats {
			if smallest == 0 || (f.Filesize < smallest && f.Filesize > 0) {
				durl = f.URL
				ytFileName += "." + f.Extension
				smallest = f.Filesize
			}
		}
		fmt.Printf("durl %s\n", durl)

		outCh <- Msg{Key: "info", Value: meta}

		var r *braid.Request
		ctx := context.Background()
		r, err = braid.NewRequest()
		if err != nil {
			errCh <- err
		}
		r.SetJobs(ClientJobs)
		braid.SetLogger(log.Printf)
		go func() {
			GetProgress(ctx, outCh, r)
		}()
		var file *os.File

		tCtx, cancel := context.WithTimeout(ctx, ws.Timeout)
		defer cancel()
		file, err = r.FetchFile(tCtx, durl, ytFileName)
		if err != nil {
			errCh <- err
		}
		file.Close()

		errCh <- nil
	}()

	go func() {
		log.Printf("Fetching url %s\n", url.String())
		args := []string{
			"-j", // write json to stdout only
			url.String(),
		}
		err = RunCommandCh(ctx, wPipe, ws.YTCmd, args...)
		if err != nil {
			errCh <- err
		}
	}()

	err1 := <-errCh
	fmt.Printf("errCh err %s\n", err1)
	return err1
}

func GetProgress(ctx context.Context, outCh chan<- Msg, r *braid.Request) {
	ticker := time.Tick(time.Second)

	start := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker:
			stats := r.Stats()

			pct := float64(stats.ReadBytes) / float64(stats.TotalBytes) * 100.0
			mib := float64(stats.TotalBytes) / 1024 / 1024

			dur := time.Now().Sub(start)

			rem := float64(dur) / pct * (100 - pct)
			eta := time.Duration(rem).String()

			m := Msg{}
			m.Key = "progress"
			p := Progress{
				Pct: strconv.FormatFloat(pct, 'f', 2, 64),
				MiB: strconv.FormatFloat(mib, 'f', 2, 64),
				ETA: eta,
			}
			m.Value = p
			outCh <- m
		}
	}
}

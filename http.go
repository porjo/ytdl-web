package main

import (
	"bufio"
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// default process timeout in seconds (if not explicitly set via flag)
const DefaultProcessTimeout = 300

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

type Info struct {
	Title       string
	Filesize    int
	Extension   string `json:"ext"`
	DownloadURL string
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

			loop:
				for {
					select {
					case m, open := <-outCh:
						if !open {
							break loop
						}
						err := conn.writeMsg(m)
						if err != nil {
							errCh <- err
						}
					case err := <-errCh:
						log.Printf("handler err: %s\n", err)
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
	ytFileName := diskFileNameNoExt + ".%(ext)s"

	errCh := make(chan error)

	var info Info
	go func() {
		count := 0
		for {
			count++
			if count > 20 {
				errCh <- fmt.Errorf("waited too long for info file")
				break
			}

			infoFileName := diskFileNameNoExt + ".info.json"

			time.Sleep(500 * time.Millisecond)
			if _, err := os.Stat(infoFileName); os.IsNotExist(err) {
				continue
			}
			raw, err := ioutil.ReadFile(infoFileName)
			if err != nil {
				errCh <- fmt.Errorf("info file read error: %s", err)
				break
			}

			err = json.Unmarshal(raw, &info)
			if err != nil {
				errCh <- fmt.Errorf("info file json unmarshal error: %s", err)
				break
			}
			m := Msg{Key: "info", Value: info}
			outCh <- m
			break
		}
	}()

	rPipe, wPipe := io.Pipe()
	defer rPipe.Close()

	go func() {
		scannerStdout := bufio.NewScanner(rPipe)
		scannerStdout.Split(scanCR)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			if scannerStdout.Scan() {
				text := scannerStdout.Text()
				if strings.TrimSpace(text) != "" {
					fmt.Printf("out: %s\n", text)
					m := getProgress(text)
					if m != nil {
						outCh <- *m
					}
				}
			} else {
				errCh <- scannerStdout.Err()
				return
			}
		}
	}()

	go func() {
		log.Printf("Fetching url %s\n", url.String())
		args := []string{
			"--write-info-json",
			"--external-downloader", "aria2c",
			"--external-downloader-args", "\"--stderr=true\"",
			"--add-metadata",
			"--max-filesize", fmt.Sprintf("%dm", MaxFileSizeMB),
			"-f", "worstaudio",
			// output progress bar as newlines
			"--newline",
			// Do not use the Last-modified header to set the file modification time
			"--no-mtime",
			"-o", ytFileName,
			url.String(),
		}
		tCtx, cancel := context.WithTimeout(ctx, ws.Timeout)
		defer cancel()
		err = RunCommandCh(tCtx, wPipe, ws.YTCmd, args...)
		errCh <- err
	}()

	return <-errCh
}

func getProgress(v string) *Msg {
	var m *Msg
	matches := progressRe.FindStringSubmatch(v)

	if len(matches) == 4 {
		m = new(Msg)
		m.Key = "progress"
		p := Progress{
			Pct: matches[2],
			MiB: matches[1],
			ETA: matches[3],
		}
		m.Value = p
	}
	return m
}

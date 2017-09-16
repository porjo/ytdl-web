package main

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"time"

	"github.com/gorilla/websocket"
)

type timeout int

const processTimeoutCtxKey timeout = 0

// default process timeout if not explicitly set via handlerTimeout
const DefaultProcessTimeout = 30

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

// capture progress output e.g '73.2% of 6.25MiB ETA 00:01'
var progressRe = regexp.MustCompile(`([\d.]+)% of ([\d.]+)(?:.*ETA ([\d:]+))?`)

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

type wsHandler struct{}

func handlerTimeout(next http.Handler, processTimeout int) http.Handler {

	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		ctx := context.WithValue(req.Context(), processTimeoutCtxKey, processTimeout)
		next.ServeHTTP(rw, req.WithContext(ctx))
	})
}

func (ws *wsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	timeout, ok := r.Context().Value(processTimeoutCtxKey).(int)
	fmt.Printf("timeout %v ok %v\n", timeout, ok)
	if !ok {
		timeout = DefaultProcessTimeout
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(timeout)*time.Second)
	defer cancel()

	gconn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}

	// wrap Gorilla conn with our conn so we can extend functionality
	conn := Conn{gconn}

	for {
		msgType, raw, err := conn.ReadMessage()
		if err != nil {
			fmt.Println(err)
			return
		}

		log.Printf("WS: read message %s\n", string(raw))

		if msgType == websocket.TextMessage {
			var msg Msg
			err = json.Unmarshal(raw, &msg)
			if err != nil {
				log.Printf("json unmarshal error: %s", err)
				return
			}

			if msg.Key == "url" {
				outCh := make(chan Msg)
				errCh := make(chan error)
				go func() {
					err := msgHandler(ctx, outCh, msg)
					if err != nil {
						errCh <- err
					}
				}()

			loop:
				for {
					select {
					case m, open := <-outCh:
						if !open {
							log.Printf("outCh closed\n")
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

func msgHandler(ctx context.Context, outCh chan<- Msg, msg Msg) error {
	url, err := url.Parse(msg.Value.(string))
	if err != nil {
		return err
	}

	// filename is md5 sum of URL
	urlSum := md5.Sum([]byte(url.String()))
	fileName := fmt.Sprintf("%x", urlSum)
	webFileName := "dl/ytdl-" + fileName
	diskFileNameNoExt := webRoot + "/" + webFileName
	ytFileName := diskFileNameNoExt + ".%(ext)s"

	errCh := make(chan error)
	cmdCh := make(chan string)
	go func() {
		log.Printf("Fetching url %s\n", url.String())
		args := []string{"--write-info-json", "-f", "worstaudio", "--newline", "-o", ytFileName, url.String()}
		err := RunCommandCh(ctx, cmdCh, "\r\n", ytCmd, args...)
		if err != nil {
			errCh <- err
		}
	}()

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

loop:
	for {
		select {
		case v, open := <-cmdCh:
			// is channel closed?
			if !open {
				// if we got here, then command completed successfully
				log.Printf("msgHandler: output channel closed\n")
				info.DownloadURL = webFileName + "." + info.Extension
				m := Msg{Key: "link", Value: info}
				outCh <- m
				break loop
			}
			fmt.Println(v)

			m := getProgress(v)
			if m != nil {
				outCh <- *m
			}
		case err := <-errCh:
			return err
		}
	}

	close(outCh)
	return nil
}

func getProgress(v string) *Msg {
	var m *Msg
	matches := progressRe.FindStringSubmatch(v)

	if len(matches) == 4 {
		m = new(Msg)
		m.Key = "progress"
		p := Progress{
			Pct: matches[1],
			MiB: matches[2],
			ETA: matches[3],
		}
		m.Value = p
	}
	return m
}

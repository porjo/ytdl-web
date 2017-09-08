package main

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"time"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

type Msg struct {
	Key   string
	Value string
}

func httpHandlers() {
	http.HandleFunc("/websocket", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			fmt.Println(err)
			return
		}

		for {
			msgType, raw, err := conn.ReadMessage()
			if err != nil {
				fmt.Println(err)
				return
			}

			fmt.Printf("ws recv msg: %s\n", raw)

			if msgType == websocket.TextMessage {
				var msg Msg
				err = json.Unmarshal(raw, &msg)
				if err != nil {
					fmt.Printf("json unmarshal error: %s", err)
					return
				}

				if msg.Key == "url" {
					err = msgHandler(msg)
					if err != nil {
						fmt.Println(err)
						return
					}
				}
			} else {
				conn.Close()
				fmt.Println(string(raw))
				return
			}
		}
	})

	webRoot := "html"
	http.Handle("/", http.FileServer(http.Dir(webRoot)))

	log.Printf("Listening on :3000...\n")
	http.ListenAndServe(":3000", nil)
}

func msgHandler(msg Msg) error {

	url, err := url.Parse(msg.Value)
	if err != nil {
		fmt.Println(err)
		return err
	}

	// filename is md5 sum of URL
	urlSum := md5.Sum([]byte(url.String()))
	fileName := "ytdl-" + fmt.Sprintf("%x", urlSum)

	errCh := make(chan error)
	cmdCh := make(chan string)
	go func() {

		fmt.Printf("Fetching url %s\n", url.String())
		err := RunCommandCh(cmdCh, "\r\n", ytCmd, "--write-info-json", "--newline", "-o", fileName, url.String())
		if err != nil {
			errCh <- err
			log.Println(err)
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

			time.Sleep(500 * time.Millisecond)
			if _, err := os.Stat(fileName + ".info.json"); os.IsNotExist(err) {
				continue
			}
			raw, err := ioutil.ReadFile(fileName + ".info.json")
			if err != nil {
				errCh <- fmt.Errorf("info file read error: %s", err)
				break
			}

			err = json.Unmarshal(raw, &info)
			if err != nil {
				errCh <- fmt.Errorf("info file json unmarshal error: %s", err)
				break
			}
			fmt.Printf("info %v\n", info)
			break
		}
	}()

loop:
	for {
		select {
		case v, ok := <-cmdCh:
			if !ok {
				break loop
			}
			fmt.Println(v)
		case err := <-errCh:
			return err
		}
	}

	return nil
}

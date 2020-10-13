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
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/porjo/braid"
)

// capture progress output e.g '73.2% of 6.25MiB ETA 00:01'
var ytProgressRe = regexp.MustCompile(`([\d.]+)% of ~?([\d.]+)(?:.*ETA ([\d:]+))?`)

const MaxFileSize = 170e6 // 150 MB

// default process timeout in seconds (if not explicitly set via flag)
const DefaultProcessTimeout = 300
const ClientJobs = 5

const PingInterval = 30 * time.Second
const WriteWait = 10 * time.Second

// default content expiry in seconds
const DefaultExpiry = 7200

// filename sanitization
// swap specific special characters
var filenameReplacer = strings.NewReplacer(
	"(", "_", ")", "_", "&", "+", "—", "-", "~", ".", "¿", "_", "'", "", "±", "+", "/", ".", "\\", ".", "ß", "ss",
	"!", "_", "^", "_", "$", "_", "%", "_", "@", "_", "¯", "-", "`", ".", "#", "", "¡", "_", "ñ", "n", "Ñ", "N",
	"é", "e", "è", "e", "ê", "e", "ë", "e", "É", "E", "È", "E", "Ê", "E", "Ë", "E",
	"à", "a", "â", "a", "ä", "a", "á", "a", "À", "A", "Â", "A", "Ä", "A", "Á", "A",
	"ò", "o", "ô", "o", "ö", "o", "ó", "o", "Ò", "O", "Ô", "O", "Ö", "O", "Ó", "O",
	"ì", "i", "î", "i", "ï", "i", "í", "i", "Ì", "I", "Î", "I", "Ï", "I", "Í", "I",
	"ù", "u", "û", "u", "ü", "u", "ú", "u", "Ù", "U", "Û", "U", "Ü", "U", "Ú", "U",
)

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

type Request struct {
	URL          string
	YTDownloader bool
}

type Meta struct {
	Id      string
	Title   string
	Formats []MetaFormat

	// fallback fields for non-youtube URLs
	URL      string
	Ext      string
	Filesize int
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
	Extension   string `json:"ext"`
	DownloadURL string
}

type Progress struct {
	Pct      string
	FileSize string
	ETA      string
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
		ticker := time.NewTicker(PingInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				log.Printf("ping: context done\n")
				return
			case <-ticker.C:
				// WriteControl can be called concurrently
				if err := conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(WriteWait)); err != nil {
					log.Printf("WS: ping client, err %s\n", err)
					return
				}
			}
		}
	}()

	outCh := make(chan Msg)
	errCh := make(chan error)
	go func() {
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
				m := Msg{Key: "error", Value: err.Error()}
				conn.writeMsg(m)
				return
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
			var req Request
			err = json.Unmarshal(raw, &req)
			if err != nil {
				log.Printf("json unmarshal error: %s\n", err)
				return
			}

			err := ws.msgHandler(ctx, outCh, req)
			if err != nil {
				errCh <- err
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

func (ws *wsHandler) msgHandler(ctx context.Context, outCh chan<- Msg, req Request) error {
	url, err := url.Parse(req.URL)
	if err != nil {
		return err
	}

	if url.String() == "" {
		return fmt.Errorf("url was empty")
	}

	webFileName := ws.OutPath + "/ytdl-"
	diskFileName := ws.WebRoot + "/" + webFileName

	if req.YTDownloader {
		err = ws.ytDownload(ctx, outCh, url, webFileName, diskFileName)
	} else {
		err = ws.braidDownload(ctx, outCh, url, webFileName, diskFileName)
	}

	return err

}

func (ws *wsHandler) ytDownload(ctx context.Context, outCh chan<- Msg, url *url.URL, webFileName, diskFileName string) error {

	rPipe, wPipe := io.Pipe()
	defer rPipe.Close()

	tCtx, cancel := context.WithTimeout(ctx, ws.Timeout)
	defer cancel()

	errCh := make(chan error)

	// filename is md5 sum of URL
	urlSum := md5.Sum([]byte(url.String()))
	tmpFileName := diskFileName + fmt.Sprintf("%x", urlSum)

	go func() {
		log.Printf("Fetching url %s\n", url.String())
		args := []string{
			"--write-info-json",
			"--max-filesize", fmt.Sprintf("%d", int(MaxFileSize)),
			"-f", "worstaudio",
			// output progress bar as newlines
			"--newline",
			// Do not use the Last-modified header to set the file modification time
			"--no-mtime",
			"-o", tmpFileName,
			url.String(),
		}
		log.Printf("Running command %v\n", args)
		err := RunCommandCh(tCtx, wPipe, ws.YTCmd, args...)
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

			infoFileName := tmpFileName + ".info.json"

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
			if info.FileSize > MaxFileSize {
				errCh <- fmt.Errorf("filesize %d too large", info.FileSize)
				break
			}
			m := Msg{Key: "info", Value: info}
			outCh <- m
			break
		}
	}()

	stdout := bufio.NewReader(rPipe)
	for {
		select {
		case err := <-errCh:
			return err
		default:
		}

		line, err := stdout.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				return err
			}
			sanitizedTitle := filenameReplacer.Replace(info.Title)
			sanitizedTitle = filenameRegexp.ReplaceAllString(sanitizedTitle, "")
			sanitizedTitle = strings.Join(strings.Fields(sanitizedTitle), " ") // remove double spaces
			webFileName += sanitizedTitle

			finalFileName := diskFileName + sanitizedTitle + "." + info.Extension
			err = os.Rename(tmpFileName, finalFileName)
			if err != nil {
				return err
			}
			// if we got here, then command completed successfully
			info.DownloadURL = webFileName + "." + info.Extension
			m := Msg{Key: "link", Value: info}
			outCh <- m
			break
		}
		//fmt.Println(line)

		m := getYTProgress(line)
		if m != nil {
			outCh <- *m
		}
	}

	close(outCh)
	return nil
}

func (ws *wsHandler) braidDownload(ctx context.Context, outCh chan<- Msg, url *url.URL, webFileName, diskFileName string) error {

	ctxHandler, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error)

	var meta Meta

	rPipe, wPipe := io.Pipe()
	defer rPipe.Close()

	go func() {
		dec := json.NewDecoder(rPipe)
		err := dec.Decode(&meta)
		if err != nil {
			rawR := dec.Buffered()
			rawB := make([]byte, 1000)
			i, _ := rawR.Read(rawB)
			errCh <- fmt.Errorf("Error decoding JSON, '%s': %s", err, rawB[:i])
			return
		}

		durl := ""
		fileSize := 0
		ext := ""

		if len(meta.Formats) == 0 && meta.URL != "" {
			durl = meta.URL
			fileSize = meta.Filesize
			ext = meta.Ext
		} else {

			for _, f := range meta.Formats {
				if fileSize == 0 ||
					(f.FileSize < fileSize && f.FileSize > 0 && f.Vcodec == "none") {
					durl = f.URL
					ext = f.Extension
					fileSize = f.FileSize
				}
			}
		}

		if fileSize > MaxFileSize {
			errCh <- fmt.Errorf("filesize %d too large", fileSize)
			return
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
			return
		}
		r.SetJobs(ClientJobs)
		braid.SetLogger(log.Printf)
		go func() {
			getBraidProgress(ctxHandler, outCh, r)
		}()
		var file *os.File

		tCtx, cancel := context.WithTimeout(ctxHandler, ws.Timeout)
		defer cancel()
		fmt.Printf("durl %s\n", durl)
		file, err = r.FetchFile(tCtx, durl, diskFileName)
		if err != nil {
			errCh <- err
			return
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
			"--no-warnings",
			url.String(),
		}
		err := RunCommandCh(ctxHandler, wPipe, ws.YTCmd, args...)
		if err != nil {
			errCh <- err
		}
	}()

	return <-errCh
}

func getBraidProgress(ctx context.Context, outCh chan<- Msg, r *braid.Request) {
	ticker := time.NewTicker(time.Millisecond * 500)
	defer ticker.Stop()

	start := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			stats := r.Stats()

			if stats.TotalBytes == 0 {
				continue
			}

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
				Pct:      strconv.FormatFloat(pct, 'f', 1, 64),
				ETA:      eta,
				FileSize: strconv.FormatFloat(float64(stats.TotalBytes)/1024/1024, 'f', 2, 64),
			}
			m.Value = p
			outCh <- m
		}
	}
}

func getYTProgress(v string) *Msg {
	var m *Msg
	matches := ytProgressRe.FindStringSubmatch(v)

	if len(matches) == 4 {
		m = new(Msg)
		m.Key = "progress"
		p := Progress{
			Pct:      matches[1],
			FileSize: matches[2],
			ETA:      matches[3],
		}
		m.Value = p
	}
	return m
}

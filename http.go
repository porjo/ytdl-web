package main

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// capture progress output e.g '100 of 10000000 / 10000000 eta 30'
var ytProgressRe = regexp.MustCompile(`([\d]+) of ([\dNA]+) / ([\d.NA]+) eta ([\d]+)`)

const MaxFileSize = 170e6 // 150 MB

// default process timeout in seconds (if not explicitly set via flag)
const DefaultProcessTimeoutSec = 300
const ClientJobs = 5

// timeout opus stream if no new data read from file in this time
const StreamSourceTimeout = 30 * time.Second

// FIXME: need a better way of detecting and timing out slow clients
// http response deadline (slow reading clients)
const HTTPWriteTimeout = 1800 * time.Second

const WSPingInterval = 10 * time.Second
const WSWriteWait = 2 * time.Second

const YtdlpSocketTimeoutSec = 10

// default content expiry in seconds
const DefaultExpirySec = 7200

var noReencodeSites = []string{"youtube.com", "twitter.com", "rumble.com"}

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
	URL        string
	DeleteURLs []string `json:"delete_urls"`
}

type Info struct {
	Title       string
	Artist      string
	FileSize    int64
	Extension   string `json:"ext"`
	DownloadURL string
}

type Progress struct {
	Pct      string
	FileSize string
	ETA      string
}

type Conn struct {
	sync.Mutex
	*websocket.Conn
}

type wsHandler struct {
	Timeout          time.Duration
	WebRoot          string
	SponsorBlock     bool
	SponsorBlockCats string
	OutPath          string
	RemoteAddr       string
}

func (ws *wsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if ws.Timeout == 0 {
		ws.Timeout = time.Duration(DefaultProcessTimeoutSec)
	}

	ctx, cancel := context.WithCancel(context.Background())
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

	outCh := make(chan Msg)
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
					m := Msg{Key: "error", Value: err.Error()}
					conn.writeMsg(m)
				}
				return
			}
		}
	}()

	// Send recently retrieved URLs
	recentURLs, err := GetRecentURLs(ctx, ws.WebRoot, ws.OutPath, ws.Timeout)
	if err != nil {
		log.Printf("WS %s: GetRecentURLS err %s\n", ws.RemoteAddr, err)
		return
	}
	m := Msg{Key: "recent", Value: recentURLs}
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

func (ws *wsHandler) msgHandler(ctx context.Context, outCh chan<- Msg, req Request) error {

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

func (ws *wsHandler) ytDownload(ctx context.Context, outCh chan<- Msg, restartCh chan bool, url *url.URL) error {

	forceOpus := true
	if slices.Contains(noReencodeSites, url.Host) {
		forceOpus = false
	}

	tCtx, cancel := context.WithTimeout(ctx, ws.Timeout)
	defer cancel()

	errCh := make(chan error)
	defer close(errCh)

	// filename is md5 sum of URL
	urlSum := md5.Sum([]byte(url.String()))
	diskFileNameTmp := filepath.Join(ws.WebRoot, ws.OutPath, "t", "ytdl-"+fmt.Sprintf("%x", urlSum))

	log.Printf("Fetching url %s\n", url.String())
	args := []string{
		"--write-info-json",
		"--max-filesize", fmt.Sprintf("%d", int(MaxFileSize)),

		// output progress bar as newlines
		"--newline",
		"--progress-template", "%(progress.downloaded_bytes)s of %(progress.total_bytes)s / %(progress.total_bytes_estimate)s eta %(progress.eta)s",

		// Do not use the Last-modified header to set the file modification time
		"--no-mtime",

		"--socket-timeout", fmt.Sprintf("%d", YtdlpSocketTimeoutSec),
		"--no-playlist",
		"-o", diskFileNameTmp + ".%(ext)s",
		"--embed-metadata",

		// extract audio
		"-x",
		// print final output filename (after postprocessing etc)
		"--print-to-file", "after_move:filepath", diskFileNameTmp + ".ext",

		// proto:dash is needed for fast Youtube downloads
		// sort by size, bitrate in ascending order
		"-S", "proto:dash,+size,+br",

		// Faster youtube downloads: this combined with -S proto:dash ensures that we get dash https://github.com/yt-dlp/yt-dlp/issues/7417
		"--extractor-args", "youtube:formats=duplicate",
	}

	if ws.SponsorBlock {
		args = append(args, []string{
			"--sponsorblock-remove", ws.SponsorBlockCats,
		}...)
	}
	if forceOpus {
		args = append(args, []string{
			"--audio-format", "opus",
			"--audio-quality", "32K",
			//	"--postprocessor-args", `ExtractAudio:-compression_level 0`,  // fastest, lowest quality compression
		}...)
	}
	args = append(args, url.String())

	log.Printf("Running command %v\n", append([]string{YTCmd}, args...))
	cmdOutCh, cmdErrCh, err := RunCommandCh(tCtx, YTCmd, args...)
	if err != nil {
		return err
	}

	var info Info
	count := 0
	for {
		count++
		if count > 20 {
			return fmt.Errorf("waited too long for info file")
		}

		infoFileName := diskFileNameTmp + ".info.json"

		time.Sleep(500 * time.Millisecond)
		if _, err := os.Stat(infoFileName); os.IsNotExist(err) {
			continue
		}
		raw, err := os.ReadFile(infoFileName)
		if err != nil {
			return fmt.Errorf("info file read error: %s", err)
		}

		err = json.Unmarshal(raw, &info)
		if err != nil {
			return fmt.Errorf("info file json unmarshal error: %s", err)
		}
		if info.FileSize > MaxFileSize {
			return fmt.Errorf("filesize %d too large", info.FileSize)
		}
		m := Msg{Key: "info", Value: info}
		outCh <- m
		break
	}

	// output size of opus file as it gets written
	if forceOpus {
		go getOpusFileSize(tCtx, info, outCh, errCh, diskFileNameTmp+".opus", ws.OutPath)
	}

	var startDownload time.Time
	lastOut := time.Now()
	var line string
	var open bool
loop:
	for {
		select {
		case err := <-errCh:
			return err
		case err, open := <-cmdErrCh:
			if !open {
				break loop
			}
			return err
		case line, open = <-cmdOutCh:
			if !open {
				break loop
			}
			//fmt.Println(line)

			p := getYTProgress(line)
			if p != nil {
				if startDownload.IsZero() {
					startDownload = time.Now()
				}
				pct, err := strconv.ParseFloat(p.Pct, 64)
				if err != nil {
					return err
				}
				if restartCh != nil {
					if time.Since(startDownload) > time.Second*5 && pct < 10 {
						close(restartCh)
						msg := "Restarting download...\n"
						log.Print(msg)
						m := Msg{Key: "unknown", Value: msg}
						outCh <- m
						return nil
					}
				}

				if time.Since(lastOut) > 500*time.Millisecond {
					m := Msg{Key: "progress", Value: *p}
					outCh <- m
					lastOut = time.Now()
				}
			} else {
				m := Msg{Key: "unknown", Value: line}
				outCh <- m
			}
		}
	}

	// if we got here, then command completed successfully

	// yt-dlp writes it's final output filename to a temporary file. Read that back
	diskFileNameTmp2b, err := os.ReadFile(diskFileNameTmp + ".ext")
	if err != nil {
		return err
	}
	diskFileNameTmp2 := strings.TrimSpace(string(diskFileNameTmp2b))
	// read the first line
	idx := bytes.Index(diskFileNameTmp2b, []byte{'\n'})
	if idx > 0 {
		diskFileNameTmp2 = string(diskFileNameTmp2b[:idx])
	}

	log.Println("Fetching title from media file metadata")
	filename := diskFileNameTmp + "." + info.Extension
	if forceOpus {
		filename = diskFileNameTmp + ".opus"
	}

	ff, err := runFFprobe(ctx, FFprobeCmd, filename, ws.Timeout)
	if err != nil {
		return err
	}
	info.Title, info.Artist = titleArtist(ff)

	if info.Title == "" {
		return fmt.Errorf("unknown error (title was empty), last line: '%s'", line)
	}

	if info.Artist == "" {
		info.Artist = "unknown"
	}
	sanitizedTitle := filenameReplacer.Replace(info.Artist + "-" + info.Title)
	sanitizedTitle = filenameRegexp.ReplaceAllString(sanitizedTitle, "")
	sanitizedTitle = strings.Join(strings.Fields(sanitizedTitle), " ") // remove double spaces

	finalFileNameNoExt := filepath.Join(ws.WebRoot, ws.OutPath, "ytdl-"+sanitizedTitle)

	ext := path.Ext(diskFileNameTmp2)
	// rename .opus to .oga. It's already an OGG container and most clients prefer .oga extension.
	if ext == ".opus" {
		ext = ".oga"
	}
	finalFileName := finalFileNameNoExt + ext

	if forceOpus {
		fi, err := os.Stat(diskFileNameTmp2)
		if err != nil {
			return err
		}
		m := Msg{
			Key:   "unknown",
			Value: fmt.Sprintf("opus file size %.2f MB\n", float32(fi.Size())*1e-6),
		}
		outCh <- m
	}
	log.Printf("rename %s to %s", diskFileNameTmp2, finalFileName)
	err = os.Rename(diskFileNameTmp2, finalFileName)
	if err != nil {
		return err
	}

	info.DownloadURL = filepath.Join(ws.OutPath, filepath.Base(finalFileName))
	// don't send link for forceOpus as that's handled in getOpusFileSize goroutine
	if !forceOpus {
		m := Msg{Key: "link_stream", Value: info}
		outCh <- m
	}

	m := Msg{Key: "completed", Value: "true"}
	outCh <- m

	// Send recently retrieved URLs
	recentURLs, err := GetRecentURLs(ctx, ws.WebRoot, ws.OutPath, ws.Timeout)
	if err != nil {
		return fmt.Errorf("WS %s: GetRecentURLS err %w", ws.RemoteAddr, err)
	}
	m = Msg{Key: "recent", Value: recentURLs}
	outCh <- m

	return nil
}

func getYTProgress(v string) *Progress {
	matches := ytProgressRe.FindStringSubmatch(v)

	var p *Progress
	if len(matches) == 5 {
		p = new(Progress)
		downloaded, _ := strconv.Atoi(matches[1])
		total := 0
		// if total_bytes is missing, try total_bytes_estimate
		if matches[2] != "NA" {
			total, _ = strconv.Atoi(matches[2])
		} else {
			// for some reason we get decimal for the estimated bytes
			totalf, _ := strconv.ParseFloat(matches[3], 64)
			// don't care about loss of precision
			total = int(totalf)
		}
		eta, _ := strconv.Atoi(matches[4])
		pct := float64(downloaded) / float64(total) * 100.0
		p.Pct = fmt.Sprintf("%.2f", pct)
		p.FileSize = fmt.Sprintf("%.2f", float64(total)/(1024.0*1024.0))
		p.ETA = fmt.Sprintf("%v", time.Duration(eta)*time.Second)
	}
	return p
}

func getOpusFileSize(ctx context.Context, info Info, outCh chan<- Msg, errCh chan error, filename, webPath string) {
	var startTime time.Time
	ffprobeRan := false
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		opusFI, err := os.Stat(filename)
		// abort on errors except for ErrNotExist
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				errCh <- fmt.Errorf("error getting stat on opus file '%s': %w", filename, err)
				return
			}
			time.Sleep(time.Second)
			continue
		}

		// wait until we have some data before running ffprobe
		if !ffprobeRan && opusFI.Size() > 10000 {
			ff, err := runFFprobe(ctx, FFprobeCmd, filename, time.Second*10)
			if err != nil {
				// FIXME this blocks as nobody is reading
				errCh <- err
				return
			}
			ffprobeRan = true
			info.Title, info.Artist = titleArtist(ff)
			info.DownloadURL = filepath.Join(webPath, "stream", "t", filepath.Base(filename))
			m := Msg{Key: "link_stream", Value: info}
			outCh <- m
		}

		if startTime.IsZero() {
			startTime = time.Now()
		}
		if info.Extension == "mp3" {
			mp3FI, err := os.Stat(strings.TrimSuffix(filename, filepath.Ext(filename)) + ".mp3")
			if err == nil {
				// Opus compression ratio from MP3 approximately 1:4
				pctf := (float64(opusFI.Size()) / (float64(mp3FI.Size()) / 4)) * 100
				pct := fmt.Sprintf("%.1f", pctf)
				diff := time.Since(startTime)
				etaStr := ""
				if pctf > 0 {
					eta := time.Duration((float64(diff) / pctf) * (100 - pctf)).Round(time.Second)
					etaStr = eta.String()
				}
				m := Msg{
					Key: "progress",
					Value: Progress{
						Pct: pct,
						ETA: etaStr,
					},
				}
				outCh <- m
			}
		}
		m := Msg{
			Key:   "unknown",
			Value: fmt.Sprintf("opus file size %.2f MB\n", float32(opusFI.Size())*1e-6),
		}
		outCh <- m
		time.Sleep(3 * time.Second)
	}
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

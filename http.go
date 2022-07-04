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
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// capture progress output e.g '73.2% of 6.25MiB ETA 00:01'
var ytProgressRe = regexp.MustCompile(`([\d.]+)% of ~?([\d.]+)(?:.*ETA ([\d:]+))?`)

const MaxFileSize = 170e6 // 150 MB

// default process timeout in seconds (if not explicitly set via flag)
const DefaultProcessTimeout = 300
const ClientJobs = 5

const PingInterval = 10 * time.Second
const WriteWait = 2 * time.Second

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
var badTitleRegexp = regexp.MustCompile("[0-9A-Fa-f_-]+")

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

type Msg struct {
	Key   string
	Value interface{}
}

type Request struct {
	URL       string
	ForceOpus bool
}

type ffprobe struct {
	Format struct {
		Tags struct {
			Title  string
			Artist string
		}
	}
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
	Timeout          time.Duration
	WebRoot          string
	YTCmd            string
	SponsorBlock     bool
	SponsorBlockCats string
	OutPath          string
	RemoteAddr       string
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
	ws.RemoteAddr = gconn.RemoteAddr().String()
	log.Printf("WS %s: Client connected\n", ws.RemoteAddr)

	// wrap Gorilla conn with our conn so we can extend functionality
	conn := Conn{gconn}

	// setup ping/pong to keep connection open
	go func(remoteAddr string) {
		ticker := time.NewTicker(PingInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				log.Printf("WS %s: ping, context done\n", remoteAddr)
				return
			case <-ticker.C:
				// WriteControl can be called concurrently
				if err := conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(WriteWait)); err != nil {
					log.Printf("WS %s: ping client, err %s\n", remoteAddr, err)
					cancel()
					return
				}
			}
		}
	}(ws.RemoteAddr)

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
		// Send recently retrieved URLs
		recentURLs, err := GetRecentURLs(ws.WebRoot, ws.OutPath)
		if err != nil {
			log.Printf("WS %s: GetRecentURLS err %s\n", ws.RemoteAddr, err)
			return
		}
		m := Msg{Key: "recent", Value: recentURLs}
		conn.writeMsg(m)

		msgType, raw, err := conn.ReadMessage()
		if err != nil {
			log.Printf("WS %s: ReadMessage err %s\n", ws.RemoteAddr, err)
			return
		}

		log.Printf("WS %s: read message %s\n", ws.RemoteAddr, string(raw))

		if msgType == websocket.TextMessage {
			go func() {
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
				return
			}()
		} else {
			log.Printf("unknown message type - close websocket\n")
			conn.Close()
			return
		}
		log.Printf("WS %s: end main loop\n", ws.RemoteAddr)
	}
	log.Printf("WS %s: end ServeHTTP\n", ws.RemoteAddr)
}

func (c *Conn) writeMsg(val interface{}) error {
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
	url, err := url.Parse(req.URL)
	if err != nil {
		return err
	}

	if url.String() == "" {
		return fmt.Errorf("url was empty")
	}

	webFileName := ws.OutPath + "/ytdl-"
	diskFileName := ws.WebRoot + "/" + webFileName

	err = ws.ytDownload(ctx, outCh, url, webFileName, diskFileName, req.ForceOpus)

	return err

}

func (ws *wsHandler) ytDownload(ctx context.Context, outCh chan<- Msg, url *url.URL, webFileName, diskFileName string, forceOpus bool) error {

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
				"-x",
			}...)
		}
		args = append(args, []string{
			"-o", tmpFileName + ".%(ext)s",
		}...)
		args = append(args, url.String())

		log.Printf("Running command %v\n", append([]string{ws.YTCmd}, args...))
		err := RunCommandCh(tCtx, wPipe, ws.YTCmd, args...)
		if err != nil {
			log.Printf("command err %s", err)
			errCh <- err
		}
	}()

	var info Info
	count := 0
	for {
		count++
		if count > 20 {
			return fmt.Errorf("waited too long for info file")
		}

		infoFileName := tmpFileName + ".info.json"

		time.Sleep(500 * time.Millisecond)
		if _, err := os.Stat(infoFileName); os.IsNotExist(err) {
			continue
		}
		raw, err := ioutil.ReadFile(infoFileName)
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
		go func() {
			for {
				select {
				case <-tCtx.Done():
					return
				default:
				}
				fi, err := os.Stat(tmpFileName + ".opus")
				if err == nil {
					m := Msg{
						Key:   "unknown",
						Value: fmt.Sprintf("opus file size %.2f MB\n", float32(fi.Size())*1e-6),
					}
					outCh <- m
				}
				time.Sleep(3 * time.Second)
			}
		}()
	}

	stdout := bufio.NewReader(rPipe)
	lastOut := time.Now()
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

			if info.Title == "" {
				return fmt.Errorf("unknown error (title was empty), last line: '%s'", line)
			}
			if badTitleRegexp.MatchString(info.Title) {
				log.Println("Fetching title from media file metadata")
				args := []string{"-i", tmpFileName + "." + info.Extension,
					"-print_format", "json",
					"-v", "quiet",
					"-show_format",
				}
				ffCtx, cancel := context.WithTimeout(ctx, ws.Timeout)
				defer cancel()
				out, err := exec.CommandContext(ffCtx, "/usr/bin/ffprobe", args...).Output()
				if err != nil {
					return err
				}
				ff := ffprobe{}
				err = json.Unmarshal(out, &ff)
				if err != nil {
					return err
				}
				title := ff.Format.Tags.Title
				artist := ff.Format.Tags.Artist
				if title != "" && artist != "" {
					info.Title = artist + " - " + title
				}
			}
			sanitizedTitle := filenameReplacer.Replace(info.Title)
			sanitizedTitle = filenameRegexp.ReplaceAllString(sanitizedTitle, "")
			sanitizedTitle = strings.Join(strings.Fields(sanitizedTitle), " ") // remove double spaces
			webFileName += sanitizedTitle

			finalFileName := diskFileName + sanitizedTitle + "." + info.Extension
			tmpFileName2 := tmpFileName + "." + info.Extension
			if forceOpus {
				finalFileName = diskFileName + sanitizedTitle + ".oga"
				tmpFileName2 = tmpFileName + ".opus"

				fi, err := os.Stat(tmpFileName2)
				if err != nil {
					return err
				}
				m := Msg{
					Key:   "unknown",
					Value: fmt.Sprintf("opus file size %.2f MB\n", float32(fi.Size())*1e-6),
				}
				outCh <- m
			}
			err = os.Rename(tmpFileName2, finalFileName)
			if err != nil {
				return err
			}
			// if we got here, then command completed successfully
			if forceOpus {
				info.DownloadURL = webFileName + ".oga"
			} else {
				info.DownloadURL = webFileName + "." + info.Extension

			}

			m := Msg{Key: "link", Value: info}
			outCh <- m
			break
		}
		//fmt.Println(line)

		m := getYTProgress(line)
		if m != nil {
			if time.Now().Sub(lastOut) > 500*time.Millisecond {
				outCh <- *m
				lastOut = time.Now()
			}
		} else {
			m := Msg{Key: "unknown", Value: line}
			outCh <- m
		}
	}

	close(outCh)
	return nil
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

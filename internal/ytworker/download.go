package ytworker

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/porjo/ytdl-web/internal/command"
	"github.com/porjo/ytdl-web/internal/jobs"
	"github.com/porjo/ytdl-web/internal/websocket"
)

const (
	DefaultMaxProcessTime = 5 * time.Minute

	MaxFileSize = 1 << 20 * 500 // 500 MiB

	YtdlpSocketTimeoutSec = 10

	KeyCompleted  = "completed"
	KeyUnknown    = "unknown"
	KeyInfo       = "info"
	KeyLinkStream = "link_stream"
)

var (
	// capture progress output e.g '100 of 10000000 / 10000000 eta 30'
	ytProgressRe = regexp.MustCompile(`([\d]+) of ([\dNA]+) / ([\d.NA]+) eta ([\d]+)`)

	noReencodeSites = []string{"youtube.com", "twitter.com", "rumble.com"}

	// filename sanitization
	// swap specific special characters
	filenameReplacer = strings.NewReplacer(
		"(", "_", ")", "_", "&", "+", "—", "-", "~", "-", "¿", "_", "'", "", "±", "+", "/", "-", "\\", "-", "ß", "ss",
		"!", "_", "^", "_", "$", "_", "%", "_", "@", "_", "¯", "-", "`", "_", "#", "", "¡", "_", "ñ", "n", "Ñ", "N",
		"é", "e", "è", "e", "ê", "e", "ë", "e", "É", "E", "È", "E", "Ê", "E", "Ë", "E",
		"à", "a", "â", "a", "ä", "a", "á", "a", "À", "A", "Â", "A", "Ä", "A", "Á", "A",
		"ò", "o", "ô", "o", "ö", "o", "ó", "o", "Ò", "O", "Ô", "O", "Ö", "O", "Ó", "O",
		"ì", "i", "î", "i", "ï", "i", "í", "i", "Ì", "I", "Î", "I", "Ï", "I", "Í", "I",
		"ù", "u", "û", "u", "ü", "u", "ú", "u", "Ù", "U", "Û", "U", "Ü", "U", "Ú", "U",
		"|", "_",
	)

	// remove all remaining non-allowed characters
	filenameRegexp = regexp.MustCompile("[^0-9A-Za-z_ +,-]+")
)

type YTInfo struct {
	Title   string
	Channel string
	Series  string
	//Description          string
	FileSize             int64
	Extension            string        `json:"ext"`
	SponsorBlockChapters []interface{} `json:"sponsorblock_chapters"`
}
type Info struct {
	Id     int64
	Title  string
	Artist string
	//Description  string
	FileSize     int64
	Extension    string
	DownloadURL  string
	SponsorBlock bool

	Progress Progress
}

type Progress struct {
	Pct      float32
	FileSize int64
	ETA      string
}
type Misc struct {
	Id  int64
	Msg string
}

type Download struct {
	OutChan chan websocket.Msg

	maxProcessTime time.Duration

	webRoot          string
	outPath          string
	sponsorBlock     bool
	sponsorBlockCats string
	ytCmd            string

	ctx context.Context
}

func NewDownload(ctx context.Context, webroot, outPath string, sponsorBlock bool, sponsorBlockCats string, ytCmd string, maxProcessTime time.Duration) *Download {

	if maxProcessTime < time.Second*30 {
		maxProcessTime = DefaultMaxProcessTime
	}

	dl := &Download{
		maxProcessTime:   maxProcessTime,
		outPath:          outPath,
		webRoot:          webroot,
		sponsorBlock:     sponsorBlock,
		sponsorBlockCats: sponsorBlockCats,
		ytCmd:            ytCmd,
		ctx:              ctx,
	}
	dl.OutChan = make(chan websocket.Msg)

	go func() {
		<-ctx.Done()
		slog.Info("closing outchan")
		time.Sleep(time.Second)
		close(dl.OutChan)
	}()

	return dl
}

func (yt *Download) Work(j *jobs.Job) {

	id := time.Now().UnixMicro()

	ctx, cancel := context.WithTimeout(yt.ctx, yt.maxProcessTime)
	defer cancel()

	url, err := url.Parse(j.Payload)
	if err != nil {
		slog.Error("unable to parse job URL", "url", j.Payload, "error", err)
		return
	}

	yt.download(ctx, id, yt.OutChan, nil, url)

}

func (yt *Download) download(ctx context.Context, id int64, outCh chan<- websocket.Msg, restartCh chan bool, url *url.URL) error {

	forceOpus := true
	for _, h := range noReencodeSites {
		if strings.Contains(url.Host, h) {
			forceOpus = false
			break
		}
	}

	errCh := make(chan error)
	defer close(errCh)

	// filename is md5 sum of URL
	urlSum := md5.Sum([]byte(url.String()))
	diskFileNameTmp := filepath.Join(yt.webRoot, yt.outPath, "t", "ytdl-"+fmt.Sprintf("%x", urlSum))

	slog.Info("Fetching url", "url", url.String())
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

	if yt.sponsorBlock {
		args = append(args, []string{
			"--sponsorblock-remove", yt.sponsorBlockCats,
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

	slog.Info("Running command", "command", append([]string{yt.ytCmd}, args...))
	cmdOutCh, cmdErrCh, err := command.RunCommandCh(ctx, yt.ytCmd, args...)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			slog.Warn("command terminated due to cancelled context")
		}
		return err
	}

	infoFileName := ""
	count := 0
	for {

		infoFileName = diskFileNameTmp + ".info.json"

		time.Sleep(500 * time.Millisecond)
		_, err := os.Stat(infoFileName)
		if err == nil {
			break
		} else if !os.IsNotExist(err) {
			return err
		}

		if count > 20 {
			return fmt.Errorf("waited too long for info file")
		}
		count++
	}

	raw, err := os.ReadFile(infoFileName)
	if err != nil {
		return fmt.Errorf("info file read error: %s", err)
	}

	var ytInfo YTInfo
	err = json.Unmarshal(raw, &ytInfo)
	if err != nil {
		return fmt.Errorf("info file json unmarshal error: %s", err)
	}

	var info Info
	info.Id = id
	info.Title = ytInfo.Title
	info.Artist = ytInfo.Channel
	if info.Artist == "" {
		info.Artist = ytInfo.Series
	}
	// description is mostly ads!
	//info.Description = ytInfo.Description
	info.FileSize = ytInfo.FileSize
	info.Extension = ytInfo.Extension
	info.SponsorBlock = len(ytInfo.SponsorBlockChapters) > 0

	if info.FileSize > MaxFileSize {
		return fmt.Errorf("filesize %d too large", info.FileSize)
	}

	m := websocket.Msg{Key: KeyInfo, Value: info}
	outCh <- m

	// output size of opus file as it gets written
	if forceOpus {
		go getOpusFileSize(ctx, id, info, outCh, errCh, diskFileNameTmp+".opus", yt.outPath)
	}

	// var startDownload time.Time
	lastOut := time.Now()
	var line string
	var open bool
loop:
	for {
		select {
		case <-ctx.Done():
			slog.Info("download, context done")
			return nil
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
				/*
					if startDownload.IsZero() {
						startDownload = time.Now()
					}
						if restartCh != nil {
							if time.Since(startDownload) > time.Second*5 && p.Pct < 10 {
								close(restartCh)
								misc := Misc{
									Id:  id,
									Msg: "Restarting download...\n",
								}
								m := websocket.Msg{Key: "unknown", Value: misc}
								outCh <- m
								return nil
							}
						}
				*/

				if time.Since(lastOut) > 500*time.Millisecond {
					m := websocket.Msg{
						Key: KeyInfo,
						Value: Info{
							Id:       id,
							Artist:   info.Artist,
							Title:    info.Title,
							FileSize: info.FileSize,
							Progress: *p,
						},
					}
					outCh <- m
					lastOut = time.Now()
				}
			} else {
				misc := Misc{
					Id:  id,
					Msg: line,
				}
				m := websocket.Msg{Key: KeyUnknown, Value: misc}
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

	if info.Title == "" {
		info.Title = "unknown"
	}

	if info.Artist == "" {
		info.Artist = "unknown"
	}
	// swap specific special characters
	sanitizedTitle := filenameReplacer.Replace(info.Artist + "-" + info.Title)
	// remove all remaining non-allowed characters
	sanitizedTitle = filenameRegexp.ReplaceAllString(sanitizedTitle, "")
	sanitizedTitle = strings.Join(strings.Fields(sanitizedTitle), " ") // remove double spaces
	sanitizedTitle = "ytdl-" + sanitizedTitle
	// check maximum filename length
	// 255 is common max length, but 100 is enough
	if len(sanitizedTitle) > 100 {
		sanitizedTitle = sanitizedTitle[:100]
	}

	finalFileNameNoExt := filepath.Join(yt.webRoot, yt.outPath, sanitizedTitle)

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
		m := websocket.Msg{
			Key: KeyUnknown,
			Value: Misc{
				Id:  id,
				Msg: fmt.Sprintf("opus file size %.2f MB\n", float32(fi.Size())*1e-6),
			},
		}
		outCh <- m
	}
	slog.Info("rename file", "src", diskFileNameTmp2, "dst", finalFileName)
	err = os.Rename(diskFileNameTmp2, finalFileName)
	if err != nil {
		return err
	}

	info.DownloadURL = filepath.Join(yt.outPath, filepath.Base(finalFileName))
	// don't send link for forceOpus as that's handled in getOpusFileSize goroutine
	if !forceOpus {
		m := websocket.Msg{Key: KeyLinkStream, Value: info}
		outCh <- m
	}

	m = websocket.Msg{
		Key: KeyCompleted,
		Value: Misc{
			Id:  id,
			Msg: "",
		},
	}
	outCh <- m

	return nil
}

func getYTProgress(v string) *Progress {
	matches := ytProgressRe.FindStringSubmatch(v)

	//slog.Debug("yt progress matches", "matches", matches)

	var p *Progress
	if len(matches) == 5 {
		p = new(Progress)
		downloaded, _ := strconv.Atoi(matches[1])
		var total int64
		// if total_bytes is missing, try total_bytes_estimate
		if matches[2] != "NA" {
			total, _ = strconv.ParseInt(matches[2], 10, 64)
		} else {
			// for some reason we get decimal for the estimated bytes
			totalf, _ := strconv.ParseFloat(matches[3], 64)
			// don't care about loss of precision
			total = int64(totalf)
		}
		eta, _ := strconv.Atoi(matches[4])
		p.Pct = float32(downloaded) / float32(total) * 100.0
		p.FileSize = total
		p.ETA = fmt.Sprintf("%v", time.Duration(eta)*time.Second)
	}
	return p
}

func getOpusFileSize(ctx context.Context, id int64, info Info, outCh chan<- websocket.Msg, errCh chan error, filename, webPath string) {
	var startTime time.Time
	streamURLSent := false

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			opusFI, err := os.Stat(filename)
			// abort on errors except for ErrNotExist
			if err != nil {
				if !errors.Is(err, fs.ErrNotExist) {
					errCh <- fmt.Errorf("error getting stat on opus file '%s': %w", filename, err)
					return
				}
				continue
			}

			// wait until we have some data before sending stream URL
			if !streamURLSent && opusFI.Size() > 10000 {
				info.DownloadURL = filepath.Join(webPath, "stream", "t", filepath.Base(filename))
				m := websocket.Msg{Key: KeyLinkStream, Value: info}
				outCh <- m
				streamURLSent = true
			}

			if startTime.IsZero() {
				startTime = time.Now()
			}
			if info.Extension == "mp3" {
				mp3FI, err := os.Stat(strings.TrimSuffix(filename, filepath.Ext(filename)) + ".mp3")
				if err == nil {
					// Opus compression ratio from MP3 approximately 1:4
					estTotal := mp3FI.Size() / 4
					pct := (float32(opusFI.Size()) / float32(estTotal)) * 100
					diff := time.Since(startTime)
					etaStr := ""
					if pct > 0 {
						eta := time.Duration((float32(diff) / pct) * (100 - pct)).Round(time.Second)
						etaStr = eta.String()
					}
					m := websocket.Msg{
						Key: KeyInfo,
						Value: Info{
							Id:       id,
							Artist:   info.Artist,
							Title:    info.Title,
							FileSize: estTotal,
							Progress: Progress{
								Pct:      pct,
								FileSize: estTotal,
								ETA:      etaStr,
							},
						},
					}
					outCh <- m
				}
			}
			m := websocket.Msg{
				Key: KeyUnknown,
				Value: Misc{
					Id:  id,
					Msg: fmt.Sprintf("opus file size %.2f MB\n", float32(opusFI.Size())*1e-6),
				},
			}
			outCh <- m
		}
	}
}

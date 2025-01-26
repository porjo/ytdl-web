package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/porjo/ytdl-web/internal/jobs"
	"github.com/porjo/ytdl-web/internal/util"
	"github.com/porjo/ytdl-web/internal/ytworker"
	sse "github.com/tmaxmax/go-sse"
)

const MaxProcessTime = time.Second * 300

func main() {

	ytCmd := flag.String("cmd", "/usr/bin/yt-dlp", "path to yt-dlp")
	ffprobeCmd := flag.String("ffprobe", "/usr/bin/ffprobe", "path to ffprobe")
	sponsorBlock := flag.Bool("sponsorBlock", false, "enable SponsorBlock ad removal")
	sponsorBlockCats := flag.String("sponsorBlockCategories", "sponsor", "set SponsorBlock categories (comma separated)")
	webRoot := flag.String("webRoot", "html", "web root directory")
	outPath := flag.String("outPath", "dl", "where to store downloaded files (relative to web root)")
	maxProcessTime := flag.Duration("timeout", MaxProcessTime, "maximum processing time")
	expiry := flag.Duration("expiry", DefaultExpiry, "expire downloaded content")
	port := flag.Int("port", 8080, "listen on this port")
	debug := flag.Bool("debug", false, "debug logging")
	flag.Parse()

	// setup logging
	programLevel := new(slog.LevelVar) // Info by default
	so := &slog.HandlerOptions{Level: programLevel}
	if *debug {
		//so.AddSource = true
		programLevel.Set(slog.LevelDebug)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, so))
	slog.SetDefault(logger)

	outPathFull := filepath.Join(*webRoot, *outPath)

	slog.Info("starting ytdl-web...")
	slog.Info("set web root", "webroot", *webRoot)
	slog.Info("set process timeout", "timeout", *maxProcessTime)
	slog.Info("set output path", "output_path", outPathFull)
	slog.Info("set content expiry", "expiry", *expiry)

	// create tmp dir
	if err := os.MkdirAll(filepath.Join(outPathFull, "t"), os.ModePerm); err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}

	err := mime.AddExtensionType(".oga", "audio/ogg")
	if err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())

	dl := ytworker.NewDownload(ctx, *webRoot, *outPath, *sponsorBlock, *sponsorBlockCats, *ytCmd, *maxProcessTime)
	dispatcher := jobs.NewDispatcher(dl, 10)
	go func() {
		slog.Info("starting job dispatcher")
		dispatcher.Start(ctx)
	}()

	s := &sse.Server{
		OnSession: func(s *sse.Session) (sse.Subscription, bool) {

			logger.Debug("sse session started", "remote_addr", s.Req.RemoteAddr)

			return sse.Subscription{
				Client:      s,
				LastEventID: s.LastEventID,
				Topics:      []string{sse.DefaultTopic},
			}, true
		},
	}

	pingType, err := sse.NewType("ping")
	if err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}

	go func() {
		defer logger.Debug("downloader outCh read loop, done")

		pingTick := time.NewTicker(5 * time.Second)

		for {
			select {
			case <-ctx.Done():
				return
			case <-pingTick.C:

				slog.Debug("ping tick")

				sseM := &sse.Message{
					Type: pingType,
				}
				sseM.AppendData("ping")
				err = s.Publish(sseM)
				if err != nil {
					logger.Error("SSE publish error", "error", err)
					return
				}
			case m, open := <-dl.OutCh:
				if !open {
					return
				}
				sseM := &sse.Message{}
				j, _ := m.JSON()
				sseM.AppendData(string(j))

				if m.Key == ytworker.KeyCompleted {
					// on completion, also send recent URLs
					gruCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
					defer cancel()
					recentURLs, err := GetRecentURLs(gruCtx, *webRoot, *outPath, *ffprobeCmd)
					if err != nil {
						logger.Error("GetRecentURLS error", "error", err)
						continue
					}
					m := util.Msg{Key: "recent", Value: recentURLs}
					j, _ = m.JSON()
					logger.Debug("recent", "json", string(j))
					sseM.AppendData(string(j))
				}
				err = s.Publish(sseM)
				if err != nil {
					logger.Error("SSE publish error", "error", err)
					continue
				}
			}
		}
	}()

	dlh := &dlHandler{
		WebRoot:    *webRoot,
		OutPath:    *outPath,
		FFProbeCmd: *ffprobeCmd,
		Dispatcher: dispatcher,
		Downloader: dl,
		Logger:     logger,
		SSE:        s,
	}
	http.HandleFunc("/dl/stream/", ServeStream(*webRoot))
	http.Handle("/", http.FileServer(http.Dir(*webRoot)))

	http.Handle("/sse", s)
	http.Handle("/dl", dlh)
	http.Handle("/recent", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recentURLs, err := GetRecentURLs(r.Context(), *webRoot, *outPath, *ffprobeCmd)
		if err != nil {
			logger.Error("GetRecentURLS error", "error", err)
			return
		}
		m := util.Msg{Key: "recent", Value: recentURLs}
		j, _ := m.JSON()
		sseM := &sse.Message{}
		sseM.AppendData(string(j))
		err = s.Publish(sseM)
		if err != nil {
			logger.Error("SSE publish error", "error", err)
			return
		}
	}))

	slog.Info("starting cleanup routine...")
	go fileCleanup(filepath.Join(*webRoot, *outPath), *expiry)

	slog.Info("listening on port", "port", *port)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", *port),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: HTTPWriteTimeout,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil {
			if !errors.Is(err, http.ErrServerClosed) {
				slog.Error("http server listen", "error", err)
			}
		}
	}()

	// Setting up signal capturing
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)

	// Waiting for SIGINT (kill -2)
	<-stop
	slog.Info("shutting down")
	cancel()

	if err := srv.Shutdown(ctx); err != nil {
		if !errors.Is(err, context.Canceled) {
			slog.Error("http server shutdown", "error", err)
		}
	}

	// wait a bit for things to settle
	time.Sleep(time.Second)

	slog.Info("Exiting")
}

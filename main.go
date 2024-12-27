package main

import (
	"context"
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
	"github.com/porjo/ytdl-web/internal/ytworker"
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
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: programLevel}))
	slog.SetDefault(logger)
	if *debug {
		programLevel.Set(slog.LevelDebug)
	}

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

	ws := &wsHandler{
		WebRoot:    *webRoot,
		OutPath:    *outPath,
		FFProbeCmd: *ffprobeCmd,
		Dispatcher: dispatcher,
		YTworker:   dl,
	}
	http.Handle("/websocket", ws)
	http.HandleFunc("/dl/stream/", ServeStream(*webRoot))
	http.Handle("/", http.FileServer(http.Dir(*webRoot)))

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
			slog.Error("http server listen", "error", err)
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
		slog.Error("http server shutdown", "error", err)
	}

	// wait a bit for things to shut down
	time.Sleep(time.Second)

}

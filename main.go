package main

import (
	"flag"
	"fmt"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func main() {

	ytCmd := flag.String("cmd", "/usr/bin/yt-dlp", "path to yt-dlp")
	ffprobeCmd := flag.String("ffprobe", "/usr/bin/ffprobe", "path to ffprobe")
	sponsorBlock := flag.Bool("sponsorBlock", false, "enable SponsorBlock ad removal")
	sponsorBlockCats := flag.String("sponsorBlockCategories", "sponsor", "set SponsorBlock categories (comma separated)")
	webRoot := flag.String("webRoot", "html", "web root directory")
	outPath := flag.String("outPath", "dl", "where to store downloaded files (relative to web root)")
	timeout := flag.Duration("timeout", DefaultProcessTimeout, "maximum processing time")
	expiry := flag.Duration("expiry", DefaultExpiry, "expire downloaded content")
	port := flag.Int("port", 8080, "listen on this port")
	flag.Parse()

	outPathFull := filepath.Join(*webRoot, *outPath)

	log.Printf("Starting ytdl-web...\n")
	log.Printf("Set web root: %s\n", *webRoot)
	log.Printf("Set process timeout: %s\n", *timeout)
	log.Printf("Set output path: %s\n", outPathFull)
	log.Printf("Set content expiry: %s\n", *expiry)

	// create tmp dir
	if err := os.MkdirAll(filepath.Join(outPathFull, "t"), os.ModePerm); err != nil {
		log.Fatal(err)
	}

	err := mime.AddExtensionType(".oga", "audio/ogg")
	if err != nil {
		log.Fatal(err)
	}

	YTCmd = *ytCmd
	FFprobeCmd = *ffprobeCmd

	ws := &wsHandler{
		WebRoot:          *webRoot,
		Timeout:          *timeout,
		SponsorBlock:     *sponsorBlock,
		SponsorBlockCats: *sponsorBlockCats,
		OutPath:          *outPath,
	}
	http.Handle("/websocket", ws)
	http.HandleFunc("/dl/stream/", ServeStream(*webRoot))
	http.Handle("/", http.FileServer(http.Dir(*webRoot)))

	log.Printf("Starting cleanup routine...\n")
	go fileCleanup(filepath.Join(*webRoot, *outPath), *expiry)

	log.Printf("Listening on :%d...\n", *port)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", *port),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: HTTPWriteTimeout,
	}
	err = srv.ListenAndServe()
	if err != nil {
		log.Fatal(err)
	}
}

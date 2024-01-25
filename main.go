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
	timeout := flag.Int("timeout", DefaultProcessTimeoutSec, "process timeout (seconds)")
	expiry := flag.Int("expiry", DefaultExpirySec, "expire downloaded content (seconds)")
	port := flag.Int("port", 8080, "listen on this port")
	flag.Parse()

	outPathFull := filepath.Join(*webRoot, *outPath)

	log.Printf("Starting ytdl-web...\n")
	log.Printf("Set web root: %s\n", *webRoot)
	log.Printf("Set process timeout: %d sec\n", *timeout)
	log.Printf("Set output path: %s\n", outPathFull)
	log.Printf("Set content expiry: %d sec\n", *expiry)

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
		Timeout:          time.Duration(*timeout) * time.Second,
		SponsorBlock:     *sponsorBlock,
		SponsorBlockCats: *sponsorBlockCats,
		OutPath:          *outPath,
	}
	http.Handle("/websocket", ws)
	http.HandleFunc("/dl/stream/", ServeStream(*webRoot))
	http.Handle("/", http.FileServer(http.Dir(*webRoot)))

	log.Printf("Starting cleanup routine...\n")
	expiryD := time.Second * time.Duration(*expiry)
	go fileCleanup(filepath.Join(*webRoot, *outPath), expiryD)

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

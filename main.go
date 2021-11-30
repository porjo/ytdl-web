package main

import (
	"flag"
	"fmt"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const cleanupInterval = 30 // seconds

func main() {

	ytCmd := flag.String("cmd", "/usr/bin/yt-dlp", "path to yt-dlp")
	sponsorBlock := flag.Bool("sponsorBlock", false, "enable SponsorBlock ad removal")
	sponsorBlockCats := flag.String("sponsorBlockCategories", "sponsor", "set SponsorBlock categories (comma separated)")
	webRoot := flag.String("webRoot", "html", "web root directory")
	outPath := flag.String("outPath", "dl", "where to store downloaded files (relative to web root)")
	timeout := flag.Int("timeout", DefaultProcessTimeout, "process timeout (seconds)")
	expiry := flag.Int("expiry", DefaultExpiry, "expire downloaded content (seconds)")
	port := flag.Int("port", 8080, "listen on this port")
	flag.Parse()

	log.Printf("Starting ytdl-web...\n")
	log.Printf("Set web root: %s\n", *webRoot)
	log.Printf("Set process timeout: %d sec\n", *timeout)
	log.Printf("Set output path: %s\n", *webRoot+"/"+*outPath)
	log.Printf("Set content expiry: %d sec\n", *expiry)

	err := mime.AddExtensionType(".oga", "audio/ogg")
	if err != nil {
		log.Fatal(err)
	}

	ws := &wsHandler{
		WebRoot:          *webRoot,
		Timeout:          time.Duration(*timeout) * time.Second,
		YTCmd:            *ytCmd,
		SponsorBlock:     *sponsorBlock,
		SponsorBlockCats: *sponsorBlockCats,
		OutPath:          *outPath,
	}
	http.Handle("/websocket", ws)
	http.Handle("/", http.FileServer(http.Dir(*webRoot)))

	log.Printf("Starting cleanup routine...\n")
	expiryD := time.Second * time.Duration(*expiry)
	go fileCleanup(*webRoot+"/"+*outPath, expiryD)

	log.Printf("Listening on :%d...\n", *port)
	err = http.ListenAndServe(fmt.Sprintf(":%d", *port), nil)
	if err != nil {
		log.Fatal(err)
	}
}

func fileCleanup(outPath string, expiry time.Duration) {
	visit := func(path string, f os.FileInfo, err error) error {

		if err != nil {
			return err
		}

		if f.IsDir() || strings.HasPrefix(f.Name(), ".") {
			return nil
		}

		// if last modification time is prior to expiry time,
		// then delete the file
		if time.Now().Sub(f.ModTime()) > expiry {
			if err := os.Remove(path); err != nil {
				return err
			}
			log.Printf("old file removed: %s\n", path)
		}
		return nil
	}

	tickChan := time.NewTicker(time.Second * cleanupInterval).C

	for _ = range tickChan {
		err := filepath.Walk(outPath, visit)
		if err != nil {
			log.Printf("file cleanup error: %s\n", err)
		}
	}
}

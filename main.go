package main

import (
	"flag"
	"log"
	"net/http"
)

func main() {

	ytCmd := flag.String("cmd", "/usr/bin/youtube-dl", "path to youtube-dl")
	webRoot := flag.String("webRoot", "html", "web root directory")
	outPath := flag.String("outPath", "dl", "where to store downloaded files (relative to web root)")
	timeout := flag.Int("timeout", DefaultProcessTimeout, "process timeout (seconds)")
	expiry := flag.Int("expiry", DefaultExpiry, "expire downloaded content (seconds)")
	flag.Parse()

	log.Printf("Starting ytdl-web...\n")
	log.Printf("Set web root: %s\n", *webRoot)
	log.Printf("Set process timeout: %d sec\n", *timeout)
	log.Printf("Set output path: %s\n", *webRoot+"/"+*outPath)
	log.Printf("Set content expiry: %d sec\n", *expiry)

	ws := &wsHandler{
		WebRoot: *webRoot,
		Timeout: *timeout,
		YTCmd:   *ytCmd,
		OutPath: *outPath,
	}
	http.Handle("/websocket", ws)
	http.Handle("/", http.FileServer(http.Dir(*webRoot)))

	log.Printf("Starting cleanup routine...\n")
	go contentCleanup(*outPath, *expiry)

	log.Printf("Listening on :3000...\n")
	http.ListenAndServe(":3000", nil)

}

func contentCleanup(outPath string, expiry int) {

}

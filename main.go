package main

import (
	"log"
	"net/http"
)

const (
	ytCmd = "/usr/bin/youtube-dl"
)

func main() {

	log.Printf("Starting ytdl-web...\n")

	http.HandleFunc("/websocket", wsHandler)
	webRoot := "html"
	http.Handle("/", http.FileServer(http.Dir(webRoot)))

	log.Printf("Listening on :3000...\n")
	http.ListenAndServe(":3000", nil)

}

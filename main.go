package main

import (
	"log"
)

const (
	ytCmd = "/usr/bin/youtube-dl"
)

type Info struct {
	Title     string `json:"title"`
	Extension string `json:"ext"`
}

func main() {

	log.Printf("Starting ytdl-web...\n")

	httpHandlers()

}

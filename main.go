package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"time"
)

const (
	ytCmd = "/usr/bin/youtube-dl"
)

type Info struct {
	Title     string `json:"title"`
	Extension string `json:"ext"`
}

func main() {
	ch := make(chan string)

	fileName := "blahblah"

	go func() {
		err := RunCommandCh(ch, "\r\n", ytCmd, "--write-info-json", "--newline", "-o", fileName, "https://www.youtube.com/watch?v=xwx4QzEAKKw")
		if err != nil {
			log.Fatal(err)
		}
	}()

	var info Info
	go func() {
		for {
			time.Sleep(500 * time.Millisecond)
			if _, err := os.Stat(fileName + ".info.json"); os.IsNotExist(err) {
				continue
			}
			raw, err := ioutil.ReadFile(fileName + ".info.json")
			if err != nil {
				break
			}

			json.Unmarshal(raw, &info)
			fmt.Printf("info %v\n", info)
			break
		}
	}()

	for v := range ch {
		fmt.Println(v)
	}
}

package main

import (
	"context"
	"io/ioutil"
	"log"
	"path/filepath"
	"strings"
	"time"
)

type recent struct {
	URL       string
	Artist    string
	Title     string
	Timestamp time.Time
}

func GetRecentURLs(ctx context.Context, webRoot, outPath string, cmdTimeout time.Duration) ([]recent, error) {
	recentURLs := make([]recent, 0)

	files, err := ioutil.ReadDir(filepath.Join(webRoot, outPath))
	if err != nil {
		return nil, err
	}

	for _, file := range files {
		if !file.IsDir() && file.Name() != ".README" && !strings.HasSuffix(file.Name(), ".json") {
			ff, err := runFFprobe(ctx, FFprobeCmd, filepath.Join(webRoot, outPath, file.Name()), cmdTimeout)
			if err != nil {
				log.Printf("ffprobe error %w\n", err)
				continue
			}
			r := recent{}
			r.URL = filepath.Join(outPath, file.Name())
			r.Title, r.Artist = titleArtist(ff)
			r.Timestamp = file.ModTime()
			recentURLs = append(recentURLs, r)
		}
	}

	return recentURLs, nil
}

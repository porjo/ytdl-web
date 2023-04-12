package main

import (
	"context"
	"io/ioutil"
	"log"
	"os"
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

func DeleteFiles(urls []string, webRoot string) error {

	for _, u := range urls {
		path := filepath.Join(webRoot, u)
		if err := os.Remove(path); err != nil {
			return err
		}
		log.Printf("file removed: %s\n", path)
	}
	return nil
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

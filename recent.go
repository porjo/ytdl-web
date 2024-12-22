package main

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const cleanupInterval = 30 * time.Second

type recent struct {
	URL         string
	Title       string
	Artist      string
	Description string
	Timestamp   time.Time
}

func GetRecentURLs(ctx context.Context, webRoot, outPath string, ffProbeCmd string) ([]recent, error) {
	recentURLs := make([]recent, 0)

	files, err := os.ReadDir(filepath.Join(webRoot, outPath))
	if err != nil {
		return nil, err
	}

	for _, file := range files {
		if !file.IsDir() && file.Name() != ".README" && !strings.HasSuffix(file.Name(), ".json") {
			ff, err := runFFprobe(ctx, ffProbeCmd, filepath.Join(webRoot, outPath, file.Name()))
			if err != nil {
				if errors.Is(context.DeadlineExceeded, err) {
					log.Printf("ffprobe ran too long and was cancelled, error %s\n", err)

				} else {
					log.Printf("ffprobe error %s\n", err)
				}
				continue
			}
			r := recent{}
			r.URL = filepath.Join(outPath, file.Name())
			r.Title, r.Artist, r.Description = titleArtistDescription(ff)
			i, err := file.Info()
			if err != nil {
				log.Print(err)
				continue
			}
			r.Timestamp = i.ModTime()
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
		if time.Since(f.ModTime()) > expiry {
			if err := os.Remove(path); err != nil {
				return err
			}
			log.Printf("old file removed: %s\n", path)
		}
		return nil
	}

	tickChan := time.NewTicker(cleanupInterval).C

	for _ = range tickChan {
		err := filepath.Walk(outPath, visit)
		if err != nil {
			log.Printf("file cleanup error: %s\n", err)
		}
	}
}

package main

import (
	"io/ioutil"
	"strings"
	"time"
)

type recent struct {
	URL       string
	Timestamp time.Time
}

func GetRecentURLs(webRoot, outPath string) ([]recent, error) {
	recentURLs := make([]recent, 0)

	files, err := ioutil.ReadDir(webRoot + "/" + outPath)
	if err != nil {
		return nil, err
	}

	for _, file := range files {
		if !file.IsDir() && file.Name() != ".README" && !strings.HasSuffix(file.Name(), ".json") {
			r := recent{URL: outPath + "/" + file.Name(), Timestamp: file.ModTime()}
			recentURLs = append(recentURLs, r)
		}
	}

	return recentURLs, nil
}

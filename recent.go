package main

import (
	"time"
)

type recent struct {
	URL       string
	Timestamp time.Time
}

var recentURLs = make([]recent, 0)

func GetRecentURLs() []recent {
	return recentURLs
}

func AddRecentURL(u string) error {
	for _, v := range recentURLs {
		if v.URL == u {
			return nil
		}
	}
	r := recent{URL: u, Timestamp: time.Now()}
	recentURLs = append(recentURLs, r)

	if len(recentURLs) > recentURLsCount {
		_ = recentURLs[0]
		// Discard top element
		recentURLs = recentURLs[1:]
	}

	return nil
}

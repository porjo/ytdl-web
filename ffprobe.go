package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type ffprobeTags struct {
	Title  string
	Artist string
}

type ffprobe struct {
	Streams []struct {
		Tags ffprobeTags
	}
	Format struct {
		Tags ffprobeTags
	}
}

func runFFprobe(ctx context.Context, ffprobeCmd, filename string, timeout time.Duration) (*ffprobe, error) {
	ffCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := []string{"-i", filename,
		"-print_format", "json",
		//		"-v", "quiet",
		"-show_streams",
		"-show_format",
	}
	//	fmt.Printf("ffprobe cmd %s, filename %s, args %v\n", ffprobeCmd, filename, args)
	out, err := RunCommand(ffCtx, ffprobeCmd, args...)
	if err != nil {
		return nil, fmt.Errorf("error running ffprobe: '%w'", err)
	}

	ff := &ffprobe{}
	err = json.Unmarshal(out, ff)
	if err != nil {
		return nil, err
	}

	return ff, nil
}

func titleArtist(ff *ffprobe) (title, artist string) {
	if ff.Format.Tags.Title != "" {
		title = ff.Format.Tags.Title
	}
	if ff.Format.Tags.Artist != "" {
		artist = ff.Format.Tags.Artist
	}

	if title == "" && len(ff.Streams) > 0 {
		title = ff.Streams[0].Tags.Title
		artist = ff.Streams[0].Tags.Artist
	}
	if title == "" {
		title = "unknown title"
	}
	if artist == "" {
		artist = "unknown artist"
	}
	return
}

package main

import (
	"context"
	"encoding/json"
	"fmt"
)

type ffprobeTags struct {
	Title       string
	Artist      string
	Show        string
	Description string
}

type ffprobe struct {
	Streams []struct {
		Tags ffprobeTags
	}
	Format struct {
		Tags ffprobeTags
	}
}

func runFFprobe(ctx context.Context, ffprobeCmd, filename string) (*ffprobe, error) {
	args := []string{"-i", filename,
		"-print_format", "json",
		//		"-v", "quiet",
		"-show_streams",
		"-show_format",
	}
	//	fmt.Printf("ffprobe cmd %s, filename %s, args %v\n", ffprobeCmd, filename, args)
	out, err := RunCommand(ctx, ffprobeCmd, args...)
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

func titleArtistDescription(ff *ffprobe) (title, artist, description string) {
	if ff.Format.Tags.Title != "" {
		title = ff.Format.Tags.Title
	}
	if ff.Format.Tags.Artist != "" {
		artist = ff.Format.Tags.Artist
	}
	if ff.Format.Tags.Description != "" {
		description = ff.Format.Tags.Description
	}

	if title == "" && len(ff.Streams) > 0 {
		title = ff.Streams[0].Tags.Title
		artist = ff.Streams[0].Tags.Artist
		if artist == "" && ff.Streams[0].Tags.Show != "" {
			artist = ff.Streams[0].Tags.Show
		}
		description = ff.Streams[0].Tags.Description
	}
	if title == "" {
		title = "unknown title"
	}
	if artist == "" {
		artist = "unknown artist"
	}
	return
}

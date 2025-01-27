## ytdl-web

[![](https://img.shields.io/docker/automated/porjo/ytdl-web.svg)](https://github.com/users/porjo/packages/container/package/ytdl-web)

Simple web app that takes a Youtube video URL (or any URL supported by [yt-dlp](https://github.com/yt-dlp/yt-dlp)) and produces a downloadable audio file.

![Screenshot](https://github.com/porjo/ytdl-web/blob/master/screenshot.png?raw=true)

### Features

- playblack of audio in the browser with skip and speed controls
- mp3 files converted to Opus format on server-side and available immediately via streamed audio (no waiting for re-encode) 
- previous downloads displayed on page, with customizable expiry to auto-remove old files
- supports [SponsorBlock](https://github.com/ajayyy/SponsorBlock) for removing sponsor segments in a video. Just add the `-sponsorBlock` command parameter. See [yt-dlp doco](https://github.com/yt-dlp/yt-dlp#sponsorblock-options) for more details.

### Usage

All command line parameters are optional.

```
Usage of ./ytdl-web:
  -cmd string
    	path to yt-dlp (default "/usr/bin/yt-dlp")
  -expiry int
    	expire downloaded content (seconds) (default 7200)
  -outPath string
    	where to store downloaded files (relative to web root) (default "dl")
  -port int
    	listen on this port (default 8080)
  -sponsorBlock
    	enable SponsorBlock ad removal
  -sponsorBlockCategories string
    	set SponsorBlock categories (comma separated) (default "sponsor")
  -timeout int
    	process timeout (seconds) (default 300)
  -webRoot string
    	web root directory (default "html")
```

### Install

Use prebuilt Docker image from container registry:
```
$ docker run -it -p 8080:8080 ghcr.io/porjo/ytdl-web
```

or clone this Github repo and run `docker build` on it.

then:
- (optionally) configure [Caddy](https://caddyserver.com) proxy (see config below)

### Caddy config

```
example.com {
	encode gzip
	reverse_proxy /* h2c://localhost:8080
}
```
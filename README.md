## ytdl-web

[![](https://img.shields.io/docker/automated/porjo/ytdl-web.svg)](https://github.com/users/porjo/packages/container/package/ytdl-web)

Simple web app that takes a Youtube video URL (or any URL supported by [yt-dlp](https://github.com/yt-dlp/yt-dlp)) and produces a downloadable audio file.

![Screenshot](https://porjo.github.io/ytdl-web/screenshot.png)

Supports [SponsorBlock](https://github.com/ajayyy/SponsorBlock) for removing sponsor segments in a video. Just add the `-sponsorBlock` command parameter. See [yt-dlp doco](https://github.com/yt-dlp/yt-dlp#sponsorblock-options) for more details.

By default, the audio codec will be whatever codec is used in the video container (typically AAC). The 'Opus Audio' toggle switch will force the audio to be encoded with Opus for smaller file size.

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
	# Origin and Host header values must match
	reverse_proxy /websocket localhost:8080 {
		header_up Host {host}
	}
	reverse_proxy /* localhost:8080
}
```

## ytdl-web

[![](https://img.shields.io/docker/automated/porjo/ytdl-web.svg)](https://hub.docker.com/r/porjo/ytdl-web)

Simple web app that takes a Youtube video URL and produces a downloadable audio file.

![Screenshot](https://porjo.github.io/ytdl-web/screenshot.png)

### Install

Use prebuilt Docker image from Docker hub:
```
$ docker pull porjo/ytdl-web
```

or clone this Github repo and run `docker build` on it.

then:
- (optionally) configure [Caddy](https://caddyserver.com) proxy (see config below)
- run Docker container: `docker run -it -p 8080:8080 ytdl-web`

### Caddy config

```
	# Origin and Host header values must match
	proxy /yt/websocket localhost:8080 {
		without /yt
		websocket
		header_upstream Host {host}
	}


	proxy /yt localhost:8080 {
		without /yt
		transparent
	}
```

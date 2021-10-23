## ytdl-web

[![](https://img.shields.io/docker/automated/porjo/ytdl-web.svg)](https://github.com/users/porjo/packages/container/package/ytdl-web)

Simple web app that takes a Youtube video URL and produces a downloadable audio file.

![Screenshot](https://porjo.github.io/ytdl-web/screenshot.png)

### Install

Use prebuilt Docker image from container registry:
```
$ docker pull ghcr.io/porjo/ytdl-web:latest
```

or clone this Github repo and run `docker build` on it.

then:
- (optionally) configure [Caddy](https://caddyserver.com) proxy (see config below)
- run Docker container: `docker run -it -p 8080:8080 ytdl-web`

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

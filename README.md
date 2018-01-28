## ytdl-web

Simple web app that takes a Youtube video URL and produces a downloadable audio file.

![Screenshot](https://porjo.github.io/ytdl-web/screenshot.png)

### Install

Use prebuilt Docker image from Docker hub:
```
$ docker pull porjo/ytdl-web
```

or clone this Github repo and run `docker build` on it.

then:
- (optionally) configure Nginx proxy (see config below)
- run Docker container: `docker run -it -p 8080:8080 ytdl-web`

### Nginx config

When using Nginx as a frontend proxy, the following location block config can be used:


```
		location /yt/websocket {
			proxy_pass         http://127.0.0.1:8080/websocket;
			proxy_redirect     off;
			proxy_set_header   Host $host;
			proxy_set_header   X-Forwarded-For $proxy_add_x_forwarded_for;
			proxy_set_header   Upgrade $http_upgrade;
			proxy_set_header   Connection "Upgrade";
		}

		location /yt/ {
			proxy_pass         http://127.0.0.1:8080/;
			proxy_redirect     off;
			proxy_set_header   Host $host;
			proxy_set_header   X-Forwarded-For $proxy_add_x_forwarded_for;
		}
```

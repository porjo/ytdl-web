# Build stage
FROM golang:alpine AS build-env

COPY . /go/src/github.com/porjo/ytdl-web
WORKDIR /go/src/github.com/porjo/ytdl-web

RUN apk update && \
    apk upgrade && \
	apk add git

RUN go get github.com/gorilla/websocket
RUN go get github.com/porjo/braid

RUN go build -o ytdl-web

# Final stage
FROM alpine

RUN apk update && apk upgrade

RUN apk --update add --no-cache ca-certificates curl python
RUN curl -L https://yt-dl.org/downloads/latest/youtube-dl -o /usr/local/bin/youtube-dl
RUN chmod a+rx /usr/local/bin/youtube-dl

# Update youtube-dl once a week
RUN echo '0 0 * * * /usr/local/bin/youtube-dl -U' >> /etc/crontabs/root

WORKDIR /app/ytdl-web
COPY --from=build-env /go/src/github.com/porjo/ytdl-web/ /app/ytdl-web

RUN chmod a+rx entrypoint.sh

ENTRYPOINT ["/app/ytdl-web/entrypoint.sh"]


# Build stage
FROM golang:alpine AS build-env

COPY . /go/src/github.com/porjo/ytdl-web
WORKDIR /go/src/github.com/porjo/ytdl-web

RUN apk update && \
    apk upgrade && \
	apk add git

RUN go get github.com/gorilla/websocket

RUN go build -o ytdl-web

# Final stage
FROM alpine

RUN apk --update add --no-cache youtube-dl

WORKDIR /app/ytdl-web
COPY --from=build-env /go/src/github.com/porjo/ytdl-web/ /app/ytdl-web
ENTRYPOINT ["/app/ytdl-web/ytdl-web"]

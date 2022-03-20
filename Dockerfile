# Build stage
FROM golang:alpine AS build-env

COPY . /go/src/github.com/porjo/ytdl-web
WORKDIR /go/src/github.com/porjo/ytdl-web

RUN apk update && \
    apk upgrade && \
	apk add git

RUN go build -o ytdl-web

# Final stage
FROM alpine

RUN apk update && apk upgrade

RUN apk --update add --no-cache ca-certificates curl python3 ffmpeg
RUN curl -L https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp -o /usr/local/bin/yt-dlp
RUN chmod a+rx /usr/local/bin/yt-dlp

# Update yt-dlp once a week
RUN echo '0 0 * * * /usr/local/bin/yt-dlp -U' >> /etc/crontabs/root

WORKDIR /app/ytdl-web
COPY --from=build-env /go/src/github.com/porjo/ytdl-web/ /app/ytdl-web

RUN chmod a+rx entrypoint.sh

ENTRYPOINT ["/app/ytdl-web/entrypoint.sh"]
CMD ["-cmd", "/usr/local/bin/yt-dlp", "-sponsorBlock"]

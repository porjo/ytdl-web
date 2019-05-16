#!/bin/ash

crond &

/app/ytdl-web/ytdl-web -cmd /usr/local/bin/youtube-dl

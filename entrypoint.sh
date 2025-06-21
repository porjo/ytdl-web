#!/bin/ash

crond

# update yt-dlp on launch
yt-dlp -U

exec "$@"

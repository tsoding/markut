# Markut

Given the [VOD](https://help.twitch.tv/s/article/video-on-demand) of the stream and the timestamps file, generate a video using [ffmpeg](https://www.ffmpeg.org/) that cuts out part of the VOD according to the provided markers.

![thumbnail](./thumbnail.png)

## Quick Start

Install [Go](https://golang.org/) and [ffmpeg](https://www.ffmpeg.org/).

```console
$ go build
$ ./markut final -markut marks.markut -input vod.mp4 -delay 4
```

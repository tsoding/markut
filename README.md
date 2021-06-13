# Markut

Given the [VOD](https://help.twitch.tv/s/article/video-on-demand) of the stream and the [markers](https://help.twitch.tv/s/article/creating-highlights-and-stream-markers) that are exported as a [CSV](https://en.wikipedia.org/wiki/Comma-separated_values) file, generate a video using [ffmpeg](https://www.ffmpeg.org/) that cuts out part of the VOD according to the provided markers.

![thumbnail](https://i.imgur.com/shk7eqG.png)

## Quick Start

```console
$ ./markut.py -c marks.csv -i vod.mp4 -d 4
```

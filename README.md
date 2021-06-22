# Markut

Given the [VOD](https://help.twitch.tv/s/article/video-on-demand) of the stream and the [markers](https://help.twitch.tv/s/article/creating-highlights-and-stream-markers) that are exported as a [CSV](https://en.wikipedia.org/wiki/Comma-separated_values) file, generate a video using [ffmpeg](https://www.ffmpeg.org/) that cuts out part of the VOD according to the provided markers.

![thumbnail](https://i.imgur.com/shk7eqG.png)

## Quick Start

```console
$ ./markut.py -c marks.csv -i vod.mp4 -d 4
```

## Type-Checking

The project uses [Python 3 typing](https://docs.python.org/3/library/typing.html) that is automatically checked with [mypy](http://mypy-lang.org/) on each commit. You can do that locally too:

```console
$ pip install mypy
$ mypy markut.py
```

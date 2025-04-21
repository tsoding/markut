# Markut

You describe how you want to edit your video in a `MARKUT` file using a simple [Stack-Based Language](https://en.wikipedia.org/wiki/Stack-oriented_programming) and Markut translates it to a sequence of ffmpeg command and assembles the final video. I'm using this tools to edit my VODs that I upload at [Tsoding Daily](https://youtube.com/@TsodingDaily) YouTube channel.

## Quick Start

Install [Go](https://golang.org/) and [ffmpeg](https://www.ffmpeg.org/).

```console
$ go build
```

To get the list of markut subcommands do

```console
$ ./markut help
```

To get the list of functions of the stack language do

```console
$ ./markut funcs
```

<!-- TODO: document available stacks of Markut language -->
<!-- TODO: document available types and values of Markut language -->
<!-- TODO: document available commands of Markut language -->

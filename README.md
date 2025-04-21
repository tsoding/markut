# Markut

Given the [VOD](https://help.twitch.tv/s/article/video-on-demand) of the stream and the timestamps file, generate a video using [ffmpeg](https://www.ffmpeg.org/) that cuts out part of the VOD according to the provided markers.

## Quick Start

Install [Go](https://golang.org/) and [ffmpeg](https://www.ffmpeg.org/).

```console
$ go build
$ ./markut final -markut MARKUT -y
```

<!-- TODO: document available stacks of Markut language -->
<!-- TODO: document available types and values of Markut language -->
<!-- TODO: document available commands of Markut language -->

## Example of a Markut file

```c
// You can use C-style comments
/* Inline comments work too if you're into that */

// Markut is a stack based language

23           // A single number is seconds.
             // This is 23 seconds.

23.69        // Seconds may have fractional part.
             // This is 23 seconds and 690 milliseconds.

chunk        // Pop two timestamps out of the operand stack
             // and form a video chunk out of them.

45  50 chunk // Timestamps don't have to be on the same line.
             // Useful when you want to visually denote a range.

69           // This is 1 minute and 9 seconds.
1:09         // This is also 1 minute and 9 seconds.
chunk

24:45:09     // 24 hours, 45 minutes and 9 seconds.
24:45:09.69  // 24 hours, 45 minutes, 9 seconds and 690 milliseconds.
chunk
```

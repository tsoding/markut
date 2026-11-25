# Allow rendering chunks in parallel

- STATUS: OPEN
- PRIORITY: 99
- TAGS: scope

Which means that instead of using `unfinished-chunk.mp4` we should come up with a unique name per rendered chunk.

Maybe it should be based on TASK(20261122-194210)? Basically `chunks/<hash>-unfinished.mp4`.

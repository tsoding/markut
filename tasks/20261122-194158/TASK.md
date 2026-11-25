# Allow rendering chunks in parallel

- STATUS: CLOSED
- PRIORITY: 99

The goal is actually to enable rendering cuts while the entire video
is rendering with `final`. Literally had this use case recently and
the tool wasn't supporting it.

Which means that instead of using `unfinished-chunk.mp4` we should
come up with a unique name per rendered chunk.

Maybe it should be based on TASK(20261122-194210)? Basically
`chunks/<hash>-unfinished.mp4`.

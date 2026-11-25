# Disable chapters through func

- STATUS: CLOSED
- PRIORITY: 100

```
<timestamp> "Ignored chapter" chapter_ignore
<timestamp> "Active chapter" chapter
```

The only thing you need to do is `s/chapter/chapter_ignore` and vise versa.

---

TASK(20261125-151329) is an interesting idea, but it will take too much time
to implement and I'm not even sure if it's worth it.

---

We already have `drop` which works on argument stack.
I think for the sake of consistency we should introduce `drop_chapter` 
and rename `removed` to `drop_chunk`.


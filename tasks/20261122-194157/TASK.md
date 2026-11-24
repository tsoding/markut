# Disable chapters through func

- STATUS: OPEN
- PRIORITY: 100
- TAGS: scope

```
<timestamp> "Ignored chapter" chapter_ignore
<timestamp> "Active chapter" chapter
```

The only thing you need to do is `s/chapter/chapter_ignore` and vise versa.

---

There is another idea I had. An `ignore` magical function that works on anything.

```
<timestamp> "Ignored chapter" ignore chapter
<start> <end> ignore chunk
```

What it does is executes the next function after it making it consume and verify its arguments
but completely supresses its effect. I'm not sure how to implement this though.

---

But what will happen if you do `ignore` twice?

```
... ignore ignore chunk
```

Does it `ignore` the `ignore` effectively cancelling it out?
Sounds like a reasonable thing to do in this case actually.

---

You should also be able to ignore certain arguments this way

```
ignore <timestamp> // "ignores" so it's consumed but never pushed onto the argument stack
```

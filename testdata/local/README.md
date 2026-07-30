# Local story fixtures

Commercial story files, fetched on demand and never committed.

**These exist to test this engine.** They are not a distribution channel, and
nothing here supplies story files for running a real service. To use the
commercial editions she owns, the client provides her own copies from the media
she bought; this repository does not supply them and grants no right to them.

**[obtaining-story-files.md](obtaining-story-files.md) has the steps** — both
the fetch script and the route for supplying your own files — along with the
rules on what must never happen to them.

`testdata/stories/` holds Zork I, II and III as Microsoft released them under
the MIT Licence. This directory holds a *different edition of the same three
games*: the ones on *The Lost Treasures of Infocom*, which the client owns and
which the embedding server may one day be asked to host.

Those carry no redistribution licence. Only this file, `fetch.sh` and
`obtaining-story-files.md` are in git; the story files themselves are
gitignored, exactly as `references/` treats the specifications it fetches.

## Fetching them

```sh
sh testdata/local/fetch.sh
```

The script pulls the volume 1 disc image from the Internet Archive item
[`lost-treasures-of-infocom`](https://archive.org/details/lost-treasures-of-infocom),
extracts `PC/DATA/ZORK{1,2,3}.DAT`, deletes the image, and checks that each file
is the version, release and serial expected. It needs `curl` and either
`bsdtar` (which ships with macOS) or `7z`. It is idempotent, and does nothing if
the files are already here.

It produces:

| File | Version | Release | Serial | Checksum |
| --- | --- | --- | --- | --- |
| `zork1-r88-840726.z3` | 3 | 88 | 840726 | `0xa129` |
| `zork2-r48-840904.z3` | 3 | 48 | 840904 | `0xd899` |
| `zork3-r17-840727.z3` | 3 | 17 | 840727 | `0x2e7a` |

SHA-256, should a fetch ever need checking against a known-good copy:

```
0ae5ac229e79094ff368b6669356444af0f35e21d862a1baaa546989085c15fd  zork1-r88-840726.z3
abf145d22371f825f13388587d92632bcde90582698f774b896b123a90e1fb1e  zork2-r48-840904.z3
dce7e6f757fb8379dea9da9c13cdda5412ba03fa9b70d79fb6b8c7faf5970692  zork3-r17-840727.z3
```

## What they are for

They are a second edition to test against, which is worth more than a second
copy of the same thing.

The Microsoft Zork I is release 119; this one is release 88. Both are Version 3
and both load, but **they are different story files**, so a saved state from one
is not valid for the other — and that is the story-identity rule the engine
enforces through the Quetzal `IFhd` chunk, available here as a real case rather
than a constructed one.

They also settle a question that was open while planning the compatibility
policy. The first editions of Zork I and Zork II were compiled as Z-machine
Version 1 and Version 2, which this engine refuses by design. Appendix F of
Standard 1.1 catalogues which releases those were, and the Lost Treasures
edition is not among them: it shipped the 1984 re-releases, all Version 3. The
`fetch.sh` checks assert exactly that, so a disc image that ever produced
something else would fail loudly rather than quietly widening what the engine is
expected to run.

One quirk worth knowing. The files are padded to 92,160 bytes on the disc, while
their headers declare 84,876, 89,912 and 82,714. `LoadStory` reads the declared
length and ignores the trailing padding, so they need no trimming — and a host
handing over a file straight off a disc image needs to do nothing special
either.

## Using them in tests

Tests that use these files must skip themselves when the files are absent, the
way the tests over `testdata/stories/` already do. A developer who has not run
`fetch.sh`, and any environment that cannot reach the Internet Archive, must
still see a passing `go test ./...`.

Do not add these to `testdata/stories/`, do not commit them under any name, and
do not make any test require them.

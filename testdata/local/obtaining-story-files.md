# Obtaining the story files

Steps for populating `testdata/local/`, and the boundary around what that is
for.

## Scope: this is for testing the engine

The files in this directory exist so that this engine can be tested against a
second edition of Zork I, II and III. That is their only purpose here.

This directory is **not** a distribution channel, and nothing in this repository
supplies story files for running a real service:

- The engine ships no story files and needs none. It is a library; the host
  provides the story.
- `testdata/stories/` holds the Microsoft release, which is MIT-licensed and may
  be redistributed. It is committed for that reason.
- `testdata/local/` holds commercial files that may **not** be redistributed.
  They are gitignored, fetched on demand, and deleted whenever convenient.
- `fetch.sh` is a testing convenience for developers of this engine. **It is not
  a licence, and it is not a supply route for anyone's production deployment.**

**To run the commercial editions she owns, the client must provide her own
copies, taken from the media she bought.** This repository does not supply them
to her, and no part of it grants a right to them.

## Step 1 — choose how the files get here

There are two routes. They put the same three files in the same place.

### Route A: fetch them (engine testing)

For developing and testing this engine:

```sh
sh testdata/local/fetch.sh
```

The script pulls the volume 1 disc image of *The Lost Treasures of Infocom* from
the Internet Archive, extracts `PC/DATA/ZORK{1,2,3}.DAT`, deletes the image, and
verifies what it produced. It needs `curl` and either `bsdtar` (which ships with
macOS) or `7z`. Running it twice is harmless; it does nothing if the files are
already present.

### Route B: supply your own (files you own)

For anyone working from media they own, and the only route that applies to using
the commercial editions in earnest:

1. Read the story files off your own media — the disc, the floppies, or an
   installed copy.
2. Copy them into `testdata/local/`.
3. Name them `game-rRELEASE-SERIAL.z3`, matching how `testdata/stories/` names
   its own. The release number and serial code are printed by step 2 below.

The tests look for these three names:

```
zork1-r88-840726.z3
zork2-r48-840904.z3
zork3-r17-840727.z3
```

Those are the Lost Treasures releases. A different edition will have different
numbers, and the tests will skip rather than fail — see
[Other editions](#other-editions).

Files do not need trimming. Story files taken off a disc are usually padded well
past the length their header declares, and `LoadStory` reads the declared length
and ignores the rest.

## Step 2 — check what you have

The first byte of a story file is its Z-machine version, bytes 2–3 are the
release number, and bytes 18–23 the serial code:

```sh
for f in testdata/local/*.z3; do
	printf '%s: v%s r%s/%s\n' "$(basename "$f")" \
		"$(od -An -tu1 -N1 -j0 "$f" | tr -d ' ')" \
		"$(od -An -tu1 -N2 -j2 "$f" | tr '\n' ' ' | awk '{print $1 * 256 + $2}')" \
		"$(dd if="$f" bs=1 skip=18 count=6 2>/dev/null)"
done
```

Expected for the Lost Treasures edition:

| File | Version | Release | Serial | Checksum |
| --- | --- | --- | --- | --- |
| `zork1-r88-840726.z3` | 3 | 88 | 840726 | `0xa129` |
| `zork2-r48-840904.z3` | 3 | 48 | 840904 | `0xd899` |
| `zork3-r17-840727.z3` | 3 | 17 | 840727 | `0x2e7a` |

**Version 3 is the thing to check.** This engine implements Version 3 only, and
`LoadStory` refuses everything else. The first editions of Zork I and Zork II
were compiled as Version 1 and Version 2 and will not load; Appendix F of the
Z-Machine Standards Document 1.1 catalogues which releases those are. The Lost
Treasures edition is not among them — it shipped the 1984 re-releases.

## Step 3 — run the tests

```sh
go test -run TestLocal ./...
go test ./...
```

With the files present the tests over them run. Without them, they skip:

```
--- SKIP: TestLocalEditionsAreVersion3
--- SKIP: TestLocalEditionsRunAcrossARequestBoundary
--- SKIP: TestStateDoesNotCrossEditions
```

A skip here is expected and correct. No test in this package may require these
files: `go test ./...` must pass for a developer who has never run `fetch.sh`
and in any environment that cannot reach the Internet Archive.

## Other editions

The tests name the Lost Treasures releases specifically, because they assert the
release and serial they expect. Files from another edition will simply be
skipped rather than tested — the names will not match.

That is deliberate rather than a limitation. `TestStateDoesNotCrossEditions`
exists to prove that a saved state from Zork I release 119 is refused by a
machine built from release 88, so the test needs to know exactly which two
editions it is holding. Testing a third would mean adding it explicitly.

## Rules

- **Never commit these files.** `.gitignore` keeps everything in this directory
  out of git except `README.md`, `fetch.sh` and this document. Do not add an
  exception, and do not move the files into `testdata/stories/`.
- **Never redistribute them**, including to the team embedding this engine. They
  obtain their own copies, as the client does.
- **Never make a test require them.** Absence is skipped, never failed.
- Deleting the whole directory's contents at any time is safe.

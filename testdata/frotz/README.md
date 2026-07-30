# Frotz fixtures

Files produced by [Frotz](https://gitlab.com/DavidGriffith/frotz), so that this
engine can be compared against an interpreter someone else wrote. An engine
tested only against its own reading of the standard proves that it agrees with
itself; these prove it agrees with a program people actually use.

They are committed so that `differential_test.go` is part of an ordinary
`go test ./...` run. `dfrotz` is a tool used to make them, never a dependency of
the engine or of its tests. The two tests that genuinely need the binary live in
`differential_live_test.go` and skip without it.

Made with Frotz 2.55 (`dfrotz -v`), at `/opt/homebrew/bin/dfrotz` on this
machine.

## What is here

| File | What it is |
|---|---|
| `zork1-r119-trapdoor.qzl` | A save: Living Room, lamp taken, rug moved, trap door open, 10 moves |
| `zork1-r119-aboveground-s20000.txt` | A transcript: 25 commands that never leave the surface |
| `zork1-r119-underground-s20000.txt` | A transcript: down the trap door and six rounds against the troll |

The release number ties a file to a story in `../stories/`. A save restored
against the wrong story is the easiest mistake to make here, and the one the
Quetzal package is designed to refuse.

`-s20000` is the random seed. It is part of the name because a different seed is
a different run rather than a newer version of this one, and it is above 1000
deliberately: Frotz's `seed_random` treats a seed below 1000 as a request to
count rather than to generate, so a smaller one would never exercise the
generator at all.

## Making the save

`dfrotz` plays a game the way a person does, so a save is made by playing to a
spot and typing `save`.

```sh
cd testdata/frotz
printf 'open mailbox\ntake leaflet\nnorth\neast\nopen window\nwest\nwest\ntake lamp\nmove rug\nopen trap door\nsave\nzork1-r119-trapdoor.qzl\nquit\ny\n' |
	dfrotz -p -m -w 80 ../stories/zork1-r119-880429.z3
```

Three things to know:

- **Frotz appends `.qzl` to any name that does not already end that way.** Ask
  for `trapdoor.sav` and you get `trapdoor.sav.qzl`. Naming files `.qzl` in the
  first place is why this recipe has no rename step.
- **The file lands in the current directory**, not next to the story, so `cd`
  first.
- **`-p -m -w 80`** turn off formatting codes, MORE prompts, and guessing at the
  terminal width. Without them the game may wait for a keypress that never
  comes.

Check a save before trusting it, by restoring it in the same run:

```sh
printf 'restore\nzork1-r119-trapdoor.qzl\nlook\nquit\ny\n' |
	dfrotz -p -m -w 80 ../stories/zork1-r119-880429.z3
```

If `look` describes the room you expected, the save is good.

If you add a save, add it to `frotzSaves` in `differential_test.go` along with
the commands that produced it. The commands are what let this engine be played
to the same position, so a save without them cannot be compared against
anything.

## Making the transcripts

These are not made by hand: the command lists live in `differential_test.go`,
and keeping a shell recipe in step with them would be one more thing to forget.
Regenerate them from the installed `dfrotz` instead.

```sh
ZMACHINE_UPDATE_GOLDEN=1 go test -run TestFrotzGoldensAreCurrent -v .
```

**Read the diff before committing a regenerated transcript.** The file is the
standard the engine is measured against, so rewriting it to make a test pass is
the one way these tests can be made worthless. A change here means either Frotz
changed or the engine did, and only the first is a reason to keep the new file.

The flags the transcripts were recorded with are `dfrotzFlags` in
`differential_live_test.go`. Changing them invalidates every file here.

## Why a save cannot be compared byte for byte

`TestDifferentialGameState` compares the object tree, the object attributes and
the three globals S 8.2 defines. It deliberately does not compare dynamic memory
byte for byte, and the reason is worth recording.

Frotz writes its file from *inside* the `save` instruction, part way through the
turn that asked for it. Its text buffer already holds `save` while the parser
scratch beyond the globals still holds what the previous turn left there. This
engine can only be observed at a read boundary, where all of that has settled.
No choice of command list makes the two line up: stopping one command earlier
matches the scratch and not the buffer, and one command later matches the buffer
and not the scratch.

What both interpreters do agree on, exactly, is the state of play of S 6.1 —
every object's parent, sibling, child and all thirty-two of its attributes, for
all 255 objects. That is the thing worth checking, and it is checked in full.

## What else would be worth having

- A save taken deeper in the game, where more of the object tree has moved. The
  Cellar needs the lamp lit and the trap door open and is the usual next step.
- Saves from a second interpreter. A disagreement between this engine and Frotz
  says only that the two differ; a third opinion says which one is unusual.
  Bocfel and jzip both write Quetzal.
- Transcripts for Zork II and Zork III, which are already in `../stories/`.
  Nothing about the harness is specific to Zork I except the fixtures.

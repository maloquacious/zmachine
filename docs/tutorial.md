# Tutorial: play three turns of Zork I

In this tutorial we will drive a Z-machine story from Go. We will load Zork I,
print its opening, play three commands, and — the part this package is built
around — play each of those commands on a `Machine` that we create, use once and
throw away.

By the end we will have a program that prints this:

```
loaded release 119, serial 880429, 86838 bytes
ZORK I: The Great Underground Empire
Infocom interactive fiction - a fantasy story
Copyright (c) 1981, 1982, 1983, 1984, 1985, 1986 Infocom, Inc. All rights reserved.
ZORK is a registered trademark of Infocom, Inc.
Release 119 / Serial number 880429

West of House
You are standing in an open field west of a white house, with a boarded front door.
There is a small mailbox here.

>open mailbox
Opening the small mailbox reveals a leaflet.

>take leaflet
Taken.

>read leaflet
"WELCOME TO ZORK!

ZORK is a game of adventure, danger, and low cunning. In it you will explore some of the most amazing territory ever seen by mortals. No computer should be without one!"

>
[ West of House   Score: 0   Moves: 3   486 bytes of state ]
```

We will build it in four steps, running the program after each one.

This tutorial teaches nothing about how a Z-machine works. For that, read the
[README](../README.md) afterwards; for what each call and field means, the
[reference](reference.md).

---

## Before we start

We will need **Go 1.26 or later** and **git**:

```sh
go version
git version
```

Everything else — the package and the story file — comes from the repository we
are about to clone.

---

## Step 1: load the story

First, clone the repository and move into it:

```sh
git clone https://github.com/maloquacious/zmachine.git
cd zmachine
```

The story we will play is already there, next to its licence:

```sh
ls testdata/stories
```

```
LICENSE.zork1.txt
LICENSE.zork2.txt
LICENSE.zork3.txt
zork1-r119-880429.z3
zork2-r63-860811.z3
zork3-r25-860811.z3
```

We will write our program in a new directory inside the clone, which lets it use
both the package and the story file with no further setup:

```sh
mkdir play
```

Now create `play/main.go`:

```go
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/maloquacious/zmachine"
)

func main() {
	data, err := os.ReadFile("testdata/stories/zork1-r119-880429.z3")
	if err != nil {
		log.Fatal(err)
	}

	story, err := zmachine.LoadStory(data)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("loaded release %d, serial %s, %d bytes\n",
		story.Release(), story.Serial(), story.Size())
}
```

Run it from the top of the clone:

```sh
go run ./play
```

The first run downloads the one dependency, so it may pause for a few seconds
before printing:

```
loaded release 119, serial 880429, 86838 bytes
```

We have a `Story`. `LoadStory` checked the whole file before returning it, so
those three numbers came from a header we now know is well formed.

If instead we see `no such file or directory`, we are not at the top of the
clone: the program reads the story by a path relative to the working directory,
so `go run ./play` must be run from the same place `ls testdata/stories` worked.

---

## Step 2: start the story

A `Story` does not run; a `Machine` does. Let's make one and start it.

Add the `context` import and the last two blocks:

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/maloquacious/zmachine"
)

func main() {
	data, err := os.ReadFile("testdata/stories/zork1-r119-880429.z3")
	if err != nil {
		log.Fatal(err)
	}

	story, err := zmachine.LoadStory(data)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("loaded release %d, serial %s, %d bytes\n",
		story.Release(), story.Serial(), story.Size())

	machine, err := zmachine.New(story)
	if err != nil {
		log.Fatal(err)
	}

	opening, err := machine.Start(context.Background())
	if err != nil {
		log.Fatal(err)
	}

	fmt.Print(opening.Output)
}
```

Run it again:

```sh
go run ./play
```

```
loaded release 119, serial 880429, 86838 bytes
ZORK I: The Great Underground Empire
Infocom interactive fiction - a fantasy story
Copyright (c) 1981, 1982, 1983, 1984, 1985, 1986 Infocom, Inc. All rights reserved.
ZORK is a registered trademark of Infocom, Inc.
Release 119 / Serial number 880429

West of House
You are standing in an open field west of a white house, with a boarded front door.
There is a small mailbox here.

>
```

Notice that the last thing printed is `>`, with no newline after it, and that
our program then exited. The story printed its prompt and asked for a line;
`Start` returned at that point rather than waiting for one. Notice too that we
used `fmt.Print` and not `fmt.Println` — the story's own whitespace is in
`opening.Output` exactly as the story wrote it, and we are careful not to add to
it.

Run the program a few times. The opening is the same every time.

---

## Step 3: play one command

The story is waiting for a line, so let's give it one. Add these lines to the end
of `main`, after `fmt.Print(opening.Output)`:

```go
	first, err := machine.Run(context.Background(), "open mailbox")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("open mailbox")
	fmt.Print(first.Output)
	fmt.Printf("\n[ %s   Score: %d   Moves: %d ]\n",
		first.StatusLine.Name, first.StatusLine.Score, first.StatusLine.Turns)
```

We print the command ourselves, on the line the story left its prompt on,
because nothing echoes it for us.

```sh
go run ./play
```

The last few lines are now:

```
>open mailbox
Opening the small mailbox reveals a leaflet.

>
[ West of House   Score: 0   Moves: 1 ]
```

Notice that the story printed a second `>`: it took our command, answered it, and
stopped at the next prompt. And notice where the status line came from — the
story never printed `West of House   Score: 0   Moves: 1`. It handed those values
to us in `first.StatusLine` and left the drawing to us.

Try changing `"open mailbox"` to `"north"` and running it again, then change it
back.

---

## Step 4: throw the machine away between turns

Everything so far kept one `Machine` alive. Now we will stop doing that, and play
each turn on a machine of its own.

Two things make this possible. `Result.State` is the whole machine, in a few
hundred bytes we can keep. `Restore` puts a fresh machine back at exactly the
point those bytes came from.

Replace the whole of `play/main.go` with this:

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/maloquacious/zmachine"
)

func main() {
	data, err := os.ReadFile("testdata/stories/zork1-r119-880429.z3")
	if err != nil {
		log.Fatal(err)
	}

	story, err := zmachine.LoadStory(data)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("loaded release %d, serial %s, %d bytes\n",
		story.Release(), story.Serial(), story.Size())

	machine, err := zmachine.New(story)
	if err != nil {
		log.Fatal(err)
	}

	opening, err := machine.Start(context.Background())
	if err != nil {
		log.Fatal(err)
	}

	fmt.Print(opening.Output)

	saved := opening.State

	var result zmachine.Result

	for _, command := range []string{"open mailbox", "take leaflet", "read leaflet"} {
		fmt.Println(command)

		result, err = playOneTurn(story, saved, command)
		if err != nil {
			log.Fatal(err)
		}

		fmt.Print(result.Output)
		saved = result.State
	}

	fmt.Printf("\n[ %s   Score: %d   Moves: %d   %d bytes of state ]\n",
		result.StatusLine.Name, result.StatusLine.Score, result.StatusLine.Turns, len(saved))
}

// playOneTurn is a whole turn: a new Machine, the state from last time, one
// command, and the Machine thrown away.
func playOneTurn(story *zmachine.Story, saved []byte, command string) (zmachine.Result, error) {
	machine, err := zmachine.New(story)
	if err != nil {
		return zmachine.Result{}, err
	}

	if err := machine.Restore(saved); err != nil {
		return zmachine.Result{}, err
	}

	return machine.Run(context.Background(), command)
}
```

```sh
go run ./play
```

```
loaded release 119, serial 880429, 86838 bytes
ZORK I: The Great Underground Empire
Infocom interactive fiction - a fantasy story
Copyright (c) 1981, 1982, 1983, 1984, 1985, 1986 Infocom, Inc. All rights reserved.
ZORK is a registered trademark of Infocom, Inc.
Release 119 / Serial number 880429

West of House
You are standing in an open field west of a white house, with a boarded front door.
There is a small mailbox here.

>open mailbox
Opening the small mailbox reveals a leaflet.

>take leaflet
Taken.

>read leaflet
"WELCOME TO ZORK!

ZORK is a game of adventure, danger, and low cunning. In it you will explore some of the most amazing territory ever seen by mortals. No computer should be without one!"

>
[ West of House   Score: 0   Moves: 3   486 bytes of state ]
```

Notice that the transcript is unbroken — the leaflet we took is the leaflet we
read — and that `Moves` reached 3, even though every one of those three turns ran
on a `Machine` that had just been created and was gone before the next line
printed. The only thing that crossed from one turn to the next was `saved`: 486
bytes by the end.

Notice also what `playOneTurn` is. It takes a story, some bytes and a string, and
returns text and new bytes. Give it an HTTP request instead of a `for` loop and
nothing about it has to change. That is the shape this package exists for.

Add a command or two to the list — `"north"` and `"east"` both work from here —
and watch `Moves` follow along.

---

## What we did

We loaded and validated a story once, ran it to its first prompt, and then played
three turns without ever keeping the machine that played them. We saw the story's
text in `Result.Output`, the status line in `Result.StatusLine`, and the whole of
the machine in a few hundred bytes of `Result.State`.

This tutorial has a test that runs the same sequence and checks the same output.
If what you saw differs from what is written here, that is worth reporting:

```sh
go test -run TestTutorial ./
```

## Where to go next

- The [README](../README.md) explains why the package is shaped this way, and
  what a `Story` and a `Machine` each are.
- The [how-to guides](how-to/) cover the goals a real host has: storing the
  state, handling a cancelled request, and serving many players at once.
- The [reference](reference.md) describes every option, every `Result` field and
  every error.

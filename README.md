# zmachine

A small, embeddable Go package that implements a **headless Z-machine Version 3
execution engine**.

It exists for one job: letting a Go server advance a browser-based interactive
fiction session one command at a time. It is not an interpreter you run — there
is no terminal, no prompt, no main loop. It is a library a request handler calls.

The whole package is one invariant:

> Given a validated immutable V3 story, an optional saved state, and at most one
> line of input, execute until the next input boundary, clean termination,
> cancellation, limit, or fault; then return captured output and resumable state
> without process-level side effects.

## Install

```sh
go get github.com/maloquacious/zmachine
```

## The request-oriented model

A conventional interpreter owns the session: it loads a story, loops, blocks on
the keyboard, and writes to a screen. None of that survives contact with a web
server, where a turn is a request, requests are not ordered, and nothing may
block a worker.

So this package inverts it. There are two types:

- **`Story`** is a validated, immutable story file. Loading and validating one is
  the expensive step, and it is done once per process. A `Story` is safe to share
  across goroutines and across every player of that game.
- **`Machine`** is one execution instance. It owns all the mutable state — its
  own copy of dynamic memory, the evaluation stack, the call chain, the program
  counter, the random generator. Creating one is cheap, it is not safe for
  concurrent use, and it is not meant to be kept.

A turn is: create a `Machine`, restore the state from last time, run one command,
take the output and the new state, throw the `Machine` away.

```text
load Story once  ─────────────────────────────────────────────┐
                                                              │
  request ──▶ New(story) ──▶ Restore(saved) ──▶ Run(ctx, cmd) ─┤──▶ Result
                                                              │      ├─ Output
                                     Machine discarded ◀──────┘      ├─ StatusLine
                                                                     └─ State ──▶ storage
```

Doing this on every single turn is observably identical to keeping one `Machine`
alive for the whole game. That equivalence is the central promise of the package,
and it has a test that plays Zork I both ways and compares every turn.

The state is a [Quetzal](https://www.inform-fiction.org/zmachine/standards/quetzal/)
saved game, produced by
[`github.com/maloquacious/quetzal`](https://github.com/maloquacious/quetzal). It
is opaque bytes to the host: store them in a column, a blob, a cache — whatever
the application already does with a session.

## Usage

A web handler, which is the shape this package was designed around:

```go
func play(
	ctx context.Context,
	story *zmachine.Story,
	saved []byte,
	command string,
) (zmachine.Result, error) {
	machine, err := zmachine.New(story)
	if err != nil {
		return zmachine.Result{}, err
	}

	if len(saved) != 0 {
		if err := machine.Restore(saved); err != nil {
			return zmachine.Result{}, err
		}
	}

	return machine.Run(ctx, command)
}
```

Starting a new game is the one case that differs, because a story usually prints
its banner and opening room before it asks for anything. There is no saved state
and no command yet, so `Start` is used instead of `Run`:

```go
story, err := zmachine.LoadStory(storyFileBytes)
if err != nil {
	return err
}

machine, err := zmachine.New(story)
if err != nil {
	return err
}

result, err := machine.Start(ctx)
if err != nil {
	return err
}

fmt.Print(result.Output) // the banner and the first room
save(result.State)       // resume here when the player types
```

`Result` carries everything the host needs and nothing it does not:

| Field | What it is |
| --- | --- |
| `Output` | Text the story printed this turn, whitespace preserved exactly. |
| `UpperWindow` | Text printed while the upper window was selected, kept separate because it overlays fixed screen positions rather than joining the narrative. |
| `StatusLine` | The Version 3 status line — room name, score and turns, or the time. Drawn by the interpreter, so it is reported rather than printed. |
| `State` | The resumable state. Present at every input boundary; `nil` when the story has halted, because there is nothing left to resume. |
| `Status` | `WaitingForInput` or `Halted`. |

### Options

```go
machine, err := zmachine.New(story,
	zmachine.WithLogger(logger),            // diagnostics; never story output
	zmachine.WithRandomSeed(42),            // reproducible execution
	zmachine.WithInstructionLimit(1_000_000), // bound one call
	zmachine.WithTracer(tracer),            // one event per instruction
)
```

A `Machine` with no options discards its diagnostics — it never falls back to
`slog.Default` — and seeds itself unpredictably.

There is one further option, `WithFrotzRandomSeed`, which makes the machine draw
random numbers exactly as Frotz does. It exists so that this engine can be
compared against `dfrotz` turn for turn on stories that use randomness, and it
is not meant for running a real session: the Z-machine standard fixes only that
a seeded generator be reproducible, not which numbers it yields, so two correct
interpreters disagree from the first draw. It also inherits Frotz's own quirk
that a seed below 1000 counts rather than generating, so a comparison should use
a larger one. Ordinary use wants `WithRandomSeed`.

## Safety in a server

Story files and saved states are both treated as hostile binary input, because
in a server they are: one is uploaded, the other comes back from storage or from
a request body.

- **Nothing panics on bad input.** Every address, length, table offset, packed
  address and allocation size derived from a story or a save is checked before
  use. Malformed input is an error with context, not a crash. Panics are reserved
  for genuine internal invariant violations.
- **Errors are classifiable.** Every error wraps one of `ErrInvalidStory`,
  `ErrInvalidState`, `ErrInvalidOpcode`, `ErrMemoryAccess`, `ErrInvalidText`,
  `ErrExecutionLimit` or `ErrExecutionFault`, so a host can tell a bad request
  from a bug. Typed errors (`StoryError`, `MemoryError`, `DecodeError`,
  `TextError`, `ExecutionError`) carry the program counter, opcode and address.
- **Execution is bounded.** Every call to `Start` or `Run` has an instruction
  limit, and honours `context.Context` cancellation. A story that loops forever
  cannot hold a worker.
- **Allocation is bounded.** A saved state cannot ask a `Machine` for a deeper
  call chain or a larger stack than that `Machine` would ever have built itself.
- **Players are isolated.** Machines built from the same `Story` share only
  immutable memory. Nothing mutable lives in a package global.
- **No process-level side effects.** No `os.Exit`, no signal handlers, no
  filesystem, no environment variables, no working-directory dependence. The
  engine knows nothing about HTTP, JSON, WebSockets, databases, sessions or
  terminals.

## Scope

**Version 3 only.** `LoadStory` rejects every other version. This is deliberate:
V3 is a complete, self-consistent target, and generalising for later versions
would cost clarity in the parts that matter here.

Some things belong to the host by design, not by omission:

- **In-story `SAVE` and `RESTORE`** report failure (they do not branch), which is
  a legal Version 3 outcome every story copes with. The host owns persistence,
  and the engine has no filesystem to save to. The story prints its own "Failed."
  message and play continues.
- **Word wrapping** is not done. The host has no screen width, and inserting
  newlines would corrupt the story's own whitespace.
- **Output streams 2 and 4** (transcript and command script) are not provided;
  they write to files. Streams 1 and 3 are implemented in full.
- **Transport, users, storage, transactions, retries and idempotency** are the
  embedding application's.

## Testing

```sh
go test ./...
go test -race ./...
go vet ./...
go test -run '^$' -fuzz FuzzRestore -fuzztime 30s .
```

Unit tests cover each layer against small, hand-built machine states rather than
whole stories, so a rule is proved by the smallest thing that can express it.
Integration tests play Zork I across dozens of create/restore/run/destroy cycles
and assert that it matches continuous execution turn for turn. Fuzz targets cover
every parser exposed to arbitrary bytes: the story header, the instruction
decoder, the Z-string decoder, the object tables and the state adapter.

### Differential tests against Frotz

A separate set of tests compares this engine against Frotz, which is a different
and harder claim than agreeing with our own reading of the standard: a misreading
held consistently throughout this package would satisfy every other test and fail
these. They run in three layers — the transcript, the status line, and the state
of play, where the whole object tree and every attribute are compared against a
save Frotz wrote.

They run as part of an ordinary `go test ./...`, from fixtures committed under
`testdata/frotz`. `dfrotz` is a tool used to make those fixtures, never a
dependency of the engine or of its tests.

Two questions a committed file cannot answer do need `dfrotz` installed, and are
skipped without it: whether the fixtures still match the Frotz people have, and
whether Frotz can take up a save this engine wrote. A skip reads the same as a
pass, so a run that means to check either should set `ZMACHINE_REQUIRE_DFROTZ`,
which turns a missing `dfrotz` into a failure.

```sh
brew install frotz        # or your platform's package
ZMACHINE_REQUIRE_DFROTZ=1 go test ./...
```

Worth doing before committing. See `testdata/frotz/README.md` for how the
fixtures are made and regenerated.

## Story fixtures and licences

`testdata/stories/` holds Zork I, II and III as test inputs. They are **test
fixtures, not dependencies** — nothing in the package needs them, and the tests
skip themselves when the files are absent.

Each is accompanied by its licence (`LICENSE.zork1.txt` and so on); they are
distributed by Microsoft under the MIT License. Respect those licences: they
cover the story files, not this package.

Zork I is the first compatibility target, not the definition of the VM. No
Zork-specific behaviour is hard-coded anywhere in the engine.

## Licence

MIT. See [LICENSE](LICENSE).

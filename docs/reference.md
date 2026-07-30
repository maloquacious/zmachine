# Reference

Technical description of the host-facing API of
`github.com/maloquacious/zmachine`.

This document describes the machinery. For why the package is shaped this way,
see the [README](../README.md); for the full per-symbol signatures, see
[pkg.go.dev](https://pkg.go.dev/github.com/maloquacious/zmachine), which is
generated from the source and is authoritative for anything this document and
the code disagree about.

Section numbers written `S 6.1` refer to the
[Z-Machine Standards Document 1.1](https://www.inform-fiction.org/zmachine/standards/z1point1/).

---

## Contents

- [Types](#types)
- [Loading a story](#loading-a-story)
- [Creating a machine](#creating-a-machine)
- [Options](#options)
- [Executing](#executing)
- [Result](#result)
- [StatusLine](#statusline)
- [Status](#status)
- [Errors](#errors)
- [Tracing](#tracing)
- [Concurrency](#concurrency)
- [Limits](#limits)
- [Not implemented](#not-implemented)
- [Package version](#package-version)

---

## Types

The package exports two opaque types.

| Type | Mutability | Concurrency | Lifetime |
| --- | --- | --- | --- |
| `Story` | Immutable after `LoadStory` returns | Safe for concurrent use | Process-lifetime; loaded once |
| `Machine` | Owns all mutable execution state | Not safe for concurrent use | One request; cheap to create and discard |

A `Machine` holds the Version 3 "state of play" (S 6.1): its own copy of dynamic
memory, the evaluation stack, the chain of routine call frames, and the program
counter. Machines built from the same `Story` share only immutable memory and
are otherwise isolated.

---

## Loading a story

### `func LoadStory(data []byte) (*Story, error)`

Validates `data` as a Version 3 story file and returns an immutable `Story`.

- Every header address and table extent is checked before use.
- The returned `Story` owns a private copy of the image; `data` may be reused or
  modified afterwards.
- Stories of any version other than 3 are rejected.
- Errors wrap `ErrInvalidStory` and are of type `*StoryError`.

### Story methods

| Method | Returns |
| --- | --- |
| `Version() uint8` | The Z-machine version. Always `3`. |
| `Release() uint16` | The release number recorded in the header. |
| `Serial() string` | The six-character serial code, conventionally `YYMMDD`. |
| `Checksum() uint16` | The checksum recorded in the header. Zero in early Version 3 stories that carry none. |
| `Size() int` | Length in bytes of the story image. |

---

## Creating a machine

### `func New(story *Story, opts ...Option) (*Machine, error)`

Creates a `Machine` positioned at the story's initial program counter (S 5.5).

- Only dynamic memory is copied; static and high memory are shared with the
  `Story`.
- Options are applied in order. An option that cannot be satisfied makes `New`
  return an error rather than leaving the machine misconfigured.
- Any number of machines may be created from one `Story`.

---

## Options

| Option | Default when omitted | Rejected when |
| --- | --- | --- |
| `WithLogger(*slog.Logger)` | Diagnostics discarded | `logger` is nil |
| `WithRandomSeed(uint64)` | Seeded unpredictably | — |
| `WithFrotzRandomSeed(uint64)` | Not used | — |
| `WithInstructionLimit(uint64)` | `10_000_000` per call | `limit` is 0 |
| `WithTracer(Tracer)` | Tracing off | `tracer` is nil |

### `WithLogger(logger *slog.Logger) Option`

Sets the logger for interpreter diagnostics.

- Story output is never written to the logger.
- Logging never changes execution semantics.
- A machine created without this option discards diagnostics. It never falls
  back to `slog.Default`.

### `WithRandomSeed(seed uint64) Option`

Places the random number generator in the predictable state of S 2.4.2. Two
machines given the same story and the same seed produce the same sequence.

Without this option the generator is seeded unpredictably, which is the random
state S 2.4 requires at the start of a game.

The story may still change the state itself: `random` with a negative range
reseeds the generator, and `random` with a range of zero reseeds it
unpredictably (S 15).

### `WithFrotzRandomSeed(seed uint64) Option`

Makes the machine draw the numbers Frotz draws, seeded as `dfrotz -s seed`
would seed it. It exists for differential comparison against another
interpreter (S 33).

- S 2.4 fixes only that a seeded generator be reproducible, not which numbers it
  yields. Two conforming interpreters disagree from the first draw.
- Frotz's `seed_random` treats a seed below 1000 as a request to count from 0 to
  `seed-1` forever rather than to generate. A comparison uses a seed of at least
  1000.
- A seed of 0 asks for entropy, as it does in Frotz.

`WithRandomSeed` is the option for reproducible ordinary execution.

### `WithInstructionLimit(limit uint64) Option`

Bounds the number of instructions one call to `Start` or `Run` may execute
(S 25).

- Reaching the limit stops execution with an error wrapping
  `ErrExecutionLimit`.
- The limit applies to each call separately; instructions executed on earlier
  turns do not count against later ones.
- The limit must be positive. There is no way to configure an unbounded machine.

### `WithTracer(tracer Tracer) Option`

Installs a `Tracer` receiving one event per executed instruction (S 30).
Tracing is off unless this option is given.

---

## Executing

### `func (m *Machine) Start(ctx context.Context) (Result, error)`

Begins execution at the story's initial program counter (S 5.5) and runs until
the first input boundary or termination. Supplies no player input.

Refused with an error wrapping `ErrExecutionFault` when:

- the story has already halted;
- execution has already begun, including on a machine that has been restored.

### `func (m *Machine) Run(ctx context.Context, input string) (Result, error)`

Supplies one line of player input and executes until the next input boundary or
termination (S 11).

- The line is given to the story as an interactive interpreter would give it.
  The engine performs the transformations Version 3 requires and leaves the
  story's own parser to interpret the command.
- Refused with an error wrapping `ErrExecutionFault` when the story has halted.

### `func (m *Machine) Restore(data []byte) error`

Replaces the machine's state with one previously returned in `Result.State`
(S 9).

- The machine must have been created from the same story the state was saved
  from. A state belonging to another story is refused.
- Saved state is untrusted input (S 26). Every address, count and length is
  checked before anything is allocated or written.
- On success the machine is at an input boundary and has already begun: the next
  call is `Run`, and `Start` is refused.
- On failure the machine is left exactly as it was, so the error may be reported
  and a different state tried.
- Saves written by this engine and saves written by another interpreter suspend
  at different points. Both are accepted; a foreign save has its program counter
  moved to the input boundary this engine resumes from.
- Errors wrap `ErrInvalidState`.

### `func (m *Machine) Halted() bool`

Reports whether the story has terminated. A halted machine cannot be run again.

### Call sequences

```
new game:        New → Start                        → Result
resumed turn:    New → Restore → Run(command)       → Result
```

`Start` is valid once, on a machine that has neither run nor been restored.
`Run` is valid on a machine that has started or been restored, until it halts.

---

## Result

Returned by `Start` and `Run`.

| Field | Type | Description |
| --- | --- | --- |
| `Output` | `string` | Text the story printed during this call, with its whitespace preserved exactly. Never contains the status line or interpreter diagnostics. |
| `UpperWindow` | `string` | Text printed while the upper window was selected (S 8.6.1). Reported separately because it overlays fixed screen positions rather than joining the narrative. |
| `StatusLine` | `StatusLine` | The status line as of the moment execution stopped. |
| `State` | `[]byte` | Resumable state in the Quetzal format (S 22). Non-nil when `Status` is `WaitingForInput`; nil when `Status` is `Halted`. |
| `Status` | `Status` | Why execution stopped. |

Passing `State` to `Restore` on a machine built from the same `Story` returns
execution to the point the call stopped at. The bytes are opaque to the host.

---

## StatusLine

The Version 3 status line (S 8.2), reported rather than printed because the
interpreter draws it. It is updated in exactly two circumstances: when the story
executes `show_status`, and immediately before a line-input instruction reads
(S 8.2.4).

| Field | Type | Description |
| --- | --- | --- |
| `Available` | `bool` | Whether the status line has been updated at least once. The remaining fields are meaningless until it is true (S 8.2.4). |
| `Object` | `uint16` | Object number held in the first global variable (S 8.2.2). |
| `Name` | `string` | Short name of that object. Empty until the object model is available. |
| `TimeGame` | `bool` | `false` for a score game, `true` for a time game (S 8.2.1). Fixed by bit 1 of Flags 1 in the header. |
| `Score` | `int16` | Score in a score game (S 8.2.3.1). Signed; may be negative. |
| `Turns` | `int16` | Turn count in a score game (S 8.2.3.1). |
| `Hours` | `uint8` | Hours in a time game (S 8.2.3.2). |
| `Minutes` | `uint8` | Minutes in a time game (S 8.2.3.2). |

`Score` and `Turns` are meaningful when `TimeGame` is false; `Hours` and
`Minutes` when it is true.

---

## Status

| Value | Meaning | `Result.State` |
| --- | --- | --- |
| `WaitingForInput` | Execution reached a line-input instruction for which no input was supplied. Resumable by supplying one. | Non-nil |
| `Halted` | The story terminated itself with `quit` (S 15). | `nil` |

Both implement `String()`.

---

## Errors

### Sentinels

Errors arising from the story, the saved state or execution wrap exactly one of
these, so a host can classify a failure with `errors.Is` without depending on
message text.

| Sentinel | Reports |
| --- | --- |
| `ErrInvalidStory` | A story file that is not a usable Version 3 story. |
| `ErrInvalidState` | A saved state that cannot be restored. |
| `ErrInvalidOpcode` | An instruction not defined in Version 3. |
| `ErrMemoryAccess` | A read or write the Version 3 memory model does not permit. |
| `ErrInvalidText` | Encoded text that does not obey the Version 3 rules for Z-strings. Distinct from `ErrInvalidStory` because strings may be built in dynamic memory while the story runs. |
| `ErrExecutionLimit` | Execution stopped because the instruction limit was reached. |
| `ErrExecutionFault` | A condition the Z-machine defines no result for: division by zero (S 2.3.1), evaluation-stack underflow (S 6.3.1), returning from the initial execution environment (S 5.5). Also the lifecycle refusals listed under [Executing](#executing). |

### Errors that do not wrap a sentinel

Two classes of error reach the host without a sentinel.

| Condition | Error returned | Classify with |
| --- | --- | --- |
| The `context` passed to `Start` or `Run` is cancelled or its deadline passes | The context's own error, unwrapped | `errors.Is(err, context.Canceled)`, `errors.Is(err, context.DeadlineExceeded)` |
| A nil `context`, or an option given a nil logger, a nil tracer, or a zero instruction limit | A plain error naming the call | Message only |

The second class reports a programming error at the call site rather than
anything derived from a story or a save.

### Typed errors

Each carries the context needed to diagnose the failure and is reachable with
`errors.As`. All expose `Error() string` and `Unwrap() error`.

| Type | Fields |
| --- | --- |
| `*StoryError` | `Field string`, `Value uint32`, `Detail string`, `Err error` |
| `*MemoryError` | `Op MemoryOp`, `Width int`, `Addr uint32`, `Region Region`, `Detail string`, `Err error` |
| `*DecodeError` | `Addr uint32`, `Opcode uint8`, `Detail string`, `Err error` |
| `*TextError` | `Addr uint32`, `Detail string`, `Err error` |
| `*ExecutionError` | `PC uint32`, `Op` (unexported type; printable), `Detail string`, `Err error` |

`StoryError.Value` is meaningful only when `Field` is set. `DecodeError.Opcode`
is zero when the failure was reading that byte itself. `TextError.Addr` is zero
when the text did not come from story memory.

### MemoryOp

| Value | Meaning |
| --- | --- |
| `MemoryRead` | A load from story memory. |
| `MemoryWrite` | A store into story memory. |

### Region

Names one of the three regions of the memory map (S 1.1).

| Value | Meaning |
| --- | --- |
| `RegionUnknown` | The address does not lie inside the story image. |
| `RegionDynamic` | Readable and writable, below the base of static memory. |
| `RegionStatic` | Readable, not writable. |
| `RegionHigh` | Routines and strings. Its bottom may overlap the top of static memory, so an address reported as high memory may also be readable as static memory. |

Both implement `String()`.

### Example

```go
result, err := machine.Run(ctx, command)
switch {
case err == nil:
	// proceed
case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
	// the request went away
case errors.Is(err, ErrExecutionLimit):
	// the story exceeded its instruction budget this turn
case errors.Is(err, ErrInvalidState):
	// the saved state is unusable
default:
	var fault *ExecutionError
	if errors.As(err, &fault) {
		// fault.PC and fault.Detail locate the instruction
	}
}
```

---

## Tracing

### `type Tracer interface { Instruction(TraceInstruction) }`

Receives one event for each instruction the machine executes. `Instruction` is
called after the instruction has taken effect. An instruction that failed
produces no event; the error describes it instead.

A tracer cannot change what the story does: it is handed copies of the machine's
values and its return is ignored.

### TraceInstruction

| Field | Type | Description |
| --- | --- | --- |
| `PC` | `uint32` | Byte address the instruction was decoded from. |
| `Next` | `uint32` | Program counter after the instruction ran. Differs from the address after the instruction when a branch, jump, call or return moved it. |
| `Opcode` | `string` | The instruction in the form used by S 14, for example `2OP:20 add`. |
| `Operands` | `[]uint16` | Operand values in the order they were evaluated (S 4.5.2). A copy; the tracer may retain it. |
| `CallDepth` | `int` | Routines on the call chain before the instruction ran. Zero in the initial execution environment (S 5.5). |
| `Stored` | `bool` | Whether the instruction wrote a result (S 4.6). |
| `StoreVariable` | `uint8` | Where the result was written. Meaningful when `Stored`. |
| `StoreValue` | `uint16` | What was written. Meaningful when `Stored`. |
| `Branched` | `bool` | Whether a branch instruction took its branch (S 4.7). False for instructions that do not branch. |
| `Called` | `bool` | The instruction entered a routine (S 6.4). |
| `Returned` | `bool` | The instruction left a routine (S 6.5). |
| `ReturnValue` | `uint16` | Meaningful only when `Returned`. |

---

## Concurrency

| Operation | Safe concurrently |
| --- | --- |
| `LoadStory` | Yes |
| Any `Story` method | Yes |
| `New` from one `Story` on many goroutines | Yes |
| Any `Machine` method | No |

One `Story` may back any number of simultaneous machines. A single `Machine`
must not be used from more than one goroutine at a time.

No mutable state is held in a package-level variable.

---

## Limits

| Bound | Value |
| --- | --- |
| Instructions per `Start` or `Run` call | `WithInstructionLimit`, default `10_000_000` |
| Context cancellation latency | Checked periodically during execution, not after every instruction |
| Call-chain depth and stack size restored from a save | Never larger than the machine would build itself |

A saved state cannot ask a machine for a deeper call chain or a larger stack
than that machine would ever have built, which is what keeps a hostile save from
turning a declared length into an allocation (S 26).

---

## Not implemented

These belong to the host or to another Z-machine version.

| Feature | Behaviour here |
| --- | --- |
| Versions other than 3 | `LoadStory` rejects them. |
| In-story `SAVE` and `RESTORE` | Report failure without branching, a legal Version 3 outcome. The story prints its own message and play continues. The host owns persistence. |
| Word wrapping | Not performed. The engine has no screen width, and inserting newlines would corrupt the story's whitespace. |
| Output streams 2 and 4 | Not provided; they write to files. Streams 1 and 3 are implemented in full. |
| Terminal, screen, filesystem, environment, process exit | None are touched. |

---

## Package version

### `func Version() string`

Reports the semantic version of this package, for a host that prints which
engine it is running beside its own build.

It reports nothing about conformance. The engine implements Version 3 and is
written against Standard 1.1, and leaves the header's standard revision number
(`$32`–`$33`, S 11.1) unset rather than claiming a standard the interoperability
testing does not yet justify.

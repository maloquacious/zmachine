# Handle a cancelled request mid-turn

This guide shows you what to do when the context you passed to `Start` or `Run`
is cancelled, or its deadline passes, while the story is still executing.

## Detect it before you classify anything else

Cancellation returns the context's own error, unwrapped. It does not wrap an
engine sentinel, so test for it first:

```go
result, err := machine.Run(ctx, command)
switch {
case err == nil:
	// the turn happened
case errors.Is(err, context.Canceled):
	// the client went away
case errors.Is(err, context.DeadlineExceeded):
	// the turn ran out of time
default:
	// an engine error; classify with the sentinels
}
```

Checking `ErrExecutionFault` or `ErrInvalidState` first will not catch this
case, and a `default` branch that assumes an engine error will report a
disconnected client as an interpreter bug.

## Discard the whole result

The `Result` returned alongside a cancellation error is the zero value. There is
no `Output`, no `StatusLine` and no `State`. Whatever the story printed before
the context was cancelled is gone, and it is not recoverable — there is no
partial turn to salvage.

**Write nothing to storage on this path.** The state you stored at the end of the
previous turn is still the good one, and it is still exactly what it was: the
machine you were running was a copy, and nothing outside it changed.

Discard the machine too. Do not call `Run` on it again.

## Answer the request

What to say depends on which error you got:

- **`context.Canceled`** — the client is gone. There is nobody to answer. Log it
  and return.
- **`context.DeadlineExceeded`** — the player is still there and their turn did
  not happen. Tell them so, and make clear the command was not played: from the
  session's point of view, the turn never occurred, and the next thing they send
  will be played against the state from before it.

Do not present a cancelled turn as a game-over. The session is intact.

## Retry, if you want to

A retry is safe. Nothing was written, and the engine has no side effects outside
the machine you threw away, so replaying the command means building a new
machine, restoring the same stored state, and running the same line:

```go
machine, err := zmachine.New(story)
if err != nil {
	return err
}

if err := machine.Restore(saved); err != nil { // the same saved bytes as before
	return err
}

result, err := machine.Run(ctx, command) // the same command
```

The retry produces the same turn the first attempt would have. The random
generator's state travels in the saved state, so the story draws from where it
left off rather than from a fresh sequence.

Retry only on `DeadlineExceeded`, and only with a fresh context: retrying with
the cancelled one fails immediately. Do not retry `context.Canceled` — the
client that asked for the turn is no longer waiting for it.

## Give the turn a deadline that will fire

Cancellation is checked periodically during execution rather than after every
instruction, so a deadline takes effect promptly but not instantly. Bound the
turn from both ends:

```go
ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
defer cancel()

machine, err := zmachine.New(story, zmachine.WithInstructionLimit(5_000_000))
```

The deadline bounds wall-clock time; the instruction limit bounds work.

## Tell it apart from a turn that ran out of instructions

`ErrExecutionLimit` looks similar — the state is lost, the previous state is
still good — but it is not transient. A retry executes the same instructions and
stops in the same place. Treat it as a story fault: report it, keep the previous
state, and do not replay the command automatically.

---

Related: [errors](../reference.md#errors), [limits](../reference.md#limits),
[`WithInstructionLimit`](../reference.md#withinstructionlimitlimit-uint64-option),
[persist and restore session state](persist-and-restore-session-state.md).

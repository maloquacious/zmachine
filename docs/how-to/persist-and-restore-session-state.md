# Persist and restore session state

This guide shows you how to store the bytes in `Result.State` between turns and
hand them back so a player resumes where they left off.

## Store the state after every turn

`Result.State` is a complete, self-contained snapshot of the machine. Write it
whole, replacing whatever you stored last turn:

```sql
CREATE TABLE session (
    id         TEXT PRIMARY KEY,
    story_key  BLOB NOT NULL,  -- SHA-256 of the story file
    state      BLOB,           -- Result.State; NULL once the story has halted
    updated_at TIMESTAMPTZ NOT NULL
);
```

Every state is independent — not a delta, not a link in a chain — so the most
recent one is the only one you need. Nothing has to be replayed, and states may
be deleted in any order.

Size a `BLOB`/`bytea` column rather than a fixed-width one. A Zork I state is
356 bytes at the opening prompt and around 500 a few turns in; it grows as the
story's dynamic memory diverges from the story file, not with the number of
turns, and a larger story starts larger.

Do not compress the bytes on the way in. Dynamic memory is already stored as a
run-length-compressed difference against the story file, so a second pass buys
almost nothing.

## Store the story file's identity beside it

A state only restores into a machine built from the story it was saved from, so
you must be able to hand back the same file. Key it by a SHA-256 over the story
image, which you have in hand before you call `LoadStory`:

```go
key := sha256.Sum256(storyFileBytes)
```

Use the hash, not `Story.Release()` and `Story.Serial()`. Those identify an
edition rather than a file, and `Story.Checksum()` is 0 for early Version 3
stories that carry none.

You do not need to store which version of this package wrote a state. A state
restores for as long as the story file is byte-identical, whatever engine build
produced it, so stored state needs no migration when you upgrade.

## Restore before each turn

A turn is a new machine, the stored state, one command:

```go
func (s *Server) turn(ctx context.Context, sessionID, command string) (zmachine.Result, error) {
	story, saved, err := s.load(ctx, sessionID)
	if err != nil {
		return zmachine.Result{}, err
	}

	machine, err := zmachine.New(story)
	if err != nil {
		return zmachine.Result{}, err
	}

	if err := machine.Restore(saved); err != nil {
		return zmachine.Result{}, err
	}

	result, err := machine.Run(ctx, command)
	if err != nil {
		return zmachine.Result{}, err
	}

	return result, s.store(ctx, sessionID, result.State)
}
```

The first turn of a game is the exception: there is no state and no command yet,
so call `Start` instead and store the state it returns.

```go
result, err := machine.Start(ctx)
```

Store the state from `Start` the same way you store the state from `Run`. From
the second turn on there is no difference between them.

## Handle a state that is nil

`Result.State` is nil when `Result.Status` is `Halted`: the story ended itself
and there is nothing to resume.

Do not overwrite a good state with nil unless you mean to close the session.
Either mark the session finished and keep the last playable state, or delete the
row — but decide, because storing nil turns the next `Restore` into a failure
you will read as corruption.

## Handle a state that is refused

`Restore` fails with an error wrapping `ErrInvalidState`:

```go
if err := machine.Restore(saved); err != nil {
	if errors.Is(err, zmachine.ErrInvalidState) {
		// the bytes and this story do not belong together
	}
	return err
}
```

That means one of two things: the bytes are damaged, or they belong to a
different story file. Neither is retryable with the same pair. Check the story
key you stored before assuming corruption — a state from Zork I release 119 is
refused by a machine built from release 88, and that refusal is the engine
protecting the session rather than a fault.

`Restore` either succeeds or leaves the machine exactly as it was, so you can
report the failure and try a different state on the same machine.

## Leave the bytes alone

Treat the state as opaque. Do not parse it, edit it, or reach into it for the
score or the room name — `Result.StatusLine` reports those, and any field you
recover by hand ties your host to a save format that is free to change.

If you want undo, or several save slots, keep more than one row rather than
building anything on top of the format. Each state stands alone, so any of them
restores by itself.

---

Related: [`Result`](../reference.md#result),
[saved-state compatibility](../reference.md#saved-state-compatibility),
[errors](../reference.md#errors).

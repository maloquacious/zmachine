# Serve many concurrent players from one story

This guide shows you how to run any number of simultaneous sessions of the same
game in one process, without the players affecting each other.

## Load each story once, at startup

`LoadStory` validates the whole image and is the expensive call. Do it once per
story file, before you start serving, and keep the `*Story` for the life of the
process:

```go
type Library struct {
	stories map[string]*zmachine.Story // keyed by SHA-256 of the story file
}

func NewLibrary(files map[string][]byte) (*Library, error) {
	lib := &Library{stories: make(map[string]*zmachine.Story, len(files))}

	for key, data := range files {
		story, err := zmachine.LoadStory(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		lib.stories[key] = story
	}

	return lib, nil
}
```

Build the map before any request can read it and never write to it again, and it
needs no lock. If stories are uploaded while the server runs, put the map behind
a `sync.RWMutex` or a `sync.Map` — the `*Story` values themselves stay safe to
share.

`LoadStory` copies the image it is given, so the `[]byte` you passed in may be
released, reused or modified afterwards.

## Create a machine per request, not per session

```go
func (s *Server) turn(ctx context.Context, sess *Session, command string) (zmachine.Result, error) {
	machine, err := zmachine.New(s.lib.stories[sess.StoryKey])
	if err != nil {
		return zmachine.Result{}, err
	}

	if err := machine.Restore(sess.State); err != nil {
		return zmachine.Result{}, err
	}

	return machine.Run(ctx, command)
}
```

The machine is local to the call and gone when it returns. Do not cache machines
between turns, and do not pool them: a machine that has run is not reusable for
anything but the session it ran, and keeping one alive costs you the isolation
you get for free by dropping it.

`New` is cheap because it copies only dynamic memory and shares the rest of the
image with the `*Story`. For Zork I that is 11,282 bytes of an 86,838-byte
story, however many players are in the process.

## Do not share a machine

A `*Machine` is not safe for concurrent use. One machine belongs to one
goroutine for the duration of one call, and nothing else may touch it.

Safe to share across goroutines: the `*Story`, and the `*slog.Logger` you pass
to `WithLogger`. Not safe unless you make it so: a `Tracer`. Give each machine
its own tracer, or make the one you install safe for concurrent calls, because
machines running in parallel will call it in parallel.

No mutable state lives in a package-level variable, so machines built from the
same story share nothing but immutable memory. Two players in the same room of
the same game cannot see each other.

## Serialise the turns of a single session

Concurrency between sessions is free. Concurrency *within* one session is a
correctness problem you have to solve, and the engine cannot solve it for you:
two turns that start from the same stored state will both succeed, both write,
and one will be overwritten. The player sees a command vanish.

Take a per-session lock for the whole read-run-write cycle:

```go
sess.mu.Lock()
defer sess.mu.Unlock()
```

A mutex is enough for one process. Across several, make the write conditional on
the state you read — a version column bumped on each turn, and an update that
matches on it — and reject the turn whose version has moved rather than storing
it:

```sql
UPDATE session SET state = $1, version = version + 1
 WHERE id = $2 AND version = $3;
```

Zero rows affected means another turn got there first. Fail that request; do not
replay it against the new state, because the player issued it against the old
one.

## Bound what one player can cost

Every call to `Start` or `Run` honours its context and stops at an instruction
limit, so a story that loops forever cannot hold a worker:

```go
ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
defer cancel()

machine, err := zmachine.New(story,
	zmachine.WithLogger(s.logger),
	zmachine.WithInstructionLimit(5_000_000),
)
```

There is no way to configure an unbounded machine. A machine created without
`WithInstructionLimit` still stops after ten million instructions per call.

---

Related: [concurrency](../reference.md#concurrency),
[limits](../reference.md#limits), [options](../reference.md#options),
[handle a cancelled request mid-turn](handle-a-cancelled-request-mid-turn.md).

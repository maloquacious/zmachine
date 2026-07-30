# Security Policy

This package makes a security claim, and it makes it prominently: story files and
saved states are treated as hostile binary input, every externally derived
address, length, table offset, packed address and allocation size is checked
before use, malformed input is an error rather than a panic, and every parser
exposed to arbitrary bytes has a fuzz target.

That claim is the reason this document exists. A team embedding the engine in a
server is entitled to know how to tell us when the claim turns out to be false,
and what we will do about it.

## Reporting a vulnerability

**Report privately, not as a public issue.**

Use GitHub's private vulnerability reporting, which is enabled on this
repository:

**[Report a vulnerability](https://github.com/maloquacious/zmachine/security/advisories/new)**
— or the *Report a vulnerability* button under the repository's **Security** tab.

The report goes to the maintainers only. It is not visible to anyone else, and it
stays that way until an advisory is published.

A useful report is one we can reproduce. Please include:

- The bytes that trigger it — the story file or the saved state, or a program
  that generates them. A fuzz corpus entry is ideal.
- Which call reaches it: `LoadStory`, `New`, `Restore`, `Start` or `Run`, and any
  options in force.
- What happens: the panic and its stack, the allocation size, the read that goes
  out of bounds, or the wall-clock time and instruction count.
- The Go version, the operating system and the version of this package.

Expect an acknowledgement within a week. If a report is confirmed, the fix ships
in a release and the advisory is published with credit, unless you would rather
not be named.

For anything that is not a vulnerability — a wrong answer from an opcode, a
misdecoded string, a story that will not load — please open an ordinary
[issue](https://github.com/maloquacious/zmachine/issues). Those are more useful
in public.

## Supported versions

| Version | Supported |
| --- | --- |
| Most recent `v0.1.x` release | Yes |
| Anything earlier | No |

This package is at `v0.x` and has one supported version: the latest. Fixes land
there and nothing is backported. If you are pinned to an older release, the
remedy is to upgrade — which the
[saved-state compatibility guarantee](docs/reference.md#saved-state-compatibility)
is designed to make cheap, since stored state does not need migrating when the
engine version changes.

## What counts as a vulnerability

Anything that breaks the safety claim above. Concretely, when it is reachable
from a story file, a saved state or a line of player input:

- **A panic.** Any panic from `LoadStory`, `New`, `Restore`, `Start` or `Run` on
  input the caller did not control. Panics in this package are reserved for
  genuine internal invariant violations; one that a crafted file can reach is by
  definition not that.
- **An out-of-bounds access.** A read or a write outside the memory the machine
  owns, whether it faults or silently returns the wrong bytes.
- **An unbounded allocation.** Any input that makes the engine allocate a size
  derived from the input without a check — a stack, a call chain, a buffer, an
  output accumulator. Memory exhaustion from a few hundred bytes of story is a
  vulnerability even though nothing crashes at the Go level.
- **Unbounded execution.** Any input that runs past the instruction limit, or
  that ignores `context.Context` cancellation, and so holds a worker. The
  guarantee is that `Start` and `Run` return.
- **A break in isolation.** Any way one `Machine` observes or alters another's
  state, or mutates the `Story` that both share. Machines built from the same
  story share only immutable memory, and a `Story` is safe to use from many
  goroutines; a counterexample to either is a vulnerability.
- **A process-level side effect.** The engine does not exit the process, install
  signal handlers, touch the filesystem, open a network connection, read the
  environment or depend on the working directory. Any path that does is a
  vulnerability regardless of how it is reached.

Data races count under isolation: run it under `-race` and say so in the report.

## What does not

The distinction that matters is between the story misbehaving *inside* the VM,
which is the story's business, and the story escaping the VM, which is ours.

- **A story that behaves badly within its own machine.** Corrupting its own
  dynamic memory, jumping to nonsense, printing garbage, looping until the
  instruction limit stops it, or halting with a fault. The engine's job is to
  contain that and report it, not to prevent it, and a `Result` carrying a fault
  status is the containment working.
- **Incorrect Version 3 semantics.** An opcode that computes the wrong answer, a
  property lookup that returns the wrong default, a dictionary that tokenises
  differently from Frotz. These are bugs, sometimes serious ones, and they belong
  in a public issue where they can be discussed against the specification.
- **Anything the host owns.** Transport, authentication, users, sessions,
  storage, transactions, retries, idempotency, and request-level resource limits
  are outside this package by design. So is the decision to accept a story file
  from a user in the first place.
- **The story fixtures.** `testdata/stories/` holds Zork I, II and III as test
  inputs under their own licences. They are not shipped, not imported and not a
  dependency.
- **Missing features.** In-story `SAVE` and `RESTORE`, word wrapping, output
  streams 2 and 4, and every Z-machine version other than 3 are deliberately
  absent. See [Not implemented](docs/reference.md#not-implemented).

## The threat model, and its ceiling

What bounds the severity of anything found here is how little the engine can
reach. It has no filesystem, no network, no subprocesses, no environment and no
process-global state. It cannot escalate, because there is nothing to escalate
to. Its only dependency outside the standard library is
[`github.com/maloquacious/quetzal`](https://github.com/maloquacious/quetzal),
for the saved-game format.

So the realistic worst case for a bug in this package is availability: a crafted
story or saved state takes down the process that embedded it, or exhausts its
memory, or pins a worker. That is a real problem for a server serving many
players from one process, and it is exactly what the checks, the limits and the
fuzz targets are for. It is not remote code execution, and it does not reach the
host's data.

The host still has its own perimeter to keep. If it accepts story files from
users, the size and the rate of those uploads are the host's to bound, and this
package's guarantee starts once the bytes reach `LoadStory`.

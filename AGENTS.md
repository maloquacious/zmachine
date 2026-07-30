# Project Guidance

## Mission

Build a small, embeddable Go package that implements a headless Z-machine Version 3 execution engine. Its primary consumer is a Go web server that advances browser-based interactive-fiction sessions one command at a time.

The defining invariant is:

> Given a validated immutable V3 story, an optional saved state, and at most one line of input, execute until the next input boundary, clean termination, cancellation, limit, or fault; then return captured output and resumable state without process-level side effects.

`specification.md` is the product and architecture specification. Read the relevant sections before making design decisions. The bundled Z-machine specification under `references/z-spec11/` is the authority for VM semantics. If they conflict, preserve correct Version 3 semantics and call out the product-level conflict rather than silently inventing behavior.

## Scope and Boundaries

- Implement Z-machine Version 3 only. Do not generalize for later versions unless a V3 requirement needs the shared mechanism.
- Keep the engine headless. Core code must not know about HTTP, WebSockets, JSON, databases, authentication, sessions, terminals, ANSI, filesystems, or command-line interfaces.
- Never block waiting for player input. An unsatisfied line-input instruction is a suspension boundary returned to the host.
- Capture story output in execution results; do not print it, log it, or send it elsewhere.
- Do not call `os.Exit`, install signal handlers, alter process-global state, depend on the working directory, or read environment variables during normal execution.
- Treat stories and save states as untrusted binary input. Validate every externally derived address, length, table offset, packed address, and allocation size before use. Malformed input should return a contextual error, not panic.
- Panics are reserved for genuine internal invariant violations, not malformed stories, malformed saves, unsupported opcodes, or ordinary execution faults.
- The interpreter owns VM execution. The Quetzal package owns save-format encoding and decoding. The embedding server owns transport, users, persistence, transactions, retries, and idempotency.
- Do not implement the Quetzal binary format locally; integrate `github.com/maloquacious/quetzal` through a narrow state adapter.

## Architecture

- Begin with a flat package and ordinary Go files grouped by responsibility. Add subpackages only for a real API or dependency boundary, not merely to organize names.
- Keep the exported API deliberately small. Prefer opaque `Story` and `Machine` types plus structured results, options, statuses, and classifiable errors.
- `Story` represents validated immutable data and must be safe to share concurrently. `Machine` owns all mutable execution state and need not support concurrent calls.
- Never place mutable VM state in package globals. Independent machines using the same story must remain fully isolated.
- Load and validate a story once; make machine creation cheap. Share static/high memory safely and give each machine private dynamic memory and execution state.
- Separate instruction decoding from execution. Decoded instructions should retain enough context for stores, branches, embedded text, and useful fault messages.
- Keep memory, variable/stack, routine, object/property, text, dictionary/input, output, and persistence concerns independently testable. Prefer direct helpers over interfaces unless substitution is actually needed.
- Make 16-bit signed and unsigned conversions explicit. Do not accidentally inherit host `int` width or overflow behavior.
- Model suspension at a precise input boundary so create/restore/run/destroy is observably equivalent to retaining one machine continuously.
- Each machine must accept and retain an injected `*slog.Logger`. Use that logger for VM diagnostics; never use `slog.Default`, package-level logging functions, or a package-global logger. Logging must not change execution semantics or contain story output.
- Randomness must use `math/rand/v2`, preferably a machine-owned `rand.NewPCG` source. Never import the legacy `math/rand` package or use package-global random functions. Randomness must be injectable or seedable for deterministic tests, and resumable random state must be preserved where required.
- Instruction limits and `context.Context` cancellation are part of the server safety model, not optional CLI conveniences.

## Implementation Practices

- Use idiomatic Go compatible with the version declared in `go.mod`.
- Prefer the standard library and the required Quetzal dependency. Do not introduce terminal, UI, HTTP-framework, CGo, or broad utility dependencies into the engine.
- Favor the smallest readable implementation that follows the specification. Do not prematurely optimize, create speculative extension points, or preserve terminal-oriented abstractions from reference interpreters.
- Add context to errors, especially the program counter, opcode, address, variable, object, or table involved. Preserve stable sentinel/type classification with `%w` where callers need `errors.Is` or `errors.As`.
- Avoid unchecked slicing and arithmetic on story-derived values. Check arithmetic for overflow before converting to `int`, indexing, slicing, or allocating.
- Do not hard-code Zork-specific behavior. Zork I is the first compatibility target, not the definition of the VM.
- Preserve meaningful story whitespace exactly. Keep logical screen/status-line behavior separate from terminal rendering concerns.
- Use structured `log/slog` attributes for diagnostics. Pass the machine's injected logger through the owning type rather than threading global logging state through helpers; use child loggers or contextual attributes where they improve opcode and program-counter diagnostics.
- Optional tracing must be injected, disabled by default, and semantically inert.
- If adapting code from another implementation, verify behavior against the Z-machine specification, retain required attribution/licensing, and reshape it around this project's request-oriented architecture rather than copying terminal lifecycle design.

## Testing and Verification

- Put focused unit tests beside implementation files using Go's standard `testing` package and table-driven cases where they improve clarity.
- Test both successful behavior and malformed-input boundaries for memory, decoding, stacks, routines, variables, branches, objects/properties, text, abbreviations, dictionaries, and tokenization.
- Test opcode behavior with minimal constructed machine states; avoid using an entire story when a small fixture proves the rule more precisely.
- Integration tests must recreate the machine between turns: start, save, discard, create, restore, run one command, and repeat. This is the central architecture contract.
- Use deterministic `math/rand/v2` PCG seeds in tests. Do not make assertions depend on production entropy, and test that save/restore preserves the random sequence when random state is persisted.
- Use a captured `slog.Handler` when testing diagnostics; do not mutate the process-default logger in tests.
- Add fuzz tests for parsers and decoders exposed to arbitrary bytes. At minimum, arbitrary input must not panic or trigger uncontrolled allocation.
- Story fixtures under `testdata/` are test inputs, not production dependencies. Respect their bundled licenses and do not duplicate or modify story binaries casually.
- For localized work, run the narrowest relevant test first. Before considering cross-cutting engine changes complete, run:

```sh
gofmt -w <changed-go-files>
go test ./...
go vet ./...
go test -race ./...
```

- During iteration, fuzz only the affected target with a bounded fuzz time. Do not claim fuzz safety solely from ordinary unit tests.
- A test failure should expose an implementation or fixture problem; do not add story-specific exceptions or weaken bounds checks merely to make it pass.

## Change Discipline

- Commit only when the user requests it or the active workflow explicitly requires a commit.
- When the work resolves a GitHub issue, branch before the first commit, push the branch, and open a pull request that closes the issue. When it does not, commit directly to `main`; do not create a branch merely to imitate a pull-request workflow.
- Assign every issue and pull request you open to `mdhender`.
- Pull requests are squash-merged, and the repository allows no other merge method. One issue's worth of work lands as one commit on `main`, so write the pull request title and body to read as that commit.
- Bump `version` in `version.go` for any change to code: patch for fixes, tests, and internal work that leaves exported behavior alone; minor for new or changed exported behavior. Never bump it for a documentation-only change. `version_test.go` pins the constant and is updated with it.
- Tag the commit `vX.Y.Z` whenever that constant changes, and push the tag. Nothing else records a release: the constant is the only statement of the version in the source, and the tag is the only one Go's module resolution can see.
- Keep behavior changes scoped to Version 3 and to the host-facing execution model in `specification.md`.
- Update public documentation and tests whenever exported behavior changes.
- When a VM rule is subtle, cite the relevant Z-machine specification section in the test name or a short comment; explain the rule, not the mechanics of the code.
- Prefer conformance and differential evidence over intuition for packed addresses, branching, signed arithmetic, object properties, text decoding, and dictionary parsing.
- Correctness, isolation, and host safety take priority over performance. Optimize only after measurement, without weakening validation or request-boundary equivalence.

# Changelog

All notable changes to this project are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

The version constant in `version.go` is the only statement of the release in the
source, and the `vX.Y.Z` git tag is the only one Go's module resolution can see;
the two are maintained together.

## [Unreleased]

## [0.2.0] — 2026-07-30

### Changed

- **`ExecutionError.Op` is now a `string`, not the unexported `opcode` type.**
  It holds the S 14 form of the instruction, for example `"2OP:20 add"` — what
  the old type's `String` method produced, and what `TraceInstruction.Opcode`
  already held. A fault and a trace now name an instruction identically, which
  a new test asserts by faulting on the same opcode the tracer just recorded.

  The field was exported but its type was not, so a host could print it and do
  nothing else — not declare a variable of that type, take one as a parameter,
  store one, or compare one against a named constant. It read as a field whose
  type the documentation would not let you follow.

  **This breaks code that assigned `ExecutionError.Op` to a variable or passed
  it somewhere typed.** Code that only printed or formatted it is unaffected,
  and that is everything the type allowed. Exporting `opcode` instead would
  have dragged `operandCount` and the whole opcode constant table into the API
  for a field that exists to be logged. Doing this at `v0.x` costs a minor
  bump; after `v1.0.0` it would have been a much larger conversation.

## [0.1.7] — 2026-07-30

Runnable examples, and the security policy. No exported behaviour changed.

### Added

- `example_test.go`, four `Example` functions covering what a host actually
  writes: load and `Start`; `Run` a command and read the output and the new
  state; the create/restore/run/discard turn; and classifying an error, with
  the context-cancellation case that wraps no engine sentinel and so has to be
  tested for first. Until now every sample in the README and the reference was
  prose, and nothing compiled it, so a rename left the first thing a new
  consumer copies silently wrong. These are compiled and run by `go test` and
  render on pkg.go.dev beside the symbols they document.
- The examples run against a Version 3 story assembled in the test file rather
  than one from `testdata/stories/`, which a checkout may not have. An example
  carrying an `// Output:` comment cannot skip itself, and a test that skips
  proves nothing.
- `SECURITY.md`. The package claims that hostile story files and saved states
  cannot panic it, over-allocate, read out of bounds or outrun their limits;
  until now there was no answer to the obvious next question, which is what to do
  when the claim turns out to be false. It gives the private reporting channel —
  GitHub private vulnerability reporting, now enabled on the repository — states
  that only the most recent release gets fixes, and draws the line that matters:
  a story misbehaving inside the VM is the story's business, a story escaping it
  is ours. It also bounds the threat model, since an engine with no filesystem,
  network or process access has nothing to escalate to and a ceiling of
  availability.
- A link to it from the README's safety section and its documentation table.

## [0.1.6] — 2026-07-30

Documentation, plus the test that holds one of the documents to its word. No
exported behaviour changed.

### Added

- `docs/tutorial.md`, a first session end to end: load Zork I, start it, play a
  command, and then play three more on a `Machine` created and discarded for
  each one. It is the missing learning-oriented document — the README argues and
  the reference describes, and neither is a lesson.
- `tutorial_test.go`, which runs the tutorial's own program and compares what it
  prints against the blocks the tutorial shows. A tutorial's reader is the one
  person who cannot tell a stale example from their own mistake, so the tutorial
  is not allowed to rot quietly.
- `docs/how-to/`, three guides titled as the goals they achieve: persist and
  restore session state, handle a cancelled request mid-turn, and serve many
  concurrent players from one story. Between the README and the reference there
  was no document that answered "I want to do X — how?".
- Links to both from the README, the reference and each other.

## [0.1.5] — 2026-07-30

### Changed

- **The minimum Go version is now 1.26, down from 1.26.4.** The patch-level
  requirement was never this module's: `github.com/maloquacious/quetzal`
  declared `go 1.26.4`, and Go requires a module's `go` directive to be at least
  that of everything it depends on, so the pin was inherited and could not be
  edited out. Quetzal `v0.2.3` relaxes its own directive, and this follows.
  Builds on Go 1.26.0 through 1.26.3 are no longer refused.
- `github.com/maloquacious/quetzal` upgraded from `v0.2.2` to `v0.2.3`.

### Added

- A saved-state compatibility policy in `docs/reference.md`, with a summary in
  the README where persistence is first mentioned. It states what the state
  bytes are, that a state restores for as long as the story file is unchanged
  and that the engine version is not part of that contract, that a story file is
  better identified by a hash than by the release and serial Quetzal records,
  which restore failures are recoverable and which are not, and a forward-only
  upgrade adapter promise should the format ever change.

## [0.1.4] — 2026-07-30

### Added

- `testdata/local/`, holding the Lost Treasures of Infocom edition of Zork I, II
  and III. These are commercial files with no redistribution licence, so only
  `README.md` and `fetch.sh` are committed and the story files themselves are
  gitignored, the way `references/` already treats what it fetches.
- Tests over that second edition: that all three are Version 3 with the expected
  release, serial and checksum; that each plays across a request boundary; and
  that a saved state from Zork I release 119 is refused by a machine built from
  release 88. The last is the story-identity rule of the compatibility policy,
  proved against two real editions of one game rather than a constructed pair.
- The README states the minimum Go version and why it is pinned to a patch
  release, and states that the exported API may change in a minor release before
  `v1.0.0`.

## [0.1.3] — 2026-07-30

Documentation only. No behaviour changed; the two `.go` files touched carry
comment changes alone.

### Added

- `CHANGELOG.md`, this file.
- `docs/reference.md`, a reference description of the host-facing API: the
  lifecycle calls, every option, every field of `Result` and `StatusLine`, the
  error taxonomy, concurrency, limits, and what is deliberately not implemented.
- A Go Reference badge and a documentation index in the README.

### Changed

- The README is now the front door — what the package is, why it is shaped the
  way it is, and how to start — and defers per-symbol description to
  `docs/reference.md` and to `go doc`.
- `Machine.Restore`'s documentation described where a foreign save resumes by
  naming an unexported function, which a reader of the public documentation
  cannot see. It now describes the behaviour.

### Fixed

- The sentinel errors' documentation claimed that *every* error returned by the
  package wraps one of them. Two kinds do not, and a host that believed the
  claim would mishandle both: a cancelled context is reported as the context's
  own error so `errors.Is` finds `context.Canceled`, and a nil context or a
  malformed option reports a mistake at the call site as a plain error. Both are
  now documented, here and in `docs/reference.md`.

## [0.1.2] — 2026-07-30

### Added

- `TestFrotzMatchesDfrotzGoldenSequence` pins the first 1,000 results of the
  Frotz-compatible generator seeded at 1234 to a SHA-256 digest taken from
  Homebrew's Frotz 2.55, not from this package. The differential tests are only
  meaningful while this engine draws the numbers dfrotz draws, and nothing
  previously guarded the sequence itself.
- `TestFrotzGoldenNormalizationIsLossless` checks the assumption the digest
  rests on: recovering each result as `printed - 1` inverts `result % 32767 + 1`
  for every result except 32767, which collides with 0.
- `docs/prng-history.md` records the derivation of the digest — source
  provenance, the probe story and its checksums, the capture commands, the
  normalization, and the independence argument — so the constant is not
  mistaken for one generated from the code it checks.

No exported behaviour changed.

## [0.1.1] — 2026-07-30

### Changed

- `github.com/maloquacious/quetzal` moved from a commit-pinned pseudo-version to
  the tagged `v0.2.2`. The upgrade is behaviourally empty: the intervening
  upstream changes were a changelog, a tutorial, README and agent guidance, its
  own version bump, and one doc comment recording that
  `IgnoreInterpreterHeader`'s ranges are the union across Versions.
- `go mod tidy` dropped two stale pseudo-version entries from `go.sum`.
- The `version` constant's documentation claimed this repository has no tags,
  which stopped being true at `v0.1.0`.

## [0.1.0] — 2026-07-30

First tagged release: a headless Z-machine Version 3 execution engine.

### Added

- **Story loading and validation.** `LoadStory` validates a Version 3 story
  file, checking every header address and table extent before use, and returns
  an immutable `Story` safe for concurrent use by any number of machines.
- **Text.** Version 3 Z-string and ZSCII decoding, including abbreviations.
- **Instruction decoding.** The full Version 3 instruction set, decoded
  separately from execution so that a decoded instruction retains the context a
  fault message needs.
- **Execution.** `Machine`, the execution loop, and the Version 3 opcodes, with
  each machine owning its own dynamic memory, evaluation stack, call chain and
  program counter.
- **Object model, dictionary and input.** The Version 3 object tree, properties
  and attributes; dictionary lookup and tokenization; and line input handled as
  a suspension boundary rather than a blocking read.
- **The request lifecycle.** `Start`, `Run` and `Restore`, with `Result`
  carrying the story's output, the upper window, the status line, the resumable
  state and the reason execution stopped. Creating a machine, restoring, running
  one command and discarding the machine is observably identical to keeping one
  machine alive for the whole game.
- **State.** Resumable state in the Quetzal format through
  `github.com/maloquacious/quetzal`, treated as untrusted input on the way back
  in.
- **Options.** `WithLogger`, `WithRandomSeed`, `WithInstructionLimit`,
  `WithTracer` and `WithFrotzRandomSeed`.
- **Error taxonomy.** `ErrInvalidStory`, `ErrInvalidState`, `ErrInvalidOpcode`,
  `ErrMemoryAccess`, `ErrInvalidText`, `ErrExecutionLimit` and
  `ErrExecutionFault`, with typed errors carrying the program counter, opcode
  and address.
- **Differential tests against Frotz** in three layers — transcript, status
  line, and the state of play compared against a save Frotz wrote — run from
  committed fixtures, with `ZMACHINE_REQUIRE_DFROTZ` turning a missing `dfrotz`
  into a failure rather than a silent skip.
- **`Version()`**, which reports this package's release and deliberately claims
  no standard revision.

[Unreleased]: https://github.com/maloquacious/zmachine/compare/v0.1.6...HEAD
[0.1.6]: https://github.com/maloquacious/zmachine/compare/v0.1.5...v0.1.6
[0.1.5]: https://github.com/maloquacious/zmachine/compare/v0.1.4...v0.1.5
[0.1.4]: https://github.com/maloquacious/zmachine/compare/v0.1.3...v0.1.4
[0.1.3]: https://github.com/maloquacious/zmachine/compare/v0.1.2...v0.1.3
[0.1.2]: https://github.com/maloquacious/zmachine/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/maloquacious/zmachine/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/maloquacious/zmachine/releases/tag/v0.1.0

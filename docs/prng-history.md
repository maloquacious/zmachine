# Deriving and Validating the dfrotz PRNG Golden Digest

## Provenance of this document

The derivation recorded here was performed in the sibling implementation at
`../z3`, which published it as its own `docs/prng-history.md`. This copy keeps
that work because the digest it produces is the constant this engine's
`TestFrotzMatchesDfrotzGoldenSequence` asserts, and a golden constant without a
recorded derivation is indistinguishable from a number somebody wrote down.

The narrative below is theirs. The sections describing the Go encoding have been
rewritten to describe this package's `internal/prng`, which reaches the same
sequence through a different API; the sections establishing the sequence itself
— source provenance, the probe story, the capture, the hashing, and the
independence argument — are unchanged, because that is the part being relied on
and paraphrasing it would only add a place for it to go wrong.

This engine's generator reproduces the captured sequence exactly, first value to
last.

## Abstract

The engine includes a Frotz-compatible random-number generator for differential
testing against `dfrotz -s 1234`. Its regression test fixes the first 1,000
unscaled generator values to the SHA-256 digest

```text
b7b37f6f04fb177b86ef1370097d9def416e893e979f28c52eb9eb7e7c67a88d
```

This document records how that digest was derived and why it is independent of
the Go implementation it tests. The process used three distinct artifacts:

1. the Frotz 2.55 source selected by Homebrew;
2. the installed Homebrew `dfrotz` executable as a behavioral oracle; and
3. a synthetic Version 3 story that asks the executable for 1,000 values.

The executable output, rather than output from `internal/prng`, supplied the
golden data. The source code was used separately to explain and cross-check the
observed sequence.

## Source provenance

The work was performed on July 30, 2026. Homebrew reported Frotz 2.55 with this
stable source archive:

```text
https://www.ifarchive.org/if-archive/infocom/interpreters/frotz/frotz-2.55.tar.gz
SHA-256: e3d6912ff94bca724759fe59aa222c1ee23aa4d35ffa15a0b40f72f97887d871
```

The metadata can be queried directly:

```sh
brew info --json=v2 frotz
```

The archive was downloaded, its checksum was verified, and it was extracted:

```sh
curl -L \
  -o /tmp/frotz-2.55.tar.gz \
  https://www.ifarchive.org/if-archive/infocom/interpreters/frotz/frotz-2.55.tar.gz
shasum -a 256 /tmp/frotz-2.55.tar.gz
tar -xzf /tmp/frotz-2.55.tar.gz -C /tmp
```

The relevant implementation is `src/common/random.c`. The dumb-interface
command-line seed handling is in `src/dumb/dinit.c`, and restart calls the
random seeding function from `src/common/fastmem.c`.

The locally installed executable identified itself as:

```text
FROTZ V2.55 - Dumb interface.
```

Its local SHA-256 was
`c749481109983825a889eb7e5a4db490b7bf08581d7aee6128de0e822c431f7d`.
That executable hash records the exact validation artifact on that machine; it
is not intended as a portable identity for every Homebrew installation.

## Behavior established from the source

Frotz maintains an LCG state, a predictable interval, and a counter. In normal
mode, the observable calculation is:

```text
state  = 0x015a4e35 * state + 1
raw    = (state >> 16) & 0x7fff
result = raw % range + 1
```

Frotz stores `state` in a C `long`. This engine explicitly retains the low 31
bits after each update. This produces the same result because all observable
state bits are bits 0 through 30, and multiplication modulo 2^31 preserves those
bits independently of higher bits or the host width of `long`.

Seeding has two forms:

- A command-line seed from `dfrotz -s N` is returned by `os_random_seed()` and
  enters normal LCG mode, even when `N` is below 1000.
- A story opcode `random -N` calls the internal seeding function directly.
  Values below 1000 select predictable mode, which cycles through raw values
  `0` to `N-1`. Values of 1000 or more select normal LCG mode.

The `random 0` opcode and story restart both ask the interface for a seed again.
With `-s 1234`, each therefore restores normal LCG state to 1234.

These observations determined what the Go implementation needed to emulate,
but they did not provide the golden test data. That came from executing Frotz.

## Constructing an independent story-level probe

A synthetic Version 3 story was created specifically for the experiment. It
contains no game logic. Its instruction stream repeats these operations 1,000
times:

1. execute `random 32767` and store the result in global variable 0;
2. execute `print_num` on global variable 0; and
3. print a newline.

It then executes `quit`. Testing through story opcodes is important: it checks
the installed executable's seed handling, generator update, range scaling,
store operation, and signed opcode interpretation together. Calling a copied
LCG formula in a separate script would not be an independent behavioral test.

The exact probe story can be reproduced with this Python program:

```python
from pathlib import Path

COUNT = 1000
START = 0x400

# VAR:random 32767 -> global 0; VAR:print_num global 0; new_line
BLOCK = bytes.fromhex("e7 3f 7f ff 10 e6 bf 10 bb")

# One quit byte and one byte of padding keep the V3 file length even.
image = bytearray(START + COUNT * len(BLOCK) + 2)

def word(offset: int, value: int) -> None:
    image[offset : offset + 2] = value.to_bytes(2, "big")

image[0] = 3                 # Version
word(2, 1)                   # Release
word(4, START)               # High memory
word(6, START)               # Initial PC
word(8, 0x280)               # Dictionary
word(10, 0x220)              # Object table
word(12, 0x40)               # Globals
word(14, 0x300)              # Static memory
image[18:24] = b"260730"    # Serial

# Empty dictionary: no separators, four-byte entries, zero entries.
image[0x281] = 4

for index in range(COUNT):
    offset = START + index * len(BLOCK)
    image[offset : offset + len(BLOCK)] = BLOCK

image[START + COUNT * len(BLOCK)] = 0xba  # 0OP:quit
word(26, len(image) // 2)                  # V3 length unit is two bytes
word(28, sum(image[64:]) & 0xffff)         # Story checksum

Path("/tmp/dfrotz-prng.z3").write_bytes(image)
```

The resulting story has these reproducibility properties:

```text
Length:   10026 bytes
SHA-256:  8db24d101f17ea0997f99fe8c2700a07872c524d36bb02c7d51f2841771e4554
Checksum: 0x155e
```

## Capturing the executable output

The probe was run against the installed executable:

```sh
/opt/homebrew/bin/dfrotz -q -m -s 1234 /tmp/dfrotz-prng.z3 \
  > /tmp/dfrotz-prng.out
```

The options have distinct purposes:

- `-q` suppresses the startup banner;
- `-m` disables interactive `***MORE***` pauses; and
- `-s 1234` supplies the deterministic interface seed being emulated.

Without `-m`, execution stops for paging after 22 values and cannot serve as a
non-interactive oracle. With `-m`, this version of `dfrotz` still inserts one
blank display line after each group of 22 printed values. The captured file
therefore contains 1,045 lines: 1,000 numeric lines and 45 blank lines.

The first numeric results were:

```text
1357
29134
21999
27019
30399
13800
26301
16521
```

The final five were:

```text
16376
29808
25227
159
5350
```

Running the command a second time produced byte-for-byte identical output.

## Normalizing and hashing the golden sequence

The package generator exposes Frotz's unscaled 15-bit values, while a
Z-machine `random` instruction stores a value from 1 through its positive
operand. For `random 32767`, each observed value is:

```text
printed = raw % 32767 + 1
```

No raw value of 32767 occurs in this 1,000-value sample, so subtracting one from
each numeric output exactly recovers every raw value in the sample. Blank lines
were discarded and the normalized decimal values were written one per line:

```sh
awk 'NF { print $1 - 1 }' /tmp/dfrotz-prng.out \
  > /tmp/dfrotz-seed-1234.golden

wc -l /tmp/dfrotz-seed-1234.golden
shasum -a 256 /tmp/dfrotz-seed-1234.golden
```

The result was:

```text
1000 /tmp/dfrotz-seed-1234.golden
b7b37f6f04fb177b86ef1370097d9def416e893e979f28c52eb9eb7e7c67a88d
```

As a third check, a small script evaluated the recurrence derived from
`random.c`, applied Frotz's `% 32767 + 1` scaling, and compared all 1,000 values
with the executable output. There were no differences.

## Encoding the result as a Go golden test

The temporary text file was not committed. Instead,
`TestFrotzMatchesDfrotzGoldenSequence` in `internal/prng/frotz_golden_test.go`
seeds a `Frotz` with 1234, draws 1,000 values, writes each as base-10 text
followed by `\n`, computes SHA-256 over those exact bytes, and compares it with
the executable-derived digest.

This package's `Source` interface exposes only `Draw(n)`, which applies the
opcode's `result % n + 1` scaling, rather than a raw generator value. The test
therefore draws over 32767 and subtracts one, reproducing both the range the
oracle actually ran and the normalization applied to its output.

Hashing the complete stream has two useful properties:

- every one of the 1,000 values contributes to the assertion; and
- the repository does not need a large fixture containing one integer per
  line.

The representation is intentionally explicit. Changing decimal formatting,
line endings, value count, seed, generator state transition, or output values
changes the digest. The test comment records the executable provenance and the
normalization step so the constant is not mistaken for a hash generated from
the implementation under test.

### Earning the normalization

Subtracting one inverts `raw % 32767 + 1` for every raw value except 32767
itself, which collides with a raw value of 0. The derivation above establishes
by inspection that no such value occurs in this sample, which is a claim about
these particular 1,000 values rather than an identity.

`TestFrotzGoldenNormalizationIsLossless` checks it rather than inheriting it.
Drawing the same seeded sequence over 32768 is lossless for a 15-bit result,
because `raw % 32768 + 1` is `raw + 1` for every value a 15-bit result can take,
so comparing the two recoveries across the sampled thousand says directly
whether the golden test's subtraction loses anything. It does not.

## Independence argument

The validation avoids a circular test in four ways:

1. **Different implementation:** the oracle was the installed Frotz C
   executable, not a Go generator.
2. **Public VM boundary:** the probe used real Version 3 `random`, store,
   output, and quit instructions rather than calling generator internals.
3. **Source/executable separation:** source inspection established intended
   semantics, while executable output supplied the golden sequence.
4. **Three-way agreement:** the executable output, the independently evaluated
   source recurrence, and the Go implementation agreed for all 1,000 values.

This does not prove every Frotz random-number behavior by itself. Separate tests
in `internal/prng/prng_test.go` cover modulo scaling, story-selected predictable
seeds, the 999/1000 mode boundary, a seed of 0 taking entropy, range bounds, and
state round trips. The golden digest has the narrower purpose of detecting any
future change to the normal LCG sequence selected by `dfrotz -s 1234`.

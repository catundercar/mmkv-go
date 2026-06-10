# mmkv-go

A **cgo-free** implementation of [Tencent MMKV](https://github.com/Tencent/MMKV)
for Go: a zero-copy read-only `Reader` plus a full read+write `MMKV` type. Both
speak the official on-disk format and flock protocol, so they interoperate with
the C++ library over the same files — without ever crossing the cgo boundary
(reads are an order of magnitude faster than the official Go binding and
allocate nothing on the view paths).

```go
import "github.com/catundercar/mmkv-go"

// read-only, zero-copy (lock-free reads):
r, err := mmkv.Open("/path/to/mmkv/dir", "myID")
// encrypted: mmkv.Open(dir, id, mmkv.WithEncryption([]byte(cryptKey)))
if err != nil { /* ... */ }
defer r.Close()
v, ok := r.GetBytes("key")   // []byte view into the reader's buffer, zero-copy
n, ok := r.GetInt32("count")

// read+write (MMKV semantics: append fast path, single-key override,
// periodic compaction; multi-process via flock with WithMultiProcess()):
m, err := mmkv.MMKVWithID(dir, "myID")
if err != nil { /* ... */ }
defer m.Close()
_ = m.SetString("name", "value")
s, ok := m.GetString("name")
```

Freshness is transparent (like MMKV C++): each read cheaply checks the writer's
change canary in the mmap'd `.crc` and reloads only when something changed.
Cross-process single-writer + multi-reader is gated in CI with the writer on
either side (cgo or pure Go).

## Scope

- **Supported:** read and write, plaintext or AES-CFB-128/256, key expiration,
  multi-process (flock interlock, `WithMultiProcess`), read-only mode,
  namespaces, backup/restore, `ImportFrom`, `[]string` values, compareBeforeSet,
  content-changed handler, corruption recovery (`WithRecoverOnError`). POSIX
  (Linux/macOS) only.
- **Encryption:** width follows key length, like MMKV (`WithCryptKey` /
  `WithEncryption`); the Reader also takes a custom `Decryptor`.
- **Key expiration:** decoded and filtered transparently (expired keys read as absent).
- **Out of scope:** Windows and Android-specific backends (ashmem); anything
  not listed above — the official cgo library remains the reference for those.

See [doc/DESIGN.md](doc/DESIGN.md) for the on-disk format spec and boundaries,
[doc/MMKV_FULL_DESIGN.md](doc/MMKV_FULL_DESIGN.md) for the read+write type's
design, and [doc/BENCHMARK.md](doc/BENCHMARK.md) for performance.

## Why it's correct (and stays correct)

The headline guarantee is **bidirectional equivalence with the official
library**: a value written by cgo reads back identically through this package
(`cgo.Get(k) == purego.Get(k)`), and a file written by this package reads back
identically through cgo — including encrypted and expiring stores. CI enforces
both directions per MMKV version × architecture (`harness/`), so a format
change in any MMKV release that breaks either side turns the build red.

## Compatibility

CI verifies the equivalence guarantee against the latest tag of each MMKV
release line, on both **amd64** and **arm64** (native runners):

| MMKV line | tested tag | note |
|---|---|---|
| v1.2.x | `v1.2.16` | earliest line tested |
| v1.3.x | `v1.3.16` | format v4 / key expiration introduced |
| v2.0.x | `v2.0.2` | |
| v2.1.x | `v2.1.1` | namespace |
| v2.2.x | `v2.2.4` | |
| v2.3.x | `v2.3.0` | AES-256 |
| v2.4.x | `v2.4.0` | latest |

On-disk **format versions 0–4** are supported for reading. The format has been
stable at v4 since v1.3.0, so files from current MMKV releases read correctly; a
future format bump surfaces as `ErrUnsupportedVersion` (never silent corruption)
and turns the CI differential red. The pure-Go **writer emits format v4**, so
the write-direction differential is gated from v1.3 on (pre-v1.3 MMKV cannot
read v4 files). Encryption (AES-CFB-128/256) and key expiration are
differential-tested in both directions on `v2.4.0`; their on-disk format is
version-stable.

**Requires** Go 1.23+ and a POSIX OS (Linux/macOS).

## Layout

```
.                      pure-Go library (package mmkv) + unit tests + testdata/
doc/                   DESIGN.md, BENCHMARK.md
harness/               cgo module: cgo≡purego gate, -race concurrency, 3-way Go bench
cpp/                   native C++ baseline (bench_cpp.cpp, build.sh)
tools/gen/             cgo module: regenerate testdata fixtures
scripts/               build_output.sh, run_cell.sh, aggregate.py
.github/workflows/     CI matrix (versions × {amd64, arm64})
```

The root module is **pure Go with no cgo dependency**, so `go get` never pulls
the C libraries, and `go test ./...` works out of the box (the cgo modules are
excluded as separate modules). Everything that needs cgo (`harness/`,
`tools/gen/`) is its own module with self-contained `replace` directives. For
cross-module local dev, after building `MMKV/output` run
`go work init . ./harness ./tools/gen` (go.work is gitignored).

## Testing & benchmarks

CI runs a matrix over MMKV's major version lines × {amd64, arm64} on native
runners. Each cell builds that MMKV version, runs the functional gate (hard
fail), then the three-way performance comparison (C++ / cgo / purego).

```sh
# one cell locally (needs git, cmake, g++, zlib dev, Go):
bash scripts/build_output.sh v2.4.0          # clone+build MMKV into ./MMKV/output
bash scripts/run_cell.sh   v2.4.0 arm64      # gate + perf -> results/
python3 scripts/aggregate.py results         # merge -> markdown report
```

Pure-Go unit tests need no cgo: `go test ./...`.

## License

MIT. MMKV itself is BSD-3-Clause; this is a clean-room implementation of its
on-disk format and locking protocol.

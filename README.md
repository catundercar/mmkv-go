# mmkv-go

A **cgo-free, read-only** decoder for [Tencent MMKV](https://github.com/Tencent/MMKV)
files. The read path never crosses the cgo boundary, so reads are an order of
magnitude faster than the official Go binding and allocate nothing — at the cost
of being read-only (writes stay on the official cgo library).

```go
import "github.com/catundercar/mmkv-go"

r, err := mmkv.Open("/path/to/mmkv/dir", "myID")
// encrypted: mmkv.Open(dir, id, mmkv.WithEncryption([]byte(cryptKey)))
if err != nil { /* fall back to the cgo library */ }
defer r.Close()

v, ok := r.GetBytes("key")   // []byte view into the reader's buffer, zero-copy
n, ok := r.GetInt32("count")
s, ok := r.GetString("name")
```

Freshness is transparent (like MMKV C++): each read cheaply checks the writer's
change canary in the mmap'd `.crc` and reloads under a shared `flock` only when
something changed. Safe for **single-writer (cgo, `MMKV_MULTI_PROCESS`) +
multi-reader (pure Go)**.

## Scope

- **Supported:** plaintext or AES-encrypted, optional key expiration, read-only, POSIX (Linux/macOS).
- **Encryption:** AES-CFB-128/256 via `WithEncryption(key)` (width follows key length, like MMKV); or a custom `Decryptor`.
- **Key expiration:** decoded and filtered transparently (expired keys read as absent).
- **Out of scope:** writes and multi-writer. Use the official cgo library for
  those (including backup *restore*; `BackupOne` here is pure Go).

See [doc/DESIGN.md](doc/DESIGN.md) for the on-disk format spec and boundaries,
and [doc/BENCHMARK.md](doc/BENCHMARK.md) for performance.

## Why it's correct (and stays correct)

The headline guarantee is **`cgo.Get(k) == purego.Get(k)`**: a value written by
the official library reads back identically through this package. CI enforces
this per MMKV version × architecture (`harness/equiv_test.go`), so a format
change in any MMKV release that breaks the pure-Go reader turns the build red.

## Compatibility

CI verifies the `cgo.Get(k) == purego.Get(k)` guarantee for files written by the
official library across the latest tag of each MMKV release line, on both **amd64**
and **arm64** (native runners):

| MMKV line | tested tag | note |
|---|---|---|
| v1.2.x | `v1.2.16` | earliest line tested |
| v1.3.x | `v1.3.16` | format v4 / key expiration introduced |
| v2.0.x | `v2.0.2` | |
| v2.1.x | `v2.1.1` | namespace |
| v2.2.x | `v2.2.4` | |
| v2.3.x | `v2.3.0` | AES-256 |
| v2.4.x | `v2.4.0` | latest |

On-disk **format versions 0–4** are supported. The format has been stable at v4
since v1.3.0, so files from current MMKV releases read correctly; a future format
bump surfaces as `ErrUnsupportedVersion` (never silent corruption) and turns the CI
differential red. Encryption (AES-CFB-128/256) and key expiration are
differential-tested on `v2.4.0`; their on-disk format is version-stable.

**Requires** Go 1.21+ and a POSIX OS (Linux/macOS).

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

MIT (default — adjust to your needs). MMKV itself is BSD-3-Clause; this is a
clean-room reader of its on-disk format.

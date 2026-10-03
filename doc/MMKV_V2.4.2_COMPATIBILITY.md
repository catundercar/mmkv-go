# MMKV v2.4.2 compatibility validation

Local validation on 2026-09-30 found no compatibility failure in the existing
functional gates. macOS and Linux **native arm64** passed; Linux **amd64 under
Apple Silicon emulation** also executed and passed. On 2026-10-03, the full
[native GitHub Actions matrix](https://github.com/catundercar/mmkv-go/actions/runs/37100633728)
passed all 14 cells and the aggregate report, including v2.4.2 on native amd64
and arm64. [PR #4](https://github.com/catundercar/mmkv-go/pull/4) remains a draft;
[issue #2](https://github.com/catundercar/mmkv-go/issues/2) has not been updated
or closed.

## Source and environment

- mmkv-go baseline: `def2d80d19ecd6187c1fa3fe9970106cb018ffd6` (main, merged
  [PR #3](https://github.com/catundercar/mmkv-go/pull/3)). The baseline selected
  v2.4.1 for its v2.4 matrix entry; this change replaces that line's selected tag
  with v2.4.2, keeping the seven-version × two-architecture matrix.
- Official MMKV: tag `v2.4.2`, commit
  `ad7657ef9d120dbcdd7432d75aa6c59391149b22`; Core and the Go binding were built
  from this checkout, rather than using bundled prebuilt libraries.
- Host: Mac Studio, macOS 27.0, darwin/arm64, Go 1.26.4, Apple Clang/CMake.
- Containers: OrbStack Linux, `golang:1.25.12-bookworm`, Go 1.25.12, GCC/G++ 12,
  CMake and zlib development package. Separate container scratch copies avoided
  sharing build outputs across operating systems or architectures.
- Native GitHub CI: `ubuntu-latest` (Ubuntu 24.04, amd64) and
  `ubuntu-24.04-arm` (arm64), Go 1.25.14, GCC/G++ 12, CMake and zlib.
- The original checkout had uncommitted `harness/go.mod` and `harness/go.sum`
  changes. Validation used an independent clean copy of main; those original
  files were not modified. No checkout `AGENTS.md` or `.agents/skills` was found.

## Executed results

| Execution environment | Result | Scope |
|---|---|---|
| macOS native arm64 | PASS | root unit tests; full `mmkvconfig` harness; root and full harness with `-race` |
| Linux native arm64 | PASS | `run_cell.sh`: unit, equiv, crypt+expire, race, multiproc; C++/Go benchmark output |
| Linux emulated amd64 | PASS (emulated) | same five gates and benchmark output; actual amd64 binaries executed |
| GitHub native amd64 | PASS | v2.4.2: all five gates and benchmark output |
| GitHub native arm64 | PASS | v2.4.2: all five gates and benchmark output |
| GitHub full matrix / aggregate | PASS | all 14 native cells and aggregate report |

No executed test failed. The verbose macOS runs recorded 63 root tests and 14
harness tests passing, with no skips, both with `-race`; the 41 plaintext
equivalence subcases also passed. Each Linux cell exited 0 and recorded all five
gate names. Benchmark runs used `100ms` Go samples: they verify the performance
path executes, not a new performance claim. Emulated amd64 timings are not
native performance evidence. Existing README performance data retains its
v2.4.0 attribution.

The native CI run tested commit `02d09f6e2c7856e4aeb0da88d859a55d29d446fc`.
All 15 jobs completed with `success`; all 14 gate artifacts were downloaded and
checked. The v2.4.2 artifacts contain exactly `unit`, `equiv`, `crypt+expire`,
`race`, and `multiproc`, plus successful C++ and Go benchmark outputs. The
[amd64 job](https://github.com/catundercar/mmkv-go/actions/runs/37100633728/job/111139304200)
logs Go 1.25.14 `linux/amd64`; the
[arm64 job](https://github.com/catundercar/mmkv-go/actions/runs/37100633728/job/111139304170)
logs Go 1.25.14 `linux/arm64`, matching their native runner images. CI's race
gate is `TestLiveReadConcurrent`; the full root/harness race results above are
from the macOS run. The follow-up commit only records CI outcomes in
documentation; it changes no workflow, library, harness or dependency files.

The root module's tests alone do not exercise the independent cgo harness.
`run_cell.sh` already enables `mmkvconfig` for all `v2.4.*` tags, including
v2.4.2; without that tag, encryption/expiration differential tests are excluded.

## Format and differential coverage

New v2.4.2 C++-written stores were read successfully by pure-Go `Reader` and
`MMKV`, with no `ErrUnsupportedVersion`. The macOS run's 14 retained `.crc`
samples all contain metadata version **4**; the expiration stores contain
`flags=1`. The relevant on-disk metadata declaration is also unchanged between
v2.4.1 and v2.4.2 ([upstream source](https://github.com/Tencent/MMKV/blob/v2.4.2/Core/MMKVMetaInfo.hpp)).
Together, the executed reads and source comparison support compatibility for
the tested stores; they do not prove every upstream behavior is equivalent.

| Existing differential | Cases executed |
|---|---|
| C++ → Go `Reader`, plaintext | typed boundaries, Unicode, byte blobs through 1 MB, overwrite/delete |
| C++ → Go `MMKV`, plaintext | live read+write type reads C++ values |
| Go → C++, plaintext | batch Writer; MMKV append, override and remove-all |
| C++ → Go `Reader`, encrypted | AES-CFB-128 and AES-CFB-256, including 4 KB multi-block values |
| Go `MMKV` → C++, encrypted | AES-CFB-128 |
| C++ → Go `MMKV`, encrypted | AES-CFB-256 |
| C++ → Go `Reader`, expiration | never expires; actual 2-second TTL absent after 3 seconds |
| Go `MMKV` → C++, expiration | never expires, timestamp suffix decoded correctly |
| race / multiple processes | live cgo writer + Go Reader; separate cgo writer and three Go Reader processes; pure-Go MMKV writer/readers |

The current harness does **not** differentially test Go → C++ AES-256, C++ → Go
MMKV AES-128, actual TTL in the Go → C++ direction, or actual TTL through the
C++ → Go MMKV direction. It also does not cover vector<string> through cgo,
same-key AES mode switching, backup/restore edge cases, or the upstream ARM CRC
alignment sweep. Go `-race` is not C++ ThreadSanitizer. These are coverage limits,
not observed failures.

## Relevant upstream differences

- The official Go binding fixes `MMKV_Expire_Year` from 946080000 seconds (30
  years) to 31536000 seconds (365 days). This library only exports `ExpireNever`
  and takes caller-provided seconds, so no corresponding constant needs a fix.
  Existing absolute expiration timestamps are not automatically recalculated.
  [Upstream fix](https://github.com/Tencent/MMKV/commit/8e9883ef6f70f3dac9acd54233c337d8c4e4d377).
- IV generation now uses platform cryptographic randomness; key/IV cleanup and
  same-key AES mode changes were fixed. AES-CFB decoding remains compatible in
  the tested directions. Random IVs mean raw ciphertext identity is not the
  differential criterion. [Upstream encryption fix](https://github.com/Tencent/MMKV/commit/7b20c03c34ee284a0ad40e64d3304551e4ce6421).
- Expiration-field and ARM CRC reads gained alignment fixes; backup/restore and
  encoding bounds were hardened. The local gates pass but do not exercise every
  new upstream regression case. [Alignment fix](https://github.com/Tencent/MMKV/commit/baf36b86954dfe8fd87058014f41232fdca6a4d7),
  [release notes](https://github.com/Tencent/MMKV/releases/tag/v2.4.2).

## Commands and evidence

macOS commands, from the independent checkout (all exited 0):

```sh
export GOWORK=off GOFLAGS=-mod=mod GOCACHE=/tmp/mmkv-v242-go-cache
bash scripts/build_output.sh v2.4.2 MMKV
go test -count=1 ./...
(cd harness && go test -tags mmkvconfig -count=1 -v ./...)
go test -race -count=1 -v ./...
(cd harness && go test -race -tags mmkvconfig -count=1 -v ./...)
```

Each Linux scratch copy installed `cmake gcc-12 g++-12 zlib1g-dev`, then ran:

```sh
CC=gcc-12 CXX=g++-12 bash scripts/run_cell.sh v2.4.2 arm64 100ms
# Separate --platform linux/amd64 container, on the same Apple Silicon host:
CC=gcc-12 CXX=g++-12 bash scripts/run_cell.sh v2.4.2 amd64 100ms
python3 scripts/aggregate.py results
```

`ARCH` in `run_cell.sh` labels files only. Container logs explicitly record
`uname`, `go version`, `GOARCH`, `GOHOSTARCH`, and `CGO_ENABLED` to establish what
executed. No cross-compilation result is counted as a passed runtime test.

Local evidence is gitignored under `results/`: `local-darwin-arm64/` contains
build/unit/harness/race logs, `metadata.json` and generated store snapshots;
`local-linux-arm64/` and `local-linux-amd64-emulated/` each contain `run.log`,
`exit-code.txt`, five-gate results, benchmark outputs and `summary.md`.
`run-linux-cell.sh` and `run-linux-amd64-cell.sh` preserve the exact container
setup commands. Go's generated harness dependency-file changes were recorded
in `generated-harness-module.diff` and removed from this patch.

Native CI evidence is under `results/ci-37100633728/`: `run.json`, `jobs.json`,
the two v2.4.2 job logs, downloaded per-cell and `all-results` artifacts,
`verification.txt`, and the locally regenerated `summary.md`. CI used
`CC=gcc-12 CXX=g++-12 bash scripts/run_cell.sh <tag> <arch> 1s` for each cell.

## Review status

The verification branch was pushed and draft PR #4 created after explicit user
authorization on 2026-10-03. Its pull-request event triggered the successful
native matrix above. The README and this record now reflect that result.
The issue checklist remains unchanged. Merge, release, and a post-merge main
run remain **not performed**; merging and publishing need separate user
authorization. The test-coverage limits listed above still apply.

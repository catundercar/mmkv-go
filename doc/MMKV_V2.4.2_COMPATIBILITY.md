# MMKV v2.4.2 compatibility validation

Local validation on 2026-09-30 found no compatibility failure in the existing
functional gates. macOS and Linux **native arm64** passed; Linux **amd64 under
Apple Silicon emulation** also executed and passed. Native amd64 and the full
GitHub Actions matrix remain **not run** for this change. This record does not
mark [issue #2](https://github.com/catundercar/mmkv-go/issues/2) complete.

## Source and environment

- mmkv-go baseline: `def2d80d19ecd6187c1fa3fe9970106cb018ffd6` (main, merged
  [PR #3](https://github.com/catundercar/mmkv-go/pull/3)). The previous completed
  native CI matrix tested v2.4.1; this change replaces that line's selected tag
  with v2.4.2, keeping the seven-version × two-architecture matrix.
- Official MMKV: tag `v2.4.2`, commit
  `ad7657ef9d120dbcdd7432d75aa6c59391149b22`; Core and the Go binding were built
  from this checkout, rather than using bundled prebuilt libraries.
- Host: Mac Studio, macOS 27.0, darwin/arm64, Go 1.26.4, Apple Clang/CMake.
- Containers: OrbStack Linux, `golang:1.25.12-bookworm`, Go 1.25.12, GCC/G++ 12,
  CMake and zlib development package. Separate container scratch copies avoided
  sharing build outputs across operating systems or architectures.
- The original checkout had uncommitted `harness/go.mod` and `harness/go.sum`
  changes. Validation used an independent clean copy of main; those original
  files were not modified. No checkout `AGENTS.md` or `.agents/skills` was found.

## Executed results

| Execution environment | Result | Scope |
|---|---|---|
| macOS native arm64 | PASS | root unit tests; full `mmkvconfig` harness; root and full harness with `-race` |
| Linux native arm64 | PASS | `run_cell.sh`: unit, equiv, crypt+expire, race, multiproc; C++/Go benchmark output |
| Linux emulated amd64 | PASS (emulated) | same five gates and benchmark output; actual amd64 binaries executed |
| GitHub native amd64 | NOT RUN | requires the updated workflow on a native runner |
| GitHub native arm64 / full matrix / aggregate | NOT RUN | local Linux arm64 evidence does not constitute a GitHub CI run |

No executed test failed. The verbose macOS runs recorded 63 root tests and 14
harness tests passing, with no skips, both with `-race`; the 41 plaintext
equivalence subcases also passed. Each Linux cell exited 0 and recorded all five
gate names. Benchmark runs used `100ms` Go samples: they verify the performance
path executes, not a new performance claim. Emulated amd64 timings are not
native performance evidence. Existing README performance data retains its
v2.4.0 attribution.

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

## Remaining native CI steps

After a separately authorized push, run `mmkv-tri-test` on the branch through a
pull request or `workflow_dispatch` selecting that branch. Require all **14**
cells and the aggregate report to succeed, including v2.4.2 on `ubuntu-latest`
(native amd64) and `ubuntu-24.04-arm` (native arm64). Inspect both v2.4.2 gate
artifacts for all five names, then update the README status and issue checklist
with the actual run URL. A post-merge main run would provide the final baseline
evidence if merging is later authorized. No push, PR, workflow dispatch, merge,
release or issue update was performed during this local validation.

# Running the full test flow locally with Docker

> **English** · [中文](LOCAL_DOCKER.zh-CN.md)

Reproduce the entire CI flow (functional gate + three-way performance) locally in
a Linux container, without installing cmake / g++ / a cgo toolchain on the host.
The container is a stand-in for the CI runner.

## Prerequisites

- **Docker**. On Apple Silicon, **OrbStack** is strongly recommended: `--platform linux/arm64` is **native** arm64
  (no QEMU), so builds are fast and the performance numbers are real.
- Image: `golang:1.23-bookworm` (ships `git` / `g++` / `go`).
- Also needed in the container: `cmake` + `zlib1g-dev` (cmake builds MMKV Core; zlib provides **both headers and the
  library** for compiling Core and for `-lz` linking).

> Note: `scripts/build_output.sh` **recompiles Core** per tag, so `zlib1g-dev` (the headers) is required. The
> `ln -sf libz.so.1 .../libz.so` trick only works when *using a prebuilt* libcore.a — not when rebuilding Core.

## 1. A single cell (start here)

Run one full "version × arch" cell: build that version's `output/` → functional gate → three-way performance.

```sh
cd ~/project/repos/mmkv-go
docker run --rm --platform linux/arm64 -v "$PWD":/work -w /work golang:1.23-bookworm bash -c '
  set -e
  apt-get update -qq && apt-get install -y -qq cmake zlib1g-dev
  bash scripts/run_cell.sh v2.4.0 arm64 1s
'
```

- `run_cell.sh <tag> <arch> [benchtime]`; `<arch>` only labels the result files — the real arch is set by `--platform`.
- Output: `./results/v2.4.0-arm64.{cpp,go}.txt` (on the host, gitignored).
- Side effect: clones MMKV at the tag into `./MMKV` (gitignored, regenerable); same as CI.

Any gate failure (`cgo≡purego` mismatch / unit tests / `-race`) makes the script exit non-zero (CI fails on it).
Performance only reports; it never fails the build.

## 2. The full matrix (multiple versions, one container loop)

Run the whole version set in one container (native arm64). The same `./MMKV` clone is reused, checked out per tag:

```sh
cd ~/project/repos/mmkv-go
docker run --rm --platform linux/arm64 -v "$PWD":/work -w /work golang:1.23-bookworm bash -c '
  set -e
  apt-get update -qq && apt-get install -y -qq cmake zlib1g-dev
  for v in v1.2.16 v1.3.16 v2.0.2 v2.1.1 v2.2.4 v2.3.0 v2.4.0; do
    echo "==================== $v ===================="
    bash scripts/run_cell.sh "$v" arm64 1s
  done
'
```

**amd64**: replace both `arm64` with `amd64` and `--platform linux/amd64`. On Apple Silicon, amd64 runs under
**emulation** — the functional gate is still valid, but **the performance numbers are distorted, treat as
indicative only**; real amd64 performance comes from the native CI runner.

## 3. Functional gate only (fastest)

Skip performance, just confirm `cgo≡purego` for a version:

```sh
docker run --rm --platform linux/arm64 -v "$PWD":/work -w /work golang:1.23-bookworm bash -c '
  set -e
  apt-get update -qq && apt-get install -y -qq cmake zlib1g-dev
  bash scripts/build_output.sh v2.4.0 /work/MMKV
  cd harness && GOWORK=off GOFLAGS=-mod=mod go test -run TestCgoEqualsPurego -v ./...
'
```

The pure-Go unit tests (no cgo, sub-second) run directly on the host: `go test ./...`.

## 4. Results + aggregation

Results land in `./results/` on the host. Aggregate into markdown (cgo vs purego, C++ baseline, 4KB three-way +
relative ratios) with the host's python3:

```sh
python3 scripts/aggregate.py results
```

(The `golang` image has no python3; to aggregate inside the container, `apt-get install -y python3` first.)

## 5. Cleanup

```sh
rm -rf MMKV results cpp/bench_cpp   # all gitignored, regenerable any time
```

## 6. Troubleshooting

- **`cmake: command not found`**: not in the image, must `apt-get install -y cmake`.
- **Link error `cannot find -lz` / Core can't find `zlib.h`**: `zlib1g-dev` is missing.
- **apt occasionally hangs** (flaky network): retry the `docker run`; apt is usually fine, no special handling needed.
- **First `go test` (cgo) is slow**: the first run compiles the cgo package + links `libcore.a/libmmkv.a` — normal.
- **Don't want MMKV cloned into the repo**: run the flow in a container-local scratch copy (zero writes to the host):
  ```sh
  docker run --rm --platform linux/arm64 -v "$PWD":/work golang:1.23-bookworm bash -c '
    apt-get update -qq && apt-get install -y -qq cmake zlib1g-dev
    cp -r /work /scratch && cd /scratch && bash scripts/run_cell.sh v2.4.0 arm64 1s
    cp -r /scratch/results /work/results   # copy results back to the host
  '
  ```

## Relationship to CI

Local Docker ≈ the CI runner. CI (`.github/workflows/ci.yml`) runs on **native** `ubuntu-latest` (amd64) and
`ubuntu-24.04-arm` (arm64): it `apt-get install`s `cmake g++-12 zlib1g-dev` and then runs `bash scripts/run_cell.sh`
directly — the runner is single-use, so no scratch is needed; results are uploaded as artifacts and the aggregation
is written to the job summary. Local and CI run the **same scripts**.

> CI pins **g++-12** rather than the distro default g++-13/14: GCC 13 dropped transitive `<cstdint>` includes, which
> breaks older MMKV Core (e.g. v1.2.16). g++-12 builds every target version. Locally, `golang:1.23-bookworm` already
> ships g++-12, so the commands above need no change.

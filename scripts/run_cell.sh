#!/usr/bin/env bash
# Run one CI matrix cell for the CURRENT architecture:
#   1. build MMKV@<tag> output (libcore.a + libmmkv.a + binding)
#   2. functional gate (HARD): pure-Go unit/reader tests + cgo≡purego equivalence
#      + concurrent live-read safety (-race)
#   3. performance (report): C++ baseline + cgo + purego, into results/
#
# Run from anywhere (resolves repo root from its own location). Assumes deps
# installed: git, cmake, a C++ toolchain, zlib dev, Go.
#
# Usage: run_cell.sh <tag> <arch> [benchtime]
set -euo pipefail

TAG="${1:?usage: run_cell.sh <tag> <arch> [benchtime]}"
ARCH="${2:?arch (amd64|arm64), used only to label results}"
BENCHTIME="${3:-1s}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
RESULTS="$ROOT/results"
mkdir -p "$RESULTS"
# Deterministic per-module builds: ignore go.work (dev-only), use each module's
# self-contained replace directives.
export GOWORK=off GOFLAGS=-mod=mod
CELL="$TAG-$ARCH"

group() { echo "::group::$*"; }
endgroup() { echo "::endgroup::"; }

group "build output ($TAG, $ARCH)"
bash scripts/build_output.sh "$TAG" "$ROOT/MMKV"
endgroup

# ---------- A. functional gate (hard fail) ----------
group "gate: pure-Go unit/reader tests"
go test ./...
endgroup

group "gate: functional equivalence (cgo ≡ purego, version $TAG)"
# v2.4.x has the unified MMKVWithIDAndConfig API → also run the encrypted /
# expiration differential tests (build-tagged mmkvconfig). Older bindings lack
# that API; their plaintext equivalence still runs (the on-disk crypt/expire
# format is version-stable, and CFB is checked by the NIST vector unit test).
EXTRA_TAGS=""
case "$TAG" in
v2.4.*) EXTRA_TAGS="-tags mmkvconfig" ;;
esac
( cd harness && go test $EXTRA_TAGS -run 'TestCgoEqualsPurego|TestEncrypted|TestExpire' -count=1 ./... )
endgroup

group "gate: concurrent live-read (-race)"
( cd harness && go test -race -run TestLiveReadConcurrent -count=1 ./... )
endgroup

# ---------- B. performance (report only) ----------
group "perf: C++ baseline"
bash cpp/build.sh
./cpp/bench_cpp | tee "$RESULTS/$CELL.cpp.txt"
endgroup

group "perf: cgo + purego"
( cd harness && go test -run '^$' -bench . -benchmem -benchtime="$BENCHTIME" ) \
  | tee "$RESULTS/$CELL.go.txt"
endgroup

echo "cell $CELL: gate PASS, perf written to results/$CELL.{cpp,go}.txt"

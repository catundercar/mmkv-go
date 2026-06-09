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
GATE="$RESULTS/$CELL.gate.txt"
: >"$GATE" # records each functional gate that passed (set -e aborts the cell on failure)

group() { echo "::group::$*"; }
endgroup() { echo "::endgroup::"; }
gatepass() { printf '%s\n' "$1" >>"$GATE"; }

group "build output ($TAG, $ARCH)"
bash scripts/build_output.sh "$TAG" "$ROOT/MMKV"
endgroup

# ---------- A. functional gate (hard fail) ----------
group "gate: pure-Go unit/reader tests"
go test ./...
gatepass unit
endgroup

group "gate: functional equivalence (cgo ≡ purego, version $TAG)"
# Base (all versions): C++ writes → both the read-only Reader and the read+write
# MMKV read back identically; plus a C++ Core (libcore.a) vector<string> read
# (the Go cgo binding doesn't expose vectors).
RUN='TestCgoEqualsPurego|TestCgoWriteMMKVReads|TestVectorCppToGo'
# Go writes → C++ reads: the writer emits on-disk format version 4 (Flag), which
# MMKV reads from v1.3.0 on, so gate these off the pre-Flag v1.2.x line.
case "$TAG" in
v1.2.*) ;;
*) RUN="$RUN|TestPuregoWriteCgoReads|TestMMKVWriteCgoReads|TestVectorGoToCpp" ;;
esac
# v2.4.x has the unified MMKVWithIDAndConfig API → also run the encrypted /
# expiration differentials in BOTH directions (build-tagged mmkvconfig). Older
# bindings lack that API; their plaintext equivalence still runs (the on-disk
# crypt/expire format is version-stable, and CFB is checked by the NIST vector
# unit test).
EXTRA_TAGS=""
case "$TAG" in
v2.4.*)
  EXTRA_TAGS="-tags mmkvconfig"
  RUN="$RUN|TestEncrypted|TestExpire|TestMMKVEncWriteCgoReads|TestCgoEncWriteMMKVReads|TestMMKVExpireWriteCgoReads"
  ;;
esac
( cd harness && go test $EXTRA_TAGS -run "$RUN" -count=1 ./... )
gatepass equiv
if [ -n "$EXTRA_TAGS" ]; then gatepass crypt+expire; fi
endgroup

group "gate: concurrent live-read (-race, same process)"
( cd harness && go test -race -run TestLiveReadConcurrent -count=1 ./... )
gatepass race
endgroup

group "gate: multi-process (separate processes)"
# cgo writer + pure-Go Reader, and a fully pure-Go MMKV writer + readers.
( cd harness && go test -run '^TestMultiProcess$' -count=1 ./... )
go test -run '^TestMMKVMultiProcess$' -count=1 .
gatepass multiproc
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

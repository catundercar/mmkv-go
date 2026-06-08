#!/usr/bin/env bash
# Build the native C++ baseline by linking the SAME libcore.a that the Go cgo
# package uses (MMKV/output/tencent.com/mmkv/lib/libcore.a, built per version by
# scripts/build_output.sh). bench_cpp.cpp #include "MMKV.h", so it also needs the
# matching Core headers (MMKV/Core).
#
# The CMake build defines FORCE_POSIX only on Apple, so on Linux we must NOT
# define it — keeps the MMKVKey_t / MMKVPath_t ABI identical to libcore.a.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CORE_INC="$HERE/../MMKV/Core"
LIBCORE="$HERE/../MMKV/output/tencent.com/mmkv/lib/libcore.a"

if [[ ! -f "$LIBCORE" ]]; then
  echo "error: $LIBCORE not found (run scripts/build_output.sh <tag> first)" >&2
  exit 1
fi

uname_s="$(uname -s)"
DEFS=()
if [[ "$uname_s" == "Darwin" ]]; then
  CXX="${CXX:-clang++}"
  DEFS+=(-DFORCE_POSIX) # matches an Apple-built libcore.a (not the shipped one)
else
  CXX="${CXX:-g++}" # Linux: libstdc++, no FORCE_POSIX
fi

set -x
"$CXX" -std=c++17 -O2 "${DEFS[@]}" -I "$CORE_INC" \
  "$HERE/bench_cpp.cpp" "$LIBCORE" \
  -lz -lpthread \
  -o "$HERE/bench_cpp"
set +x
echo "built: $HERE/bench_cpp"

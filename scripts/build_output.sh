#!/usr/bin/env bash
# Build the Go-consumable MMKV package for a given MMKV release tag, assembling
# <src>/output/tencent.com/mmkv with freshly-built lib/libcore.a + lib/libmmkv.a.
#
# We DON'T use `make install`: the per-version install() rules in
# POSIX/golang/CMakeLists.txt differ and some (e.g. v1.2.x) use source-relative
# lib paths that break. Instead we `make` the targets and copy by the stable
# CMake build-dir layout (build/libmmkv.a, build/Core/libcore.a). cgo only needs
# these libs + golang-bridge.h; golang-bridge.cpp compiles to nothing under
# -DCGO, so no Core headers are needed in output/.
#
# Usage: build_output.sh <tag> [src_dir]
#   <tag>     MMKV release tag, e.g. v2.4.0
#   [src_dir] MMKV checkout dir (default: ./MMKV). Cloned shallow if absent.
#
# Requires: git, cmake (>=3.13), a C++ toolchain, zlib dev (headers+lib).
set -euo pipefail

TAG="${1:?usage: build_output.sh <tag> [src_dir]}"
SRC="${2:-MMKV}"
REPO="https://github.com/Tencent/MMKV.git"

if [ -d "$SRC/.git" ]; then
  echo "==> $SRC: checkout $TAG"
  git -C "$SRC" checkout -q "$TAG" 2>/dev/null || {
    git -C "$SRC" fetch --depth 1 origin "refs/tags/$TAG:refs/tags/$TAG"
    git -C "$SRC" checkout -q "$TAG"
  }
else
  echo "==> cloning MMKV@$TAG (shallow) into $SRC"
  git clone --depth 1 --branch "$TAG" "$REPO" "$SRC"
fi

SRC_ABS="$(cd "$SRC" && pwd)"
GOLANG="$SRC_ABS/POSIX/golang"
BUILD="$GOLANG/build"
PKG="$SRC_ABS/output/tencent.com/mmkv"

echo "==> cmake + make (libmmkv.a + Core/libcore.a)"
rm -rf "$BUILD" "$SRC_ABS/output"
cmake -S "$GOLANG" -B "$BUILD" -DCMAKE_BUILD_TYPE=Release >/dev/null
make -C "$BUILD" -j"$(nproc 2>/dev/null || echo 4)"

echo "==> assemble $PKG"
mkdir -p "$PKG/lib"
cp "$GOLANG"/*.go "$GOLANG"/golang-bridge.h "$GOLANG"/golang-bridge.cpp "$GOLANG"/go.mod "$PKG"/
rm -f "$PKG"/*_test.go
cp "$BUILD/libmmkv.a" "$PKG/lib/"
cp "$BUILD/Core/libcore.a" "$PKG/lib/"
[ -f "$BUILD/zlib/libz.a" ] && cp "$BUILD/zlib/libz.a" "$PKG/lib/" || true

echo "OK: $PKG"
ls "$PKG"/lib

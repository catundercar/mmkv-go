// vec_cpp — a tiny MMKV Core helper for the vector<string> differential.
//
// The Go cgo binding does not expose vector<string>, so we link the SAME
// libcore.a directly to write/read a vector and cross-check it against the
// pure-Go codec (the only way to get a real C++ interop check for this type).
//
//   vec_cpp <rootdir> <id> write [items...]   set {items...} at key "vec", sync
//   vec_cpp <rootdir> <id> read               print getVector("vec"), one per line
//
// Built the same way as bench_cpp (see cpp/build.sh / harness test): -I Core,
// link libcore.a, -DFORCE_POSIX on Apple.

#include "MMKV.h"

#include <cstdio>
#include <string>
#include <vector>

using namespace mmkv;
using std::string;

int main(int argc, char **argv) {
    if (argc < 4) {
        fprintf(stderr, "usage: vec_cpp <root> <id> write|read [items...]\n");
        return 2;
    }
    string root = argv[1], id = argv[2], mode = argv[3];
    MMKV::initializeMMKV(root, MMKVLogNone);
    MMKV *kv = MMKV::mmkvWithID(id);

    if (mode == "write") {
        std::vector<string> v;
        for (int i = 4; i < argc; i++) {
            v.emplace_back(argv[i]);
        }
        kv->set(v, "vec");
        kv->sync(MMKV_SYNC);
        return 0;
    }
    if (mode == "read") {
        std::vector<string> v;
        kv->getVector("vec", v);
        for (const auto &s : v) {
            printf("%s\n", s.c_str());
        }
        return 0;
    }
    fprintf(stderr, "unknown mode %s\n", mode.c_str());
    return 2;
}

// Native C++ MMKV benchmark — the baseline (no FFI) for the CGO comparison.
//
// It links the SAME prebuilt libcore.a that the Go cgo package uses, so the
// MMKV Core machine code is byte-for-byte identical across all three subjects
// (native C++, cgo-copy, cgo-shared). Any gap is therefore attributable to the
// FFI boundary, not to a different Core build.
//
// Two read styles mirror the two Go get variants so each cgo number has a fair
// native counterpart:
//   * get_bytes_shared  -> kv->getBytes(key) returning an MMBuffer, used in
//                          place. Analog of Go GetBytesBuffer()+ByteSliceView().
//   * get_bytes_copy    -> kv->getString(key, std::string&), i.e. decode into a
//                          freshly owned std::string. Analog of Go GetBytes()
//                          (which memcpy's the C memory into the Go heap).
//
// Output: a human table plus machine-readable "RESULT<TAB>..." lines that
// run_all.sh / summary.py merge with the Go results.

#include "MMKV.h"

#include <chrono>
#include <cstdint>
#include <cstdio>
#include <string>
#include <string_view>
#include <vector>

using namespace mmkv;
using std::string;
using std::string_view;

// Volatile sink so the optimizer can't delete the work we are timing.
static volatile uint64_t g_sink = 0;

// Run fn() enough times to span at least minSeconds, return nanoseconds/op.
template <class F>
static double benchNs(F &&fn, double minSeconds = 0.3) {
    using clock = std::chrono::steady_clock;

    for (int i = 0; i < 50; i++) { // warmup
        fn();
    }

    uint64_t iters = 1000;
    for (;;) {
        auto t0 = clock::now();
        for (uint64_t i = 0; i < iters; i++) {
            fn();
        }
        auto t1 = clock::now();
        double secs = std::chrono::duration<double>(t1 - t0).count();
        if (secs >= minSeconds) {
            double ns = std::chrono::duration<double, std::nano>(t1 - t0).count();
            return ns / static_cast<double>(iters);
        }
        if (secs <= 0.0) {
            iters *= 100;
            continue;
        }
        double factor = (minSeconds * 1.5) / secs;
        uint64_t next = static_cast<uint64_t>(iters * factor) + 1;
        iters = (next <= iters) ? iters * 2 : next;
    }
}

static void report(const char *op, size_t size, double nsPerOp) {
    double mbPerS = 0.0;
    if (size > 0 && nsPerOp > 0) {
        mbPerS = (static_cast<double>(size) / (1024.0 * 1024.0)) / (nsPerOp / 1e9);
    }
    // human line
    printf("  %-20s size=%-8zu %10.1f ns/op   %10.1f MB/s\n", op, size, nsPerOp, mbPerS);
    // machine line: RESULT  impl  op  size  ns_per_op  mb_per_s
    printf("RESULT\tcpp\t%s\t%zu\t%.2f\t%.2f\n", op, size, nsPerOp, mbPerS);
    fflush(stdout);
}

int main() {
    MMKV::initializeMMKV(string("/tmp/mmkv_bench_cpp"), MMKVLogNone);
    MMKV *kv = MMKV::mmkvWithID(string("bench_cpp"));
    kv->clearAll();

    const std::vector<size_t> sizes = {16, 256, 4096, 65536, 1048576};

    printf("==== Native C++ (baseline) ====\n");

    // Pre-seed EVERY key up front so the underlying file is fully expanded
    // before any write benchmark. This mirrors the Go harness (TestMain seeds
    // all sizes before running). Without it, the small-size set benchmarks run
    // against a tiny file and pay MMKV's expand/full-rewrite cost on nearly
    // every call, which is a harness artifact, not an FFI cost.
    std::vector<std::vector<char>> datas(sizes.size());
    std::vector<string> bkeys(sizes.size()), skeys(sizes.size());
    for (size_t i = 0; i < sizes.size(); i++) {
        size_t s = sizes[i];
        bkeys[i] = "bytes_" + std::to_string(s);
        skeys[i] = "str_" + std::to_string(s);
        datas[i].resize(s);
        for (size_t j = 0; j < s; j++) {
            datas[i][j] = static_cast<char>('A' + (j % 26));
        }
        MMBuffer value((void *)datas[i].data(), s, MMBufferNoCopy);
        kv->set(value, bkeys[i]);
        kv->set(value, skeys[i]);
    }
    kv->set(int32_t(123456789), "int32");

    // ---- int32: isolates the pure call cost (no payload copy) ----
    {
        const string key = "int32";
        int32_t counter = 0;
        report("set_int32", 0, benchNs([&] {
            kv->set(counter++, key);
        }));
        report("get_int32", 0, benchNs([&] {
            g_sink += static_cast<uint64_t>(kv->getInt32(key));
        }));
    }

    // ---- bytes / string across sizes ----
    for (size_t i = 0; i < sizes.size(); i++) {
        size_t s = sizes[i];
        const string &bkey = bkeys[i];
        const string &skey = skeys[i];
        MMBuffer value((void *)datas[i].data(), s, MMBufferNoCopy);

        // write (same path serves both cgo variants; report under bytes)
        report("set_bytes", s, benchNs([&] {
            kv->set(value, bkey);
        }));

        // read, shared analog: decode into MMBuffer, touch in place, no extra copy
        report("get_bytes_shared", s, benchNs([&] {
            MMBuffer v = kv->getBytes(bkey);
            auto *p = static_cast<uint8_t *>(v.getPtr());
            if (v.length() > 0) {
                g_sink += p[0] + p[v.length() - 1] + v.length();
            }
        }));

        // read, copy analog: decode into a freshly owned std::string
        report("get_bytes_copy", s, benchNs([&] {
            string out;
            kv->getString(bkey, out);
            if (!out.empty()) {
                g_sink += static_cast<uint8_t>(out[0]) + out.size();
            }
        }));

        // string variants (MMKV stores string and bytes identically; included
        // to mirror Go GetString / GetStringBuffer 1:1)
        report("get_string_shared", s, benchNs([&] {
            MMBuffer v = kv->getBytes(skey);
            auto *p = static_cast<uint8_t *>(v.getPtr());
            if (v.length() > 0) {
                g_sink += p[0] + v.length();
            }
        }));
        report("get_string_copy", s, benchNs([&] {
            string out;
            kv->getString(skey, out);
            if (!out.empty()) {
                g_sink += static_cast<uint8_t>(out[0]) + out.size();
            }
        }));
    }

    printf("\n(sink=%llu)\n", static_cast<unsigned long long>(g_sink));
    return 0;
}

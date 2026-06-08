# mmkv-go read performance: pure Go vs cgo (same env, same file)

> **English** · [中文](BENCHMARK.zh-CN.md)

Environment: arm64 Linux container (OrbStack), the same MMKV file, read in the
same process via the official cgo library and via mmkv-go. Command: `go test
-bench . -benchmem` under `harness/`. mmkv-go uses **transparent refresh by
default** (check-on-read), so every Get includes the change probe (~+1 ns).

| Read op | Mode | ns/op | B/op | allocs/op | vs cgo |
|---|---|--:|--:|--:|--:|
| int32 | cgo | 91.0 | 0 | 0 | — |
| int32 | **pure** | **8.9** | 0 | 0 | **10.2× faster** |
| bytes 4KB | cgo copy | 776.5 | 4104 | 2 | — |
| bytes 4KB | cgo shared (zero-copy) | 223.6 | 8 | 1 | — |
| bytes 4KB | **pure view (zero-copy)** | **10.4** | 0 | 0 | **75× / 21×** |
| bytes 4KB | pure copy | 576.4 | 4096 | 1 | 1.35× vs cgo copy |
| bytes 6B | cgo copy | 154.3 | 16 | 2 | — |
| bytes 6B | **pure view** | **8.9** | 0 | 0 | **17× faster** |
| string 38B | cgo copy (GetString) | 147.1 | 56 | 2 | — |
| string 38B | cgo view (GetStringBuffer+StringView) | 133.3 | 8 | 1 | — |
| string 38B | **pure view (GetString, unsafe.String)** | **9.7** | 0 | 0 | **14× vs cgo view** |
| string 38B | pure copy (GetStringCopy) | 25.8 | 48 | 1 | 5.7× vs cgo copy |

> Both sides offer copy and zero-copy string reads. purego `GetString` is a
> zero-copy `unsafe.String` view over the buffer (9.7 ns, 0 alloc); `GetStringCopy`
> is the safe copy. cgo `GetString` copies C→Go (GoStringN); `GetStringBuffer`+
> `StringView` is its zero-copy path — yet even that (133 ns) is **~14× slower than
> purego's view**. The gap is the cgo boundary tax, **not** the copy.

> These are local OrbStack numbers. CI numbers on shared runners are noisier and
> slower in absolute terms — trust the **relative ratios** (see the per-version ×
> arch report in the CI job summary).

## Why it's this fast

1. **No cgo boundary tax** (the ~65 ns/call disappears).
2. **Parse-once**: `Open()` parses the whole file into `map[string][]byte`; afterwards every Get is a lock-free
   snapshot load + map lookup + tiny decode.
3. **Zero-copy view**: `GetBytes` returns a sub-slice into the internal buffer — no copy, so 4KB and 6B are both
   ~9–10 ns and 0 alloc.
4. **Transparent refresh is nearly free**: the check-on-read probe is a lock-free memory read, ~+1 ns/read; still
   cheaper than MMKV C++ (which takes a flock on every read).

## Honest caveats (don't misread the table)

- **These are "steady-state repeated read" numbers.** `Open()`'s whole-file parse cost (O(file size), one-time) is
  not counted in per-Get. Applies to: read-heavy, long-lived Reader doing repeated reads. **Does not apply** to
  open-read-once-close (amortization doesn't hold there).
- **View lifetime**: the view returned by `GetBytes` / `GetString` (under the hood) becomes invalid after the next
  reload/`Close()` and must not be mutated — the same kind of constraint as cgo shared needing `Destroy()`. For an
  independent copy (or multi-goroutine live use), use `GetBytesCopy`.
- **String still allocates once** (48B): Go's `string` type requires a copy by semantics, so it can't be zero-copy;
  use the `[]byte` view when you can.
- **Scope**: plaintext or AES-encrypted, optional key expiration, read-only, single-writer/multi-reader.
- **Writes stay on cgo**: readers **load the writer's changes transparently via check-on-read by default** (matching
  MMKV C++); there is no manual refresh API.

## Concurrency correctness (single-writer/multi-reader, `-race` verified)

A cgo writer (`MMKV_MULTI_PROCESS`) hammering writes + a pure-Go reader (transparent refresh, no manual refresh):

| reads | CRC errors | torn (garbage) | up to date |
|--:|--:|--:|--:|
| 472,372 | **0** | **0** | ✓ seq=3000 |

470k concurrent reads with zero torn reads, zero CRC errors, and observing the writer's final value. The key is the
**shared flock** taken on reload, which guarantees a consistent snapshot — (measured earlier: without that flock, a
million concurrent re-reads do hit CRC errors, i.e. torn reads caught by CRC; with it, zero). See
`harness/concurrency_test.go`.

## Conclusion

Going pure Go on the read path (approach F) is a big win for **read-heavy** workloads: scalars ~10×, zero-copy bytes
17–75×, most reads 0 alloc; and under single-writer/multi-reader, **transparent refresh by default** (check-on-read)
is concurrency-safe and auto-loads changes. The cost is tight format coupling (guarded by the version allowlist + the
CI differential test) and limited functionality (multi-writer not covered; writes/restore via cgo).

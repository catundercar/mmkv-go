# Full-featured pure-Go MMKV (POSIX) — design

> **English** · [中文](MMKV_FULL_DESIGN.zh-CN.md)

This is the plan to grow the project from a cgo-free **read-only** `Reader`
(see [DESIGN.md](DESIGN.md)) into a **full read + write** pure-Go MMKV for POSIX
(Linux + macOS). Every fact below was verified against the MMKV **v2.4.0** Core
source; file:line citations point into `MMKV/Core/`.

## 1. Scope (locked decisions)

| Axis | Decision | Consequence |
|---|---|---|
| Interop | **Full C++ interop** | On-disk format + flock protocol byte-identical to C++, so Go and official C++ instances share the same files cross-process. Acceptance is a **bidirectional** differential. |
| Features | **Everything** | CORE (read/write/remove/clear/trim/sync/expire/namespace/lock) + encryption&reKey, backup/restore, handlers, compareBeforeSet. |
| Concurrency | **Multi-writer** | Multiple Go + C++ writers on one file. Delivered by the exclusive-flock + `checkLoadData` reconciliation that single-writer interop already needs; the extra cost is the test matrix. |
| Read API | **Single `MMKV` type** | The read-only `Reader` folds into one `MMKV`. Read speed is preserved by *mode*: single-process disables flock (reads stay ~10 ns zero-copy); multi-process takes a shared flock per read (correct under concurrent writers, exactly like C++). A read-only open mode skips write machinery. |

POSIX only (Linux + macOS); Android/iOS/Windows out of scope. Writes never use cgo.

## 2. On-disk format (verified byte recipe)

### 2.1 Data file `<mmapID>`
```
[0,4)              legacy Fixed32 actualSize header — left 0 for version>=3 (readers use meta.actualSize)
[4, 4+actualSize)  KV-log region
[4+actualSize, …)  free space (page-rounded tail), ignored
```
The KV region is: a **4-byte ItemSizeHolder** varint placeholder, then pairs.
- ItemSizeHolder = `randomItemSizeHolder(4)` ∈ `[0x200000, 0x10000000)` so it always encodes to 4 bytes (`AESCrypt.cpp:33`). It is read via `readInt32()` and **discarded** (`MiniPBCoder.cpp:504` `decodeOneMap`) — semantically irrelevant; a fixed value is fine for plaintext.
- Each pair = `writeData(key)` + `writeData(valueBlob)`, where `writeData` = `varint(len)+bytes` (`CodedOutputData.cpp:76`).
- **Value-blob wrapping** (the crux): scalars are single-wrapped (the pair's outer `writeData` is the only length prefix); **string/bytes are double-wrapped** — the blob is itself `varint(len)+raw`, so the pair yields outer+inner length (`isDataHolder=true`, `MMKV_IO.cpp:907,938`).
- **Negative int32 → 10-byte varint** (sign-extended to 64 bits) (`CodedOutputData.cpp:60`). Classic interop footgun.
- Deletion = a pair with an empty value (tombstone); replays last-write-wins.

### 2.2 Meta file `<mmapID>.crc` — `MMKVMetaInfo`, 112 bytes LE (`MMKVMetaInfo.hpp`)
| off | field | off | field |
|--:|---|--:|---|
| 0 | crcDigest | 32 | lastActualSize |
| 4 | version | 36 | lastCRCDigest |
| 8 | sequence | 40 | _reserved[64] |
| 12 | IV[16] | 104 | flags (bit0 = expire) |
| 28 | actualSize | | |

### 2.3 Version rule (verified)
Every `writeActualSize` bumps version to **4 (Flag)** if below it (`MMKV_IO.cpp:611`). So a fresh plaintext file is `version=4, sequence=1, flags=0`, IV all-zero, `lastConfirmed=(actualSize,crc)`. CRC32 (IEEE) is over the data region — over the **ciphertext** when encrypted.

### 2.4 Crash safety / recovery
`lastActualSize`/`lastCRCDigest` is the rollback floor: advanced **only** on a sequence-bumping full write-back (never on a plain append), so a torn append rolls back to the last fully-synced snapshot. On load (`MMKV_IO.cpp` `checkDataValid`→`checkLastConfirmedInfo`): try current `(actualSize,crc)`; else legacy `[0,4)` header; else `(lastActualSize,lastCRCDigest)`; else recover (greedy-decode + full write-back) or discard.

## 3. Architecture

### 3.1 Append vs full write-back (`MMKV_IO.cpp`)
- `needed = varint(keyLen)+key + [innerVarint] + varint(valLen)+val (+4 if expire)`; `spaceLeft = fileSize-4-actualSize`.
- `needed < spaceLeft && dict非空` → **append**: write at `4+actualSize`, encrypt-in-place if needed, `actualSize+=needed`, **incremental** CRC, write meta crc+actualSize **only**, **no sequence bump, no sync**.
- else → **full write-back**: (filter expired) re-pack from offset 4 with a fresh ItemSizeHolder; grow first if needed (**double until it fits + headroom**, page-rounded `ftruncate` + zero-fill + munmap/mmap); `actualSize=total`; **whole-region** CRC; **sequence++**; advance `lastConfirmed`; rotate IV if encrypted; write full 112-byte meta; `msync(data, then meta)` when `needSync`.

### 3.2 Concurrency (locks)
C++ discipline (verified): **thread lock first → process flock (shared read / exclusive write) → `checkLoadData` under the lock**; flock on the `.crc` fd via `flock(2)` (not fcntl; fcntl is Android-only) (`InterProcessLock.cpp:94`).
- **Reentrancy**: Go has no recursive mutex and C++ self-re-enters. Resolution: public method `Foo()` locks once; all internal helpers `foo()` assume-locked; never call a locking method from a locked path.
- **flock ref-counting**: one `fileLock{fd, shared, exclusive}` with two typed handles. Shared bumps for free if any lock is held; exclusive upgrades from shared (optimistic `LOCK_EX|NB`, else drop-shared then block, recover on fail); the last exclusive release downgrades to `LOCK_SH` if shared refs remain. flock is per-open-file-description: within a process goroutines are serialized by the thread lock; flock only serializes across processes — so `fileLock` is used **under** the thread lock and needs no mutex of its own (mirrors C++ `FileLock`).
- **`checkLoadData`**: compare in-memory meta vs the live mmap'd `.crc`. `sequence` changed → full reload; `crc`/`actualSize` changed → incremental (`partialLoadFromFile`).
- **Single instance per file per process** (registry, like `g_instanceDic`): mandatory — two instances on one file would each flock the same OFD (no mutual exclusion) and diverge in memory.

### 3.3 Module layout
`reader.go` (folds into `MMKV`) · `mmkv.go` (type, registry, lifecycle, checkLoadData) · `mmkv_io.go` (set/append/fullWriteback/grow/load/recover) · `memfile.go` (RW mmap + truncate/remap + msync) · `flock.go` (ref-counted flock) · `encode.go` (CodedOutputData) · `coder.go` (map/containers, double-wrap) · `wire.go`/`meta.go`/`crypt.go`/`path.go` (decode/meta/AES-CFB/naming).

## 4. API sketch (Go)
```go
func InitializeMMKV(rootDir string, opts ...InitOption) error
func MMKVWithID(mmapID string, opts ...Option) (*MMKV, error) // WithMultiProcess/WithEncryption/WithRootPath/WithReadOnly/WithExpectedCapacity
func (m *MMKV) SetBool/SetInt32/.../SetString/SetBytes/SetStringSlice(key, v) error
func (m *MMKV) GetBool/.../GetString/GetStringCopy/GetBytes/GetBytesCopy(key) (T, bool)
func (m *MMKV) RemoveValueForKey/RemoveValuesForKeys(...) error
func (m *MMKV) ClearAll(keepSpace bool) / Trim() / Sync() / Async() error
func (m *MMKV) Count(filterExpire bool) int; AllKeys(...) []string; Contains(key) bool
func (m *MMKV) EnableAutoKeyExpire(sec uint32) error; DisableAutoKeyExpire() error; ReKey(newKey []byte) error
func (m *MMKV) Lock(); Unlock(); TryLock() bool       // cross-process atomic read-modify-write
func NameSpace(rootDir string) NS; BackupOneToDirectory(...) / RestoreOneFromDirectory(...) error
```
Zero-copy view lifetime tightens vs the reader: the data is RW-mmap'd and can remap on grow or on a reload triggered by another process, so a view is valid **only until the next call on that instance** (matching C++ `MMBufferNoCopy`). Default getters return views; `…Copy` for retention.

## 5. Status — implemented

The live read+write `MMKV` type is implemented and verified (host `-race` + a
bidirectional cgo differential on v2.4.0; CI gates the differential across the
version matrix):

- **Core**: registry (one instance per file per process), open/load with
  last-confirmed recovery, per-op locking (thread lock + shared/exclusive flock;
  disabled in single-process), checkLoadData freshness (full reload + remap /
  incremental), the single-key override + append fast paths + full
  write-back + grow, all typed
  Get/Set, Remove(s)/ClearAll/Trim/Count/AllKeys/Contains/TotalSize/ActualSize,
  Sync/Async.
- **Encryption**: `WithCryptKey` (AES-128/256), encrypted full write-back with IV
  rotation, `ReKey` (plaintext↔encrypted + key change).
- **Expiration**: `EnableAutoKeyExpire`/`DisableAutoKeyExpire` (trailing 4-byte
  timestamp, meta flag, filter-on-read).
- **Concurrency**: public `Lock`/`Unlock`/`TryLock`; a pure-Go multi-process test
  (writer + readers as separate processes, zero torn reads).
- **Containers & migration**: `SetStringSlice`/`GetStringSlice` (vector<string>),
  `ImportFrom`, `GetValueSize`/`WriteValueToBuffer`.
- **Ops**: `BackupOne` + pure-Go `RestoreOneFromDirectory`, `NameSpace`,
  `CheckExist`/`IsFileValid`/`RemoveStorage`, `compareBeforeSet`, content-changed
  + recover handlers, read-only mode (`WithReadOnly`).

`vector<string>` isn't exposed by the Go cgo binding, so it has Go unit tests
only (round-trip + spec byte-layout) and no cgo differential. The read-only
`Reader` is kept as the zero-copy, lock-free specialization (the benchmark path +
the cgo-equivalence gate); a single type still covers read-only use via
`WithReadOnly`.

Not yet optimized: encrypted writes always full-rewrite (incremental encrypted
append is future work); the writer always emits format v4 (cross-version writes
to a pre-v1.3.0 target would need version targeting). C++20 numeric vectors
(`vector<int/float/…>`) are out of scope — the binding doesn't expose them either.

## 6. Verification (acceptance gates)
1. **Bidirectional differential** across the 7-version × 2-arch matrix: C++ writes → Go reads (existing `TestCgoEqualsPurego`) **and** Go writes → C++ reads (`TestPuregoWriteCgoReads`), all types/boundaries.
2. **Multi-process flock interlock**: {Go-writer × C++-writer × Go-reader}, zero torn reads, up to date.
3. **Crash recovery**: inject a torn tail (truncate / kill mid-write, no sync) → roll back to `lastConfirmed`; Go and C++ recover identically.
4. `-race`; write-throughput benchstat vs cgo.

## 7. Risks
1. Reentrant-lock restructuring (public-locks / private-assumes-locked discipline must be total).
2. **Write-side mmap view lifetime**: a grow remap unmaps the old mapping → a retained `[]byte`/`string` view dangles (segfault). Views invalid across writes/reloads; `…Copy` otherwise.
3. flock interop exactness (upgrade/downgrade/counting) — any drift deadlocks or tears with a C++ peer.
4. Encryption: CFB stream-state on append; `ReKey` atomicity (rollback on mid-write crash).
5. Crash safety depends on the msync order (data, then meta) + `lastConfirmed`.
6. Format drift across MMKV releases → the **bidirectional** CI differential + version allowlist guard it.
7. Cross-version writes: the Writer emits version=4 (read by MMKV ≥ v1.3.0); writing the minimal version that a specific target supports is part of completing Phase A.

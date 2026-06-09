# mmkv-go — pure-Go read-only MMKV reader (approach F)

> **English** · [中文](DESIGN.zh-CN.md)

Goal: **keep the read path entirely off cgo**, eliminating the ~65 ns/call cgo
boundary tax. Writes stay on the official cgo library (which guarantees correct
format / locking / compatibility). This library does **read-only decode** only.

Target scenario (confirmed with the requester): **plaintext, single-writer /
multi-reader** — one cgo writer process plus many pure-Go reader processes, where
readers can safely load changes while the writer writes. Encryption is decoupled
at the interface level and can be plugged in later.

> **Hard requirement on the writer**: it MUST open with `MMKV_MULTI_PROCESS`.
> Otherwise MMKV takes no cross-process lock ([MMKV.cpp:113](../MMKV/Core/MMKV.cpp))
> and may cache changes in memory without flushing meta promptly — readers would
> then be neither safe nor up to date.

## 1. Responsibility boundaries (hard constraints)

- **Read-only**: never writes, never compacts. All writes go through the official `tencent.com/mmkv`.
- **Concurrent reads**: **transparent refresh by default** (matches MMKV C++, no manual API) — mmap `.crc` +
  check-on-read; on a change it takes a shared `flock` on `.crc` (interlocking with the MMKV writer's exclusive
  lock) and reloads, guaranteeing a consistent snapshot. POSIX-only (MMKV uses flock + mmap on non-Android).
- **Version gate**: `meta.version` outside the known allowlist → returns `ErrUnsupportedVersion`; never force-decodes.
- **Key expiration**: decoded — each value carries a trailing 4-byte little-endian expire timestamp; expired keys
  (timestamp ≠ 0 and ≤ now) read as absent and are filtered from `Keys()` (matches MMKV).
- **Correctness backstop**: CRC32 over the data region; mismatch → `ErrCRCMismatch` (also catches "an encrypted file
  read with the wrong/no key").
- **Encryption**: built-in AES-CFB-128/256 via `WithEncryption(key)`, or a custom injected `Decryptor`;
  default (no option) = plaintext.

## 2. MMKV on-disk format (confirmed from Core source)

### 2.1 Meta file `<mmapID>.crc` = the `MMKVMetaInfo` fixed-layout struct (little-endian)
Source: [Core/MMKVMetaInfo.hpp](../MMKV/Core/MMKVMetaInfo.hpp), `AES_IV_LEN=16`:

| Offset | Field | Type |
|--:|---|---|
| 0 | crcDigest | u32 |
| 4 | version | u32 |
| 8 | sequence (full write-back count) | u32 |
| 12 | IV[16] | bytes |
| 28 | **actualSize** | u32 |
| 32 | lastConfirmed.lastActualSize | u32 |
| 36 | lastConfirmed.lastCRCDigest | u32 |
| 40 | _reserved[16] | 64 bytes |
| 104 | **flags** (bit0 = key expiration on) | u64 |

`version`: 3 = ActualSize (actualSize stored in meta), 4 = Flag (flags present).

### 2.2 Data file `<mmapID>`
Source: [Core/MMKV_IO.cpp:100](../MMKV/Core/MMKV_IO.cpp):

```
[0,4)              Fixed32 = actualSize (little-endian, legacy header)
[4, 4+actualSize)  KV log region (valid data)
[4+actualSize, ..) append free space (ignored)
```
For `version>=3`, `meta.actualSize` is authoritative.

### 2.3 KV log (protobuf wire format)
Parsing mirrors [Core/MiniPBCoder.cpp:504](../MMKV/Core/MiniPBCoder.cpp) `decodeOneMap`:

```
build a reader over the data region [4, 4+actualSize):
  read one varint (dictionary total length, discarded)
  while not at end:
    key = readString()              # varint length + UTF-8 bytes
    if len(key) > 0:
        val = readData()            # varint length + raw bytes (value blob)
        if len(val) > 0: m[key] = val      # last write wins
        else:            delete m[key]      # empty value = deletion
    # empty key: don't read a value, continue (mirrors the C behavior)
```

Primitives ([Core/CodedInputData.cpp](../MMKV/Core/CodedInputData.cpp)):
- `readString` / `readData` = `varint length + N bytes`
- `readBool` / `readInt*` / `readUInt*` = LEB128 varint
- `readFloat` = little-endian fixed32 → IEEE754; `readDouble` = little-endian fixed64 → IEEE754

### 2.4 Typed decoding (from the value blob)
Storage holds only the blob; the type is decoded by the reader:
- `GetBytes` → blob as-is; `GetString` → `string(blob)`
- `GetBool/Int32/Int64/UInt32/UInt64` → varint-decode the blob
- `GetFloat32` → little-endian fixed32; `GetFloat64` → fixed64

### 2.5 Concurrent file lifecycle (same policy as MMKV)
MMKV **only overwrites live files in place and never swaps the inode**: changes are detected via the meta probe
(sequence/crc/actualSize) in the mmap, and there is **no `st_ino` / `inotify` / periodic stat anywhere** in the
tree (`checkLoadData` [MMKV_IO.cpp:373](../MMKV/Core/MMKV_IO.cpp)).
- restore (live instance): `copyFileContent` writes the existing fd + `memcpy` into the existing meta mmap
  ([MMKV.cpp:1475/1487](../MMKV/Core/MMKV.cpp)); the comment states plainly "can't replace the file, or other
  processes won't notice" ([:1441](../MMKV/Core/MMKV.cpp)).
- backup uses `copyFile` (atomic rename → new inode) but writes to the **backup directory**, unrelated to live files.
- `removeStorage` is the only unlink, and it `close`s the instance first ([MMKV_IO.cpp:1731-1736](../MMKV/Core/MMKV_IO.cpp)).

mmkv-go's mmap + check-on-read follows the same policy, so normal writes and restore need no extra handling.

## 3. API design

```go
type Decryptor interface {
    // Decrypt turns the encrypted data region into plaintext wire bytes. iv is the
    // per-file IV from the meta (.crc m_vector) — passed each reload since a full
    // write-back can rotate it.
    Decrypt(ciphertext, iv []byte) (plaintext []byte, err error)
}

type Reader struct { /* ... */ }

func Open(rootDir, mmapID string, opts ...Option) (*Reader, error) // transparent refresh (check-on-read) by default, POSIX
func WithEncryption(key []byte) Option // AES-CFB-128/256 (width by key length, like MMKV)
func WithDecryptor(d Decryptor) Option // custom decryptor

func (r *Reader) Err() error // last reload error (a failed reload keeps serving the prior snapshot); no manual Refresh
func (r *Reader) Close() error
func (r *Reader) Keys() []string
func (r *Reader) Contains(key string) bool
func (r *Reader) GetBool/GetInt32/GetInt64/GetUInt32/GetUInt64/GetFloat32/GetFloat64(key) (T, bool)
func (r *Reader) GetString(key string)     (string, bool) // zero-copy unsafe.String view, valid until next reload/Close
func (r *Reader) GetStringCopy(key string) (string, bool) // independent copy
func (r *Reader) GetBytes(key string)      ([]byte, bool) // internal-buffer view, valid until next reload/Close; do not mutate
func (r *Reader) GetBytesCopy(key string)  ([]byte, bool) // independent copy (recommended for multi-goroutine live use)

// namespace (custom root) + backup
func OpenNameSpace(rootDir string) NameSpace
func (ns NameSpace) Open(mmapID string, opts ...Option) (*Reader, error)
func BackupOne(rootDir, mmapID, dstDir string) error // copies data + .crc under a shared flock; restore goes through cgo
```

**State is held in `atomic.Pointer[snapshot]`**: readers load the immutable snapshot lock-free; a reload builds a
new snapshot and swaps the pointer atomically, so concurrent reads never race with a refresh (verified with `-race`).

Read path: `Open` reads `.crc` + the data file → gate checks → `Decrypt` (identity for plaintext) → parse into
`map[string][]byte` (blobs are sub-slices of the buffer, zero-copy) → afterwards every Get is a lock-free snapshot
load + map lookup (~8 ns).

**Transparent refresh (default, no manual API, matching MMKV C++)**: mmap `.crc`; each read first compares the
**change probe (crcDigest, actualSize, sequence)** (a lock-free memory read, matching MMKV's `checkLoadData`: a
normal set bumps crc/actualSize, only a full write-back bumps sequence, so all three are compared). Only on a change
does it take the shared flock, reload, and swap atomically. The probe costs about **+1 ns/read**, near-zero when
idle — "every read is automatically up to date" like MMKV C++, but cheaper (MMKV takes a flock on every read).

## 4. Phases

- **Phase 1 (done, TDD)**: plaintext + no-expire + read-only. The differential test is the oracle: the official cgo
  library writes/reads back → pure Go asserts equality key-by-key.
- **Phase 2 (done)**: CRC checking; **transparent refresh by default** (mmap + check-on-read + shared flock) for
  safe single-writer/multi-reader concurrency; namespace + special-character filenames; pure-Go `BackupOne`. Verified
  with a `-race` same-process concurrency test (cgo MP writer + pure-Go reader, ~470k reads, zero torn reads, always
  up to date — `harness/concurrency_test.go`) and a true **multi-process** test (a cgo writer process + several pure-Go
  reader processes, exercising the cross-process flock interlock — `harness/multiprocess_test.go`).
- **Phase 3 (done)**: encryption — built-in AES-CFB-128/256 (`WithEncryption`), IV from meta, single contiguous CFB
  stream, CRC over ciphertext; verified by NIST CFB known-answer vectors (host) + a cgo differential on v2.4.0. Key
  expiration — trailing 4-byte timestamp stripped + filtered; cgo differential (never / actually-expired) on v2.4.0.
  (The crypt/expire differential uses the v2.4.0 unified-config binding; the format is version-stable.)

## 5. Risks / tech debt

1. **Tight format coupling**: an MMKV upgrade could silently change the format → guarded by the version allowlist + the CI differential test.
2. **Encryption / expiration**: implemented (AES width inferred from key length, matching MMKV; expiration filtered at
   read against the host clock). Wrong/missing key surfaces as `ErrCRCMismatch` (CRC is over the ciphertext).
3. **Concurrency**: supports single-writer/multi-reader (writer must use `MMKV_MULTI_PROCESS`; readers refresh
   transparently). Multiple writers are out of scope. **Inode replacement is not an issue**: MMKV deliberately
   overwrites live files in place (including restore, see the [MMKV.cpp:1441](../MMKV/Core/MMKV.cpp) comment +
   `copyFileContent`) and never swaps the inode, so mmkv-go's `.crc` mmap stays valid and check-on-read keeps
   noticing changes — same source, same policy. The only unlink is `removeStorage` (which `close`s the instance
   first) — a coordinated teardown; readers keep serving the prior snapshot until they re-`Open` (MMKV's own live
   mmap also does not auto-recover).
4. **Writes stay on cgo**: including restore (a write operation; the writer uses the official `RestoreOneFromDirectory`).
5. **Value-view lifetime**: `GetBytes` and `GetString` return zero-copy views over the snapshot buffer (heap, kept
   alive by the GC while held); they show stale data after a reload and must not be mutated via an alias. For
   independent copies / multi-goroutine live use, use `GetBytesCopy` / `GetStringCopy`.

## 6. Testing (TDD)

- `wire_test.go`: vector unit tests for varint / fixed32 / fixed64 / readString / readData (no fixture needed, runs on the host).
- `meta_test.go`: builds a 112-byte meta and asserts each field offset.
- `reader_test.go`: reads fixtures under `testdata/` generated by the official library (`*.mmkv` + `*.crc` +
  `expected.json`) and asserts equality key-by-key via the matching typed getter.
- Fixture generator `tools/gen/` (a separate module using cgo `tencent.com/mmkv`, run once in the container).
- Acceptance: `go test ./...` (pure Go on the host) all green + key-by-key differential equality.

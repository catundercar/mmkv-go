# mmkv-go — 纯 Go MMKV 只读解析器（方案 F）

目标：**读路径完全不走 cgo**，消除 ~65ns/次的 cgo 边界税。写仍交给官方 cgo 库，
保证格式/锁/兼容性正确。本库只做 **read-only decode**。

适用场景（已与需求方确认）：**明文、单写多读**——一个 cgo 写进程 + 多个纯 Go 读进程，
写时读进程能安全加载变更。加密在接口层解耦、后续可插。

> **写进程硬性要求**：必须用 `MMKV_MULTI_PROCESS` 模式打开。否则 MMKV 不加跨进程锁
> （[MMKV.cpp:113](../MMKV/Core/MMKV.cpp)）、也可能把变更缓存在内存不及时刷 meta，读进程既不安全也看不到变更。

## 1. 职责边界（硬约束）

- **只读**：本库不写、不 compaction。写一律走官方 `tencent.com/mmkv`。
- **并发读**：**默认透明刷新**（对齐 MMKV C++，无手动接口）——mmap `.crc` + check-on-read，变更时对
  `.crc` 加共享 `flock`（与 MMKV 写进程排他锁互锁）reload，保证一致快照。POSIX-only（MMKV 非 Android 用 flock + mmap）。
- **版本闸门**：`meta.version` 超出已知白名单 → 返回 `ErrUnsupportedVersion`，不硬解。
- **能力闸门**：过期 flag 置位 → `ErrExpireUnsupported`（MVP 不支持，绝不返回乱码）。
- **正确性兜底**：CRC32 校验数据区；不匹配 → `ErrCRCMismatch`（也能兜住"误把加密文件当明文读"）。
- **加密解耦**：通过注入 `Decryptor` 支持；默认 `nil` = 明文。MVP 不内置 AES。

## 2. MMKV 磁盘格式（已从 Core 源码确认）

### 2.1 元文件 `<mmapID>.crc` = `MMKVMetaInfo` 定长结构（小端）
来源 [Core/MMKVMetaInfo.hpp](../MMKV/Core/MMKVMetaInfo.hpp)，`AES_IV_LEN=16`：

| 偏移 | 字段 | 类型 |
|--:|---|---|
| 0 | crcDigest | u32 |
| 4 | version | u32 |
| 8 | sequence（全量回写次数） | u32 |
| 12 | IV[16] | bytes |
| 28 | **actualSize** | u32 |
| 32 | lastConfirmed.lastActualSize | u32 |
| 36 | lastConfirmed.lastCRCDigest | u32 |
| 40 | _reserved[16] | 64 bytes |
| 104 | **flags**（bit0 = 开启过期） | u64 |

`version`：3=ActualSize（actualSize 进 meta），4=Flag（有 flags）。

### 2.2 数据文件 `<mmapID>`
来源 [Core/MMKV_IO.cpp:100](../MMKV/Core/MMKV_IO.cpp)：

```
[0,4)              Fixed32 = actualSize（小端，遗留头）
[4, 4+actualSize)  KV 日志区（有效数据）
[4+actualSize, ..) append 空闲区（忽略）
```
`version>=3` 时以 `meta.actualSize` 为准。

### 2.3 KV 日志（protobuf wire format）
解析逻辑对齐 [Core/MiniPBCoder.cpp:504](../MMKV/Core/MiniPBCoder.cpp) `decodeOneMap`：

```
对数据区 [4, 4+actualSize) 建 reader：
  读一个 varint（字典总长，丢弃）
  while 未到末尾:
    key = readString()              # varint 长度 + UTF-8 字节
    if len(key) > 0:
        val = readData()            # varint 长度 + 原始字节（value blob）
        if len(val) > 0: m[key] = val      # 后写覆盖先写
        else:            delete m[key]      # 空 value = 删除
    # key 为空则不读 value，继续（镜像 C 行为）
```

原语（[Core/CodedInputData.cpp](../MMKV/Core/CodedInputData.cpp)）：
- `readString`/`readData` = `varint 长度 + N 字节`
- `readBool/readInt*/readUInt*` = LEB128 varint
- `readFloat` = 小端 fixed32 → IEEE754；`readDouble` = 小端 fixed64 → IEEE754

### 2.4 类型解码（从 value blob 取）
存储层只存 blob；类型由读取方解码：
- `GetBytes` → blob 原样；`GetString` → `string(blob)`
- `GetBool/Int32/Int64/UInt32/UInt64` → 对 blob 做 varint 解码
- `GetFloat32` → blob 小端 fixed32；`GetFloat64` → fixed64

### 2.5 并发文件生命周期（与 MMKV 同策）
MMKV 对 live 文件**只原地覆盖、从不换 inode**：变更靠 mmap 的 meta 探针 (sequence/crc/actualSize) 被发现，
**全仓无 `st_ino`/`inotify`/周期 stat**（`checkLoadData` [MMKV_IO.cpp:373](../MMKV/Core/MMKV_IO.cpp)）。
- restore（存活实例）：`copyFileContent` 写已有 fd + `memcpy` 进已有 meta mmap（[MMKV.cpp:1475/1487](../MMKV/Core/MMKV.cpp)），
  注释明言“不能换文件，否则别的进程感知不到”（[:1441](../MMKV/Core/MMKV.cpp)）。
- backup 用 `copyFile`（原子 rename 换 inode）写的是**备份目录**，与 live 文件无关。
- `removeStorage` 是唯一 unlink，但先 `close` 实例（[MMKV_IO.cpp:1731-1736](../MMKV/Core/MMKV_IO.cpp)）。

mmkv-go 的 mmap + check-on-read 与此一致，故 normal 写入与 restore 都无需额外处理。

## 3. 接口设计

```go
type Decryptor interface {
    // Decrypt 把数据区密文整体还原为明文 wire 字节。明文场景注入 nil。
    Decrypt(ciphertext []byte) (plaintext []byte, err error)
}

type Reader struct { /* ... */ }

func Open(rootDir, mmapID string, opts ...Option) (*Reader, error) // 默认透明刷新(check-on-read)，POSIX
func WithDecryptor(d Decryptor) Option

func (r *Reader) Err() error // 最近一次 reload 的错误（reload 失败则继续供旧快照）；无手动 Refresh
func (r *Reader) Close() error
func (r *Reader) Keys() []string
func (r *Reader) Contains(key string) bool
func (r *Reader) GetBool/GetInt32/GetInt64/GetUInt32/GetUInt64/GetFloat32/GetFloat64(key) (T, bool)
func (r *Reader) GetString(key string) (string, bool)
func (r *Reader) GetBytes(key string)     ([]byte, bool) // 内部缓冲视图，下次 reload/Close 前有效，勿改
func (r *Reader) GetBytesCopy(key string) ([]byte, bool) // 独立副本（live 多 goroutine 推荐）

// namespace（自定义 root）+ 备份
func OpenNameSpace(rootDir string) NameSpace
func (ns NameSpace) Open(mmapID string, opts ...Option) (*Reader, error)
func BackupOne(rootDir, mmapID, dstDir string) error // 持共享 flock 拷贝 data+.crc；restore 走 cgo
```

**状态用 `atomic.Pointer[snapshot]`**：读侧无锁加载不可变快照；reload 构造新快照再原子替换，
并发读永不与刷新竞争（`-race` 验证通过）。

读取路径：`Open` 读 .crc + 数据文件 → 闸门检查 → `Decrypt`（明文恒等）→ 解析进
`map[string][]byte`（blob 为缓冲子切片，0 拷贝）→ 之后每次 Get 是无锁快照 + map 查 (~8ns)。

**透明刷新（默认，无手动接口，对齐 MMKV C++）**：mmap `.crc`，每次读先比**变更探针 (crcDigest, actualSize,
sequence)**（无锁内存读，对齐 MMKV `checkLoadData`：普通 set 改 crc/actualSize，全量回写才改 sequence，故三者都比）。
任一变化才取共享 flock reload + 原子替换。探针约 **+1ns/读**，空闲近零成本——和 MMKV C++ 一样"每次读自动最新"，但比它便宜（MMKV 每次读还要 flock）。

## 4. 分阶段

- **Phase 1（完成，TDD）**：明文 + 无过期 + 只读。差分测试为预言：官方 cgo 写/读回 → 纯 Go 逐 key 断言相等。
- **Phase 2（完成）**：CRC 校验；**默认透明刷新**（mmap + check-on-read + 共享 flock）单写多读安全并发；
  namespace + 特殊字符文件名；纯 Go `BackupOne`。已用 `-race` 并发测试验证
  （cgo MP 写 + 纯 Go 读，~47 万次读零撕裂、读到最新，见 `harness/concurrency_test.go`）。
- **Phase 3（硬骨头）**：实现 `Decryptor`（AES-CFB，IV 取自 meta，逐字节对齐 `CodedInputDataCrypt`）；过期值布局。

## 5. 风险 / 技术债

1. **格式强耦合**：MMKV 升版可能静默改格式 → 版本白名单 + CI 差分测试守住。
2. **加密/过期**：未实现，主动闸门拒绝，靠 CRC 兜底防乱码。
3. **并发**：支持单写多读（写进程须 `MMKV_MULTI_PROCESS`；读进程默认透明刷新）。多写进程不在范围。
   **inode 替换不是问题**：MMKV 故意原地覆盖 live 文件（含 restore，见 [MMKV.cpp:1441](../MMKV/Core/MMKV.cpp)
   注释 + `copyFileContent`），从不换 inode；mmkv-go 的 `.crc` mmap 因此始终有效、check-on-read 照常感知，
   与 MMKV 同源同策。唯一 unlink 是 `removeStorage`（且先 `close` 实例）——协调式销毁，读端继续供旧快照
   直到重 `Open`（MMKV 的 live mmap 亦不自动恢复）。
4. **写仍走 cgo**：含 restore（写操作，写进程用官方 `RestoreOneFromDirectory`）。
5. **value 视图生命周期**：`GetBytes` 返回内部缓冲视图，下次 reload/`Close` 后失效；live 多 goroutine 用 `GetBytesCopy`。

## 6. 测试（TDD）

- `wire_test.go`：varint / fixed32 / fixed64 / readString / readData 向量单测（无需 fixture，宿主机跑）。
- `meta_test.go`：构造 112 字节 meta，断言各字段偏移正确。
- `reader_test.go`：读 `testdata/` 下由官方库生成的 fixture（`*.mmkv` + `*.crc` + `expected.json`），
  逐 key 用对应 typed getter 断言相等。
- fixture 生成器 `tools/gen/`（独立 module，用 cgo `tencent.com/mmkv`，容器内跑一次）。
- 验收：`go test ./...`（宿主机纯 Go）全绿 + 差分逐 key 一致。

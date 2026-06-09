# 全功能纯 Go MMKV(POSIX)— 设计

> [English](MMKV_FULL_DESIGN.md) · **中文**

本文是把项目从「无 cgo **只读** `Reader`」(见 [DESIGN.zh-CN.md](DESIGN.zh-CN.md))
扩展为 POSIX(Linux + macOS)上**读 + 写**全功能纯 Go MMKV 的方案。下列事实均已对照
MMKV **v2.4.0** Core 源码核实;`file:line` 指向 `MMKV/Core/`。

## 1. 范围(已锁定的决策)

| 维度 | 决策 | 影响 |
|---|---|---|
| 互通 | **完全 C++ 互通** | on-disk 格式 + flock 协议与 C++ 字节一致,Go 与官方 C++ 实例可在同组文件上跨进程共享。验收为**双向**差分。 |
| 特性 | **全部** | CORE(读/写/删/清/trim/sync/过期/namespace/锁)+ 加密&reKey、备份还原、handler、compareBeforeSet。 |
| 并发 | **多写进程** | 多个 Go + C++ 写者同文件。由单写互通本就需要的「排他 flock + `checkLoadData` 对账」直接覆盖;增量主要在测试矩阵。 |
| 读 API | **单一 `MMKV` 类型** | 只读 `Reader` 并入 `MMKV`。读性能靠**模式**保留:单进程禁用 flock(读仍 ~10ns 零拷贝);多进程每读取共享 flock(并发写下正确,与 C++ 一致)。只读模式跳过写机器。 |

仅 POSIX(Linux + macOS);Android/iOS/Windows 不在范围。写永不走 cgo。

## 2. 磁盘格式(已核实的字节配方)

### 2.1 数据文件 `<mmapID>`
```
[0,4)              遗留 Fixed32 actualSize 头 —— version>=3 时留 0(读取方用 meta.actualSize)
[4, 4+actualSize)  KV 日志区
[4+actualSize, …)  空闲区(页对齐尾部),忽略
```
KV 区 = **4 字节 ItemSizeHolder** varint 占位 + 若干 pair。
- ItemSizeHolder = `randomItemSizeHolder(4)` ∈ `[0x200000, 0x10000000)`,故恒编码为 4 字节(`AESCrypt.cpp:33`);读取时 `readInt32()` **丢弃**(`MiniPBCoder.cpp:504` `decodeOneMap`)——语义无关,明文用固定值即可。
- 每个 pair = `writeData(key)` + `writeData(valueBlob)`,`writeData` = `varint(len)+bytes`(`CodedOutputData.cpp:76`)。
- **value-blob 包裹**(关键):标量单层(pair 外层 `writeData` 是唯一长度前缀);**string/bytes 双层**——blob 本身是 `varint(len)+raw`,故 pair 产生外+内两层(`isDataHolder=true`,`MMKV_IO.cpp:907,938`)。
- **负 int32 → 10 字节 varint**(符号扩展到 64bit)(`CodedOutputData.cpp:60`)。经典互通坑。
- 删除 = value 为空的 pair(tombstone);重放时后写覆盖先写。

### 2.2 元文件 `<mmapID>.crc` —— `MMKVMetaInfo`,112 字节小端(`MMKVMetaInfo.hpp`)
| 偏移 | 字段 | 偏移 | 字段 |
|--:|---|--:|---|
| 0 | crcDigest | 32 | lastActualSize |
| 4 | version | 36 | lastCRCDigest |
| 8 | sequence | 40 | _reserved[64] |
| 12 | IV[16] | 104 | flags(bit0=过期) |
| 28 | actualSize | | |

### 2.3 版本规则(已核实)
每次 `writeActualSize` 若版本低于 **4(Flag)** 即升到 4(`MMKV_IO.cpp:611`)。故全新明文文件为 `version=4, sequence=1, flags=0`、IV 全 0、`lastConfirmed=(actualSize,crc)`。CRC32(IEEE)覆盖数据区——加密时覆盖**密文**。

### 2.4 崩溃安全 / 恢复
`lastActualSize`/`lastCRCDigest` 是回滚底线:**仅**在 bump sequence 的 full write-back 时推进(plain append 从不动),故撕裂的 append 回滚到上一份已落盘快照。加载时(`MMKV_IO.cpp` `checkDataValid`→`checkLastConfirmedInfo`):先试当前 `(actualSize,crc)`;否则遗留 `[0,4)` 头;否则 `(lastActualSize,lastCRCDigest)`;再否则恢复(贪婪解码 + full write-back)或丢弃。

## 3. 架构

### 3.1 append vs full write-back(`MMKV_IO.cpp`)
- `needed = varint(keyLen)+key + [innerVarint] + varint(valLen)+val (+4 过期)`;`spaceLeft = fileSize-4-actualSize`。
- `needed < spaceLeft 且 dict 非空` → **append**:在 `4+actualSize` 写入,加密则就地加密,`actualSize+=needed`,**增量** CRC,**只**写 meta 的 crc+actualSize,**不 bump sequence、不 sync**。
- 否则 → **full write-back**:(过滤过期)从偏移 4 重新打包(新 ItemSizeHolder);不够先扩容(**翻倍直到容纳 + 余量**,页对齐 `ftruncate` + 零填充 + munmap/mmap);`actualSize=total`;**整区** CRC;**sequence++**;推进 `lastConfirmed`;加密则轮换 IV;写完整 112 字节 meta;`needSync` 时 `msync(先 data 后 meta)`。

### 3.2 并发(锁)
C++ 纪律(已核实):**先线程锁 → 进程 flock(读共享/写排他)→ 锁内 `checkLoadData`**;flock 打在 `.crc` fd 上用 `flock(2)`(非 fcntl;fcntl 仅 Android)(`InterProcessLock.cpp:94`)。
- **可重入**:Go 无递归 mutex 而 C++ 自我重入。解法:公共方法 `Foo()` 取锁一次;内部 helper `foo()` 一律假定已持锁;持锁路径绝不调用会再取锁的方法。
- **flock 计数**:一个 `fileLock{fd, shared, exclusive}` + 两个 typed 句柄。已持任意锁时共享免费 +1;排他从共享升级(乐观 `LOCK_EX|NB`,失败则放共享再阻塞,失败回退);最后一个排他释放时若仍持共享则降级 `LOCK_SH`。flock 是 per-OFD:进程内 goroutine 由线程锁串行,flock 只跨进程串行——故 `fileLock` 在线程锁**之下**使用,自身无需 mutex(对齐 C++ `FileLock`)。
- **`checkLoadData`**:比对内存 meta 与 mmap 的 `.crc`。`sequence` 变 → 全量 reload;`crc`/`actualSize` 变 → 增量(`partialLoadFromFile`)。
- **每进程每文件单实例**(注册表,对齐 `g_instanceDic`):必须——同文件两实例会各自 flock 同一 OFD(互不排斥)且内存发散。

### 3.3 模块布局
`reader.go`(并入 `MMKV`)· `mmkv.go`(类型、注册表、生命周期、checkLoadData)· `mmkv_io.go`(set/append/fullWriteback/grow/load/recover)· `memfile.go`(RW mmap + truncate/remap + msync)· `flock.go`(计数 flock)· `encode.go`(CodedOutputData)· `coder.go`(map/容器、double-wrap)· `wire.go`/`meta.go`/`crypt.go`/`path.go`(decode/meta/AES-CFB/命名)。

## 4. API 草案(Go)
```go
func InitializeMMKV(rootDir string, opts ...InitOption) error
func MMKVWithID(mmapID string, opts ...Option) (*MMKV, error) // WithMultiProcess/WithEncryption/WithRootPath/WithReadOnly/WithExpectedCapacity
func (m *MMKV) SetBool/SetInt32/.../SetString/SetBytes/SetStringSlice(key, v) error
func (m *MMKV) GetBool/.../GetString/GetStringCopy/GetBytes/GetBytesCopy(key) (T, bool)
func (m *MMKV) RemoveValueForKey/RemoveValuesForKeys(...) error
func (m *MMKV) ClearAll(keepSpace bool) / Trim() / Sync() / Async() error
func (m *MMKV) Count(filterExpire bool) int; AllKeys(...) []string; Contains(key) bool
func (m *MMKV) EnableAutoKeyExpire(sec uint32) error; DisableAutoKeyExpire() error; ReKey(newKey []byte) error
func (m *MMKV) Lock(); Unlock(); TryLock() bool       // 跨进程原子读改写
func NameSpace(rootDir string) NS; BackupOneToDirectory(...) / RestoreOneFromDirectory(...) error
```
零拷贝视图生命周期较只读更紧:数据走 RW mmap,扩容或他进程触发的 reload 都会 remap,故视图**仅在该实例下次调用前**有效(对齐 C++ `MMBufferNoCopy`)。默认 getter 返回视图,需留存用 `…Copy`。

## 5. 阶段与状态
- **Phase A —— 明文核心 + 互通地基** *(进行中)*:append/full-writeback/grow/recovery、单+多进程 flock、注册表、全部 标量/string/bytes/`vector<string>`、remove/clear/trim/sync/count/keys/contains。
  - **已完成**(本分支):`encode.go`(CodedOutputData,含负 int32 10 字节 + double-wrap)、`flock.go`(计数 flock + 升降级)、`memfile.go`(RW mmap + truncate/remap + msync)、`meta.go` 写侧(`lastConfirmed` + `marshal`),以及 Phase-A `Writer`(批量 full-write-back)。**已验证**:encode/flock/memfile 单测(`-race`)、`Writer`→`Reader` 往返,以及 cgo 差分 **`Writer`→官方 C++ 读到相等**(v2.4.0,`harness/write_equiv_test.go`)。
- **Phase B** —— 过期。**Phase C** —— 加密写 + `ReKey`(单点最难:CFB 续流、IV 轮换、原子 re-key)。**Phase D** —— 备份还原 + handler。**Phase E** —— compareBeforeSet(override 写路径;最后做,会改变 append 行为)。

## 6. 验收门禁
1. **双向差分**(7 版本 × 2 架构):C++ 写→Go 读(已有 `TestCgoEqualsPurego`)**且** Go 写→C++ 读(`TestPuregoWriteCgoReads`),覆盖全类型/边界。
2. **多进程 flock 互锁**:{Go 写 × C++ 写 × Go 读},零撕裂、读到最新。
3. **崩溃恢复**:注入撕裂尾巴(截断 / 写一半杀进程、不 sync)→ 回滚到 `lastConfirmed`;Go 与 C++ 恢复一致。
4. `-race`;写吞吐 benchstat vs cgo。

## 7. 风险
1. 可重入锁重构(公共取锁 / 私有假定持锁 纪律必须彻底)。
2. **写侧 mmap 视图生命周期**:扩容 remap 会 munmap 旧映射 → 留存的 `[]byte`/`string` 视图悬空(段错误)。视图跨写/reload 失效,否则用 `…Copy`。
3. flock 互通精确性(升降级/计数)——任何偏差会与 C++ peer 死锁或撕裂。
4. 加密:append 的 CFB 续流状态;`ReKey` 原子性(写一半崩溃可回滚)。
5. 崩溃安全依赖 msync 顺序(先 data 后 meta)+ `lastConfirmed`。
6. 跨版本格式漂移 → **双向** CI 差分 + 版本白名单守护。
7. 跨版本写:Writer 目前写 version=4(MMKV ≥ v1.3.0 可读);按目标版本写最小兼容版本是 Phase A 收尾的一部分。

# mmkv-go 读性能：纯 Go vs cgo（同环境同文件）

> [English](BENCHMARK.md) · **中文**

环境：arm64 Linux 容器（OrbStack），同一个 MMKV 文件，同一进程内分别用官方 cgo 库
和 mmkv-go 读取。命令：`harness/` 下 `go test -bench . -benchmem`。
mmkv-go 为**默认透明刷新**（check-on-read），每次 Get 含变更探针（约 +1ns）。

| 读操作 | 方式 | ns/op | B/op | allocs/op | vs cgo |
|---|---|--:|--:|--:|--:|
| int32 | cgo | 91.0 | 0 | 0 | — |
| int32 | **pure** | **8.9** | 0 | 0 | **10.2× faster** |
| bytes 4KB | cgo copy（不共享） | 776.5 | 4104 | 2 | — |
| bytes 4KB | cgo shared（零拷贝） | 223.6 | 8 | 1 | — |
| bytes 4KB | **pure view（零拷贝）** | **10.4** | 0 | 0 | **75× / 21×** |
| bytes 4KB | pure copy | 576.4 | 4096 | 1 | 1.35× vs cgo copy |
| bytes 6B | cgo copy | 154.3 | 16 | 2 | — |
| bytes 6B | **pure view** | **8.9** | 0 | 0 | **17× faster** |
| string 38B | cgo copy (GetString) | 147.1 | 56 | 2 | — |
| string 38B | cgo view (GetStringBuffer+StringView) | 133.3 | 8 | 1 | — |
| string 38B | **pure view (GetString, unsafe.String)** | **9.7** | 0 | 0 | **14× vs cgo view** |
| string 38B | pure copy (GetStringCopy) | 25.8 | 48 | 1 | 5.7× vs cgo copy |

> 两侧都提供拷贝与零拷贝两种 string 读法。purego `GetString` 是基于 `unsafe.String` 的零拷贝视图
> （9.7ns、0 alloc）；`GetStringCopy` 是安全副本。cgo `GetString` 走 C→Go 拷贝（GoStringN），
> `GetStringBuffer`+`StringView` 是其零拷贝路径——但即便如此（133ns）仍**比 purego 视图慢 ~14×**。
> 差距来自 **cgo 边界税，不是那次拷贝**。

> 这是本地 OrbStack 数字。CI 共享 runner 上噪声更大、绝对值更慢——以**相对比值**为准
> （见 CI job summary 里的 版本 × 架构 报告）。

## 为什么这么快

1. **没有 cgo 边界税**（~65ns/次直接消失）。
2. **parse-once**：`Open()` 时整文件解析进 `map[string][]byte`，之后每次 Get = 无锁快照 + map 查 + 极小解码。
3. **view 零拷贝**：`GetBytes` 返回指向内部缓冲的子切片，不拷贝 → 4KB 和 6B 都是 ~9–10ns 平的、0 alloc。
4. **透明刷新近免费**：check-on-read 探针是无锁内存读，约 +1ns/读；仍比 MMKV C++ 便宜（它每次读还要 flock）。

## 诚实的前提（别误读这张表）

- **这是"稳态重复读"的数字**。`Open()` 的整文件解析成本（O(文件大小)，一次性）没算进 per-Get。
  适用：读多写少、长期持有 Reader 反复读。**不适用**：开-读一次-关 的场景（那时摊销不成立）。
- **view 生命周期**：`GetBytes`/`GetString(底层)` 返回的视图在下次 reload/`Close()` 后失效，
  且不可改写——与 cgo shared 需 `Destroy()` 是同一类约束。需要独立副本（或 live 多 goroutine）用 `GetBytesCopy`。
- **string 仍有 1 次分配**（48B）：Go `string` 类型语义上要复制，无法零拷贝；能用 `[]byte` 就用 view。
- **适用范围**：明文或 AES 加密、可选过期、只读、单写多读。
- **写仍走 cgo**：读端**默认 check-on-read 透明加载**写进程的变更（对齐 MMKV C++），无手动 refresh 接口。

## 并发正确性（单写多读，已 `-race` 验证）

cgo 写进程（`MMKV_MULTI_PROCESS`）狂写 + 纯 Go 读（默认透明刷新，无手动 refresh）：

| reads | CRC错误 | 撕裂(乱码) | 读到最新 |
|--:|--:|--:|--:|
| 472,372 | **0** | **0** | ✓ seq=3000 |

47 万次并发读零撕裂、零 CRC 错误，且读到写进程的最终值。关键是 reload 时取的**共享 flock** 保证一致快照
——（曾实测：去掉该 flock，百万次并发重读会撞到 CRC 错误，即撕裂读被 CRC 拦下；加上后归零）。见
`harness/concurrency_test.go`。另有**多进程**测试（`harness/multiprocess_test.go`）：cgo 写与纯 Go 读
分别跑在独立 OS 进程里，真正验证跨进程 flock 互锁。

## 读写 MMKV 类型（读 + 写）

完整可读写 `MMKV` 类型与 Reader、cgo 一并基准（`harness/bench_test.go`；数字见 CI 性能报告的 “cgo vs MMKV” 表）。它的读取走 mutex + map 查（单进程无 flock），比无锁 Reader 略慢，但仍远超 cgo：

- int32 / bytes-view / string-view 读：约 **5–15× 快于 cgo**，view 路径 0 alloc。

写是新增的（此前只基准了 cgo 写）：

- `SetInt32`（append 快路径）：约 **1.5× 快于 cgo**（无 cgo 边界）。
- `SetBytes` 4 KB：约 **2× 慢于 cgo** —— append 填满一页时，full write-back 把区重建进新缓冲并 msync（MS_SYNC）。增量原地重写为后续优化。

绝对数字以 CI job summary 的 版本 × 架构 表为准。

## 结论

读路径走纯 Go（方案 F）对**读多写少**场景收益巨大：标量 ~10×、零拷贝 bytes 17–75×、多数读 0 alloc；
单写多读下**默认透明刷新**（check-on-read）即并发安全、自动加载变更。代价是格式强耦合（版本白名单 +
CI 差分测试守护）与功能受限（多写进程未覆盖；写入/restore 走 cgo）。

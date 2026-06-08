# 本地用 Docker 跑全流程测试

> [English](LOCAL_DOCKER.md) · **中文**

在本地用一个 Linux 容器复现 CI 的整条流程（功能门禁 + 三方性能），无需在宿主机安装
cmake / g++ / cgo 工具链。容器即 CI runner 的代理。

## 前置

- **Docker**。Apple Silicon 强烈建议 **OrbStack**：`--platform linux/arm64` 是**原生** arm64
  （不走 QEMU），构建快、性能数字真实。
- 镜像用 `golang:1.23-bookworm`（自带 `git` / `g++` / `go`）。
- 容器内还需 `cmake` + `zlib1g-dev`（cmake 构建 MMKV Core；zlib **头文件+库**供 Core 编译与 `-lz` 链接）。

> 说明：`scripts/build_output.sh` 会按 tag **重新编译 Core**，所以必须有 `zlib1g-dev`（头文件）。
> 只有"用预编译 libcore.a"的场景才可用 `ln -sf libz.so.1 .../libz.so` 绕过 apt——重建 Core 不行。

## 一、单个 cell（推荐入门）

跑「某版本 × 某架构」的完整 cell：构建该版本 `output/` → 功能门禁 → 三方性能。

```sh
cd ~/project/repos/mmkv-go
docker run --rm --platform linux/arm64 -v "$PWD":/work -w /work golang:1.23-bookworm bash -c '
  set -e
  apt-get update -qq && apt-get install -y -qq cmake zlib1g-dev
  bash scripts/run_cell.sh v2.4.0 arm64 1s
'
```

- `run_cell.sh <tag> <arch> [benchtime]`；`<arch>` 仅用于给结果文件命名，真实架构由 `--platform` 决定。
- 产物：`./results/v2.4.0-arm64.{cpp,go}.txt`（落在宿主机，已 gitignore）。
- 副作用：会把 MMKV 按 tag clone 到 `./MMKV`（gitignore，可重建）；与 CI 行为一致。

门禁任一失败（`cgo≡purego` 不一致 / 单测 / `-race`）→ 脚本非零退出（CI 据此 fail）。性能只出报告、不 fail。

## 二、全矩阵（多版本，单容器内循环）

一个容器里跑完整版本集（arm64 原生）。复用同一个 `./MMKV` clone，逐 tag checkout：

```sh
cd ~/project/repos/mmkv-go
docker run --rm --platform linux/arm64 -v "$PWD":/work -w /work golang:1.23-bookworm bash -c '
  set -e
  apt-get update -qq && apt-get install -y -qq cmake zlib1g-dev
  for v in v1.2.16 v1.3.16 v2.0.2 v2.1.1 v2.2.4 v2.3.0 v2.4.0; do
    echo "==================== $v ===================="
    bash scripts/run_cell.sh "$v" arm64 1s
  done
'
```

**amd64**：把上面两处 `arm64` 换成 `amd64`、`--platform linux/amd64`。在 Apple Silicon 上 amd64 走
**模拟**——功能门禁仍有效，但**性能数字失真，仅供参考**；真实 amd64 性能以 CI 原生 runner 为准。

## 三、只验功能门禁（最快）

不想跑性能、只想确认某版本 `cgo≡purego`：

```sh
docker run --rm --platform linux/arm64 -v "$PWD":/work -w /work golang:1.23-bookworm bash -c '
  set -e
  apt-get update -qq && apt-get install -y -qq cmake zlib1g-dev
  bash scripts/build_output.sh v2.4.0 /work/MMKV
  cd harness && GOWORK=off GOFLAGS=-mod=mod go test -run TestCgoEqualsPurego -v ./...
'
```

纯库单测（无 cgo，秒级）在宿主机直接跑即可：`go test ./...`。

## 四、看结果 + 聚合

结果在宿主机 `./results/`，用宿主机 python3 聚合成 markdown（cgo vs purego、C++ 基线、4KB 三方对照 + 相对比值）：

```sh
python3 scripts/aggregate.py results
```

（容器内 `golang` 镜像无 python3；要在容器里聚合需先 `apt-get install -y python3`。）

## 五、清理

```sh
rm -rf MMKV results cpp/bench_cpp   # 全部 gitignore，可随时重建
```

## 六、故障排查

- **`cmake: command not found`**：镜像不自带，必须 `apt-get install -y cmake`。
- **链接报 `cannot find -lz` / Core 编译找不到 `zlib.h`**：缺 `zlib1g-dev`。
- **apt 偶发卡住**（网络抖动）：重试该 `docker run`；apt 通常可用，无需特殊处理。
- **首次 `go test`(cgo) 较慢**：第一次会编译 cgo 包 + 链接 `libcore.a/libmmkv.a`，属正常。
- **不想往仓库里 clone MMKV**：把流程放进容器内 scratch 副本（对宿主机零写入）：
  ```sh
  docker run --rm --platform linux/arm64 -v "$PWD":/work golang:1.23-bookworm bash -c '
    apt-get update -qq && apt-get install -y -qq cmake zlib1g-dev
    cp -r /work /scratch && cd /scratch && bash scripts/run_cell.sh v2.4.0 arm64 1s
    cp -r /scratch/results /work/results   # 把结果拷回宿主机
  '
  ```

## 与 CI 的关系

本地 docker ≈ CI runner。CI（`.github/workflows/ci.yml`）在**原生** `ubuntu-latest`(amd64) 与
`ubuntu-24.04-arm`(arm64) 上 `apt-get install cmake g++-12 zlib1g-dev` 后直接 `bash scripts/run_cell.sh`
——runner 一次性，无需 scratch；结果上传 artifact，聚合写入 job summary。本地与 CI 跑的是**同一套脚本**。

> CI 固定用 **g++-12** 而非发行版默认的 g++-13/14：GCC 13 移除了传递性 `<cstdint>` 包含，会编不过老 MMKV
> Core（如 v1.2.16）。g++-12 能编译所有目标版本。本地 `golang:1.23-bookworm` 自带的就是 g++-12，上面命令无需改。

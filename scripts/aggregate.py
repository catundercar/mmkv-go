#!/usr/bin/env python3
"""Aggregate per-cell functional gates + benchmark outputs into one markdown report.

Reads, per cell (<version>-<arch>):
  - results/<cell>.gate.txt  one passed-gate name per line (unit/equiv/crypt+expire/race/multiproc)
  - results/<cell>.cpp.txt   C++ "RESULT\\tcpp\\t<op>\\t<size>\\t<ns>\\t<mb_s>"
  - results/<cell>.go.txt    Go "Benchmark<Name>-N  iters  X ns/op  Y B/op  Z allocs/op"
and emits a functional-gate matrix first, then cross-version/arch performance tables.

Usage: aggregate.py [results_dir]   # default ./results ; prints markdown to stdout
"""
import os
import re
import sys
from collections import defaultdict

RESULTS = sys.argv[1] if len(sys.argv) > 1 else "results"

GO_RE = re.compile(r"^Benchmark(\S+?)-\d+\s+\d+\s+([\d.]+) ns/op(?:\s+(\d+) B/op)?(?:\s+(\d+) allocs/op)?")

# (version, arch) -> impl/name -> ns ; and B/op, allocs
go = defaultdict(dict)         # cell -> benchname -> (ns, bpop, allocs)
cpp = defaultdict(dict)        # cell -> (op,size) -> ns
gates = defaultdict(set)       # cell -> {passed gate names}
cells = set()


def parse_cell(fname):
    base = fname.rsplit(".", 2)[0]      # "v2.4.0-arm64.cpp.txt" -> "v2.4.0-arm64"
    ver, arch = base.rsplit("-", 1)
    return ver, arch


for fn in sorted(os.listdir(RESULTS)) if os.path.isdir(RESULTS) else []:
    path = os.path.join(RESULTS, fn)
    if fn.endswith(".cpp.txt"):
        ver, arch = parse_cell(fn)
        cells.add((ver, arch))
        for line in open(path):
            p = line.rstrip("\n").split("\t")
            if len(p) >= 5 and p[0] == "RESULT" and p[1] == "cpp":
                cpp[(ver, arch)][(p[2], int(p[3]))] = float(p[4])
    elif fn.endswith(".go.txt"):
        ver, arch = parse_cell(fn)
        cells.add((ver, arch))
        for line in open(path):
            m = GO_RE.match(line)
            if m:
                name, ns = m.group(1), float(m.group(2))
                bpop = int(m.group(3)) if m.group(3) else 0
                allocs = int(m.group(4)) if m.group(4) else 0
                go[(ver, arch)][name] = (ns, bpop, allocs)
    elif fn.endswith(".gate.txt"):
        ver, arch = parse_cell(fn)
        cells.add((ver, arch))      # a cell that failed a gate has perf missing; still list it
        for line in open(path):
            name = line.strip()
            if name:
                gates[(ver, arch)].add(name)


def ver_key(v):
    return [int(x) for x in re.findall(r"\d+", v)]


def sorted_cells():
    return sorted(cells, key=lambda c: (c[1], ver_key(c[0])))


def fmt(x):
    return f"{x:,.1f}" if x is not None else "—"


out = []
w = out.append

w("# MMKV 三方测试报告（版本 × 架构）\n")
w(f"覆盖 {len({c[0] for c in cells})} 个版本 × {len({c[1] for c in cells})} 架构 = {len(cells)} cells。")
w("分两部分：**① 功能门禁矩阵**（cgo≡purego / 单测 / -race / 多进程 / 加密过期）+ **② 性能报告**（C++ / cgo / purego）。\n")

archs = sorted({c[1] for c in cells})

# ---- functional-gate matrix (the hard gate; perf is report-only) ----
GATE_COLS = [
    ("unit", "unit/reader"),
    ("equiv", "cgo≡purego"),
    ("crypt+expire", "crypt+expire"),
    ("race", "-race live"),
    ("multiproc", "multi-process"),
]


def is_v24(v):
    return v.startswith("v2.4.")


w("\n## ① 功能门禁矩阵（version × arch）\n")
w("每个 cell 在 CI 中按序跑下列门禁，任一失败即 `set -e` 中止该 cell（job 变红、性能不再产出）。")
w("✓=通过，✗=未通过/未运行，—=该版本无统一 Config binding（加密/过期差分仅在 v2.4.x 跑；CFB 另由 NIST 向量单测覆盖）。\n")
w("| version | arch | " + " | ".join(label for _, label in GATE_COLS) + " |")
w("|---|---|" + "|".join([":-:"] * len(GATE_COLS)) + "|")
for v, arch in sorted_cells():
    passed = gates.get((v, arch), set())
    marks = []
    for key, _ in GATE_COLS:
        if key in passed:
            marks.append("✓")
        elif key == "crypt+expire" and not is_v24(v):
            marks.append("—")
        else:
            marks.append("✗")
    w(f"| {v} | {arch} | " + " | ".join(marks) + " |")
w("\n> ✗ 仅在 cell 失败仍上传了部分产物时可见——正常 PR/push 下任一 ✗ 会让对应 job 直接失败。\n")
w("\n## ② 性能报告\n")

# ---- Go: cgo vs purego, ns/op, per arch ----
GO_ROWS = [
    ("int32 get", "Int32_Cgo", "Int32_Pure"),
    ("bytes 4K get (copy)", "Bytes4K_CgoCopy", "Bytes4K_PureCopy"),
    ("bytes 4K get (shared/view)", "Bytes4K_CgoShared", "Bytes4K_PureView"),
    ("bytes ~small get", "BytesSmall_CgoCopy", "BytesSmall_PureView"),
    ("string get (copy)", "String_CgoCopy", "String_PureCopy"),
    ("string get (view)", "String_CgoShared", "String_PureView"),
]
for arch in archs:
    vers = [c[0] for c in sorted_cells() if c[1] == arch]
    w(f"\n## Go cgo vs purego — {arch}（ns/op；括号=purego 提速×）\n")
    w("| 操作 | impl | " + " | ".join(vers) + " |")
    w("|---|---|" + "|".join(["--:"] * len(vers)) + "|")
    for label, cgo_n, pure_n in GO_ROWS:
        row_c = [f"{label}", "cgo"]
        row_p = ["", "purego"]
        for v in vers:
            g = go.get((v, arch), {})
            c = g.get(cgo_n, (None,))[0]
            p = g.get(pure_n, (None,))[0]
            row_c.append(fmt(c))
            spd = f" ({c / p:.0f}×)" if (c and p) else ""
            row_p.append(fmt(p) + spd)
        w("| " + " | ".join(row_c) + " |")
        w("| " + " | ".join(row_p) + " |")


# ---- read+write MMKV type: cgo vs MMKV, per arch (reads + writes) ----
def render_cmp(title, rows, other_label):
    for arch in archs:
        vers = [c[0] for c in sorted_cells() if c[1] == arch]
        w(f"\n## {title} — {arch}（ns/op；括号=相对 cgo，>1 更快）\n")
        w("| 操作 | impl | " + " | ".join(vers) + " |")
        w("|---|---|" + "|".join(["--:"] * len(vers)) + "|")
        for label, cgo_n, other_n in rows:
            row_c = [label, "cgo"]
            row_o = ["", other_label]
            for v in vers:
                g = go.get((v, arch), {})
                c = g.get(cgo_n, (None,))[0]
                o = g.get(other_n, (None,))[0]
                row_c.append(fmt(c))
                rel = f" ({c / o:.1f}×)" if (c and o) else ""
                row_o.append(fmt(o) + rel)
            w("| " + " | ".join(row_c) + " |")
            w("| " + " | ".join(row_o) + " |")


MMKV_READ_ROWS = [
    ("int32 get", "Int32_Cgo", "Int32_MMKV"),
    ("bytes 4K view", "Bytes4K_CgoShared", "Bytes4K_MMKVView"),
    ("string view", "String_CgoShared", "String_MMKVView"),
]
WRITE_ROWS = [
    ("set int32", "SetInt32_Cgo", "SetInt32_MMKV"),
    ("set bytes 4K", "SetBytes4K_Cgo", "SetBytes4K_MMKV"),
]
render_cmp("Go reads: cgo vs MMKV (read+write type)", MMKV_READ_ROWS, "mmkv")
render_cmp("Go writes: cgo vs MMKV (read+write type)", WRITE_ROWS, "mmkv")

# ---- C++ Core baseline across versions, per arch ----
CPP_OPS = ["get_bytes_copy", "get_bytes_shared", "set_bytes"]
SIZES = [16, 256, 4096, 65536, 1048576]
for arch in archs:
    vers = [c[0] for c in sorted_cells() if c[1] == arch]
    w(f"\n## C++ Core 基线 — {arch}（ns/op）\n")
    w("| 操作 | size | " + " | ".join(vers) + " |")
    w("|---|--:|" + "|".join(["--:"] * len(vers)) + "|")
    for op in CPP_OPS:
        for sz in SIZES:
            cellvals = [cpp.get((v, arch), {}).get((op, sz)) for v in vers]
            if not any(cellvals):
                continue
            w(f"| {op} | {sz} | " + " | ".join(fmt(x) for x in cellvals) + " |")

# ---- clean 3-way @ bytes 4KB get (the one op present in all three harnesses) ----
w("\n## 三方对照 @ bytes 4KB get（ns/op，越低越好）\n")
w("| version | arch | C++ copy | cgo copy | purego copy | C++ shared | cgo shared | purego view |")
w("|---|---|--:|--:|--:|--:|--:|--:|")
for v, arch in sorted_cells():
    c = cpp.get((v, arch), {})
    g = go.get((v, arch), {})
    cols = [
        c.get(("get_bytes_copy", 4096)),
        g.get("Bytes4K_CgoCopy", (None,))[0],
        g.get("Bytes4K_PureCopy", (None,))[0],
        c.get(("get_bytes_shared", 4096)),
        g.get("Bytes4K_CgoShared", (None,))[0],
        g.get("Bytes4K_PureView", (None,))[0],
    ]
    w(f"| {v} | {arch} | " + " | ".join(fmt(x) for x in cols) + " |")

print("\n".join(out))

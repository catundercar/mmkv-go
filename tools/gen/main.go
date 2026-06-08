// Command mmkvgen writes MMKV files with the official cgo library and emits the
// values it reads back as testdata/*.json. These are the differential oracle for
// the pure-Go reader: "what cgo reads" must equal "what puremmkv reads". Run
// inside the arm64 Linux container (needs cgo + the shipped libs):
//
//	go run . <outDir>
//
// Produces, under <outDir>:
//   - plain, plain.crc, expected.json                 (default root)
//   - ns/nsid, ns/nsid.crc, expected_ns.json          (namespace = custom root)
//   - ns/specialCharacter/<md5>, .crc, expected_special.json (special-char id)
package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"

	"tencent.com/mmkv"
)

type entry struct {
	Key  string `json:"key"`
	Type string `json:"type"`
	Val  string `json:"val"`
}

// populate writes a representative set of values to kv and returns what cgo
// reads back for each.
func populate(kv mmkv.MMKV) []entry {
	kv.ClearAll()
	var exp []entry
	addBool := func(k string, v bool) {
		kv.SetBool(v, k)
		exp = append(exp, entry{k, "bool", strconv.FormatBool(kv.GetBool(k))})
	}
	addI32 := func(k string, v int32) {
		kv.SetInt32(v, k)
		exp = append(exp, entry{k, "int32", strconv.FormatInt(int64(kv.GetInt32(k)), 10)})
	}
	addI64 := func(k string, v int64) {
		kv.SetInt64(v, k)
		exp = append(exp, entry{k, "int64", strconv.FormatInt(kv.GetInt64(k), 10)})
	}
	addU32 := func(k string, v uint32) {
		kv.SetUInt32(v, k)
		exp = append(exp, entry{k, "uint32", strconv.FormatUint(uint64(kv.GetUInt32(k)), 10)})
	}
	addU64 := func(k string, v uint64) {
		kv.SetUInt64(v, k)
		exp = append(exp, entry{k, "uint64", strconv.FormatUint(kv.GetUInt64(k), 10)})
	}
	addF32 := func(k string, v float32) {
		kv.SetFloat32(v, k)
		exp = append(exp, entry{k, "float32", strconv.FormatFloat(float64(kv.GetFloat32(k)), 'g', -1, 32)})
	}
	addF64 := func(k string, v float64) {
		kv.SetFloat64(v, k)
		exp = append(exp, entry{k, "float64", strconv.FormatFloat(kv.GetFloat64(k), 'g', -1, 64)})
	}
	addStr := func(k, v string) { kv.SetString(v, k); exp = append(exp, entry{k, "string", kv.GetString(k)}) }
	addBytes := func(k string, v []byte) {
		kv.SetBytes(v, k)
		exp = append(exp, entry{k, "bytes", base64.StdEncoding.EncodeToString(kv.GetBytes(k))})
	}

	addBool("b_true", true)
	addBool("b_false", false)
	addI32("i32_zero", 0)
	addI32("i32_one", 1)
	addI32("i32_neg", -1)
	addI32("i32_min", math.MinInt32)
	addI32("i32_max", math.MaxInt32)
	addI64("i64_min", math.MinInt64)
	addI64("i64_max", math.MaxInt64)
	addU32("u32_max", math.MaxUint32)
	addU64("u64_max", math.MaxUint64)
	addF32("f32_max", math.MaxFloat32)
	addF32("f32_pi", 3.14159)
	addF64("f64_max", math.MaxFloat64)
	addF64("f64_pi", math.Pi)
	addStr("s_ascii", "hello world")
	addStr("s_unicode", "你好,世界🌍 MMKV")
	addBytes("by_small", []byte{0, 1, 2, 3, 255, 254})
	big := make([]byte, 4096)
	for i := range big {
		big[i] = byte(i % 251)
	}
	addBytes("by_4k", big)

	kv.SetInt32(1, "overwrite")
	kv.SetInt32(2, "overwrite")
	exp = append(exp, entry{"overwrite", "int32", strconv.FormatInt(int64(kv.GetInt32("overwrite")), 10)})

	kv.SetInt32(123, "deleted")
	kv.RemoveKey("deleted")
	exp = append(exp, entry{"deleted", "absent", ""})

	kv.Sync(true)
	return exp
}

func writeJSON(path string, exp []entry) {
	f, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(exp); err != nil {
		panic(err)
	}
}

func main() {
	outDir := "../../testdata"
	if len(os.Args) > 1 {
		outDir = os.Args[1]
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		panic(err)
	}

	mmkv.InitializeMMKVWithLogLevel(outDir, mmkv.MMKVLogNone)

	// 1) default root
	writeJSON(outDir+"/expected.json", populate(mmkv.MMKVWithID("plain")))

	// 2) namespace (custom root) + 3) special-character id within it
	nsDir := outDir + "/ns"
	if err := os.MkdirAll(nsDir, 0o755); err != nil {
		panic(err)
	}
	ns := mmkv.GetNameSpace(nsDir)
	writeJSON(outDir+"/expected_ns.json", populate(ns.MMKVWithID("nsid")))
	writeJSON(outDir+"/expected_special.json", populate(ns.MMKVWithID(`with/slash:star*`)))

	fmt.Printf("wrote fixtures to %s {plain, ns/nsid, ns/specialCharacter/*} + expected*.json\n", outDir)
}

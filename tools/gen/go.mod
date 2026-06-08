// Separate cgo module: regenerates testdata fixtures from the official library.
module github.com/catundercar/mmkv-go/tools/gen

go 1.21

require tencent.com/mmkv v0.0.0-00010101000000-000000000000

replace tencent.com/mmkv => ../../MMKV/output/tencent.com/mmkv

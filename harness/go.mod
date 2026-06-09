// Separate module: depends on cgo (tencent.com/mmkv) so it must NOT be part of
// the pure-Go root library module. CI builds MMKV/output per version first.
module github.com/catundercar/mmkv-go/harness

go 1.21

require (
	github.com/catundercar/mmkv-go v0.0.0
	tencent.com/mmkv v0.0.0-00010101000000-000000000000
)

require golang.org/x/sys v0.28.0 // indirect

replace tencent.com/mmkv => ../MMKV/output/tencent.com/mmkv

replace github.com/catundercar/mmkv-go => ..

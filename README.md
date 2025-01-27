# mmkv-go

## 为什么需要 mmkv-go?
golang 使用mmkv时，一般通过cgo调用mmkv的c++库，然而，由于cgo的调用成本，导致mmkv的性能无法得到充分发挥。

性能比较(mmkv-go/test/mmkv_test.go)：
``` 
goos: darwin
goarch: arm64
pkg: catundercar.github.com/mmkv-go/test
cpu: Apple M3 Max
BenchmarkMMKVCGo-16      7007458               167.4 ns/op
BenchmarkMMKVGo-16      196787350                5.956 ns/op
PASS
ok      catundercar.github.com/mmkv-go/test     4.017s
```
可以看到，CGO的调用使性能下降了167/5.956=28 倍左右。



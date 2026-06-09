package harness

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	mmkv "github.com/catundercar/mmkv-go"
)

// The Go cgo binding doesn't expose vector<string>, so these differentials link
// the C++ Core (libcore.a) directly via the cpp/vec_cpp helper — a real
// bidirectional interop check for vector<string>, which the binding can't give.

func buildVecCpp(t *testing.T) string {
	t.Helper()
	libcore := "../MMKV/output/tencent.com/mmkv/lib/libcore.a"
	if _, err := os.Stat(libcore); err != nil {
		t.Skip("libcore.a not built; run scripts/build_output.sh <tag> first")
	}
	cxx := os.Getenv("CXX")
	if cxx == "" {
		if runtime.GOOS == "darwin" {
			cxx = "clang++"
		} else {
			cxx = "g++"
		}
	}
	bin := filepath.Join(t.TempDir(), "vec_cpp")
	args := []string{"-std=c++17", "-O2"}
	if runtime.GOOS == "darwin" {
		args = append(args, "-DFORCE_POSIX") // matches an Apple-built libcore.a
	}
	args = append(args, "-I", "../MMKV/Core", "../cpp/vec_cpp.cpp", libcore, "-lz", "-lpthread", "-o", bin)
	if out, err := exec.Command(cxx, args...).CombinedOutput(); err != nil {
		t.Fatalf("build vec_cpp (%s): %v\n%s", cxx, err, out)
	}
	return bin
}

func equalStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestVectorCppToGo: C++ Core writes a vector<string> → pure-Go reads it. All
// versions (Go reads whatever the Core writes).
func TestVectorCppToGo(t *testing.T) {
	bin := buildVecCpp(t)
	dir := t.TempDir()
	const id = "vec_c2g"
	items := []string{"alpha", "", "你好🌍", "with space", "z"}

	args := append([]string{dir, id, "write"}, items...)
	if out, err := exec.Command(bin, args...).CombinedOutput(); err != nil {
		t.Fatalf("vec_cpp write: %v\n%s", err, out)
	}
	m, err := mmkv.MMKVWithID(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	got, ok := m.GetStringSlice("vec")
	if !ok {
		t.Fatal("GetStringSlice not ok")
	}
	if !equalStrs(got, items) {
		t.Fatalf("C++→Go vector: got %q want %q", got, items)
	}
}

// TestVectorGoToCpp: pure-Go writes a vector<string> → C++ Core reads it. The
// writer emits format v4, so this is gated to MMKV >= v1.3.0 via run_cell.sh.
func TestVectorGoToCpp(t *testing.T) {
	bin := buildVecCpp(t)
	dir := t.TempDir()
	const id = "vec_g2c"
	items := []string{"one", "", "两个", "three three", "4"}

	m, err := mmkv.MMKVWithID(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	m.SetStringSlice("vec", items)
	m.Sync()
	m.Close()

	out, err := exec.Command(bin, dir, id, "read").CombinedOutput()
	if err != nil {
		t.Fatalf("vec_cpp read: %v\n%s", err, out)
	}
	got := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if !equalStrs(got, items) {
		t.Fatalf("Go→C++ vector: got %q want %q", got, items)
	}
}

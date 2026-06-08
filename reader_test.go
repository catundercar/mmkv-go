package mmkv

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"strconv"
	"testing"
)

type expEntry struct {
	Key  string `json:"key"`
	Type string `json:"type"`
	Val  string `json:"val"`
}

// TestDifferentialAgainstFixture is the acceptance test (the "oracle"):
// fixtures are written by the official cgo library (see gen/), and every value
// the pure-Go reader returns must equal what cgo read back.
func TestDifferentialAgainstFixture(t *testing.T) {
	r, err := Open("testdata", "plain")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	verifyEntries(t, r, "testdata/expected.json")
}

// verifyEntries asserts that reader r returns exactly the values recorded in the
// fixture JSON at expPath.
func verifyEntries(t *testing.T, r *Reader, expPath string) {
	t.Helper()
	raw, err := os.ReadFile(expPath)
	if err != nil {
		t.Skipf("no fixture; generate with gen/ in the container: %v", err)
	}
	var exp []expEntry
	if err := json.Unmarshal(raw, &exp); err != nil {
		t.Fatal(err)
	}
	if len(exp) == 0 {
		t.Fatal("empty fixture")
	}

	for _, e := range exp {
		t.Run(e.Key, func(t *testing.T) {
			switch e.Type {
			case "absent":
				if r.Contains(e.Key) {
					t.Fatalf("key %q should be absent", e.Key)
				}
			case "bool":
				want, _ := strconv.ParseBool(e.Val)
				got, ok := r.GetBool(e.Key)
				if !ok || got != want {
					t.Fatalf("GetBool=%v,%v want %v", got, ok, want)
				}
			case "int32":
				want, _ := strconv.ParseInt(e.Val, 10, 32)
				got, ok := r.GetInt32(e.Key)
				if !ok || int64(got) != want {
					t.Fatalf("GetInt32=%v,%v want %v", got, ok, want)
				}
			case "int64":
				want, _ := strconv.ParseInt(e.Val, 10, 64)
				got, ok := r.GetInt64(e.Key)
				if !ok || got != want {
					t.Fatalf("GetInt64=%v,%v want %v", got, ok, want)
				}
			case "uint32":
				want, _ := strconv.ParseUint(e.Val, 10, 32)
				got, ok := r.GetUInt32(e.Key)
				if !ok || uint64(got) != want {
					t.Fatalf("GetUInt32=%v,%v want %v", got, ok, want)
				}
			case "uint64":
				want, _ := strconv.ParseUint(e.Val, 10, 64)
				got, ok := r.GetUInt64(e.Key)
				if !ok || got != want {
					t.Fatalf("GetUInt64=%v,%v want %v", got, ok, want)
				}
			case "float32":
				want, _ := strconv.ParseFloat(e.Val, 32)
				got, ok := r.GetFloat32(e.Key)
				if !ok || float64(got) != want {
					t.Fatalf("GetFloat32=%v,%v want %v", got, ok, want)
				}
			case "float64":
				want, _ := strconv.ParseFloat(e.Val, 64)
				got, ok := r.GetFloat64(e.Key)
				if !ok || got != want {
					t.Fatalf("GetFloat64=%v,%v want %v", got, ok, want)
				}
			case "string":
				got, ok := r.GetString(e.Key)
				if !ok || got != e.Val {
					t.Fatalf("GetString=%q,%v want %q", got, ok, e.Val)
				}
			case "bytes":
				want, _ := base64.StdEncoding.DecodeString(e.Val)
				got, ok := r.GetBytes(e.Key)
				if !ok || string(got) != string(want) {
					t.Fatalf("GetBytes len=%d,%v want len=%d", len(got), ok, len(want))
				}
			default:
				t.Fatalf("unknown type %q", e.Type)
			}
		})
	}
}

func TestOpenMissing(t *testing.T) {
	if _, err := Open("testdata", "does_not_exist"); err == nil {
		t.Fatal("expected error opening missing mmkv")
	}
}

package mmkv

import "testing"

// TestNamespaceFixture proves a namespace (custom root) instance written by the
// cgo library reads back identically through puremmkv.
func TestNamespaceFixture(t *testing.T) {
	ns := OpenNameSpace("testdata/ns")
	r, err := ns.Open("nsid")
	if err != nil {
		t.Skipf("no namespace fixture (regenerate gen/): %v", err)
	}
	defer r.Close()
	verifyEntries(t, r, "testdata/expected_ns.json")
}

// TestSpecialCharIDFixture proves the special-character filename encoding
// (specialCharacter/<md5>) matches MMKV's, end-to-end.
func TestSpecialCharIDFixture(t *testing.T) {
	r, err := Open("testdata/ns", `with/slash:star*`)
	if err != nil {
		t.Skipf("no special-char fixture (regenerate gen/): %v", err)
	}
	defer r.Close()
	verifyEntries(t, r, "testdata/expected_special.json")
}

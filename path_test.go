package mmkv

import (
	"crypto/md5"
	"encoding/hex"
	"path/filepath"
	"testing"
)

func TestEncodeFilePath(t *testing.T) {
	if got := encodeFilePath("normal_id"); got != "normal_id" {
		t.Errorf("normal id: got %q want %q", got, "normal_id")
	}
	for _, id := range []string{"a/b", "c:d", "e*f", `g"h`, "i<j>k", "l|m", `n\o`, "p?q"} {
		sum := md5.Sum([]byte(id))
		want := filepath.Join(specialCharDir, hex.EncodeToString(sum[:]))
		if got := encodeFilePath(id); got != want {
			t.Errorf("special id %q: got %q want %q", id, got, want)
		}
	}
}

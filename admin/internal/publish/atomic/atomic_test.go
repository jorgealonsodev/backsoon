package atomic

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileReplacesAndSetsMode(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f.html")
	os.WriteFile(p, []byte("old"), 0o600)
	if err := WriteFile(p, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	st, _ := os.Stat(p)
	if string(b) != "new" || st.Mode().Perm() != 0o644 {
		t.Fatalf("content %q mode %v", b, st.Mode().Perm())
	}
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Fatalf("temp file left behind: %v", entries)
	}
}

func TestWriteFileMissingDirFails(t *testing.T) {
	if err := WriteFile(filepath.Join(t.TempDir(), "no", "f"), []byte("x"), 0o644); err == nil {
		t.Fatal("expected error")
	}
}

type syncFails struct{ File }

func (syncFails) Sync() error { return errors.New("fsync failed") }

func TestFsyncFailureKeepsOldContent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	os.WriteFile(p, []byte("old"), 0o644)
	w := Writer{CreateTemp: func(d, pat string) (File, error) {
		f, err := OSCreateTemp(d, pat)
		return syncFails{f}, err
	}}
	if err := w.WriteFile(p, []byte("new"), 0o644); err == nil {
		t.Fatal("expected error")
	}
	b, _ := os.ReadFile(p)
	entries, _ := os.ReadDir(filepath.Dir(p))
	if string(b) != "old" || len(entries) != 1 {
		t.Fatalf("content %q entries %v", b, entries)
	}
}

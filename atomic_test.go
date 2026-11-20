package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func readString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func leftovers(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestWriteFileAtomicFlushesFileBeforeRenameAndDirectoryAfter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	var steps []string
	oldFile, oldDir := syncFile, syncDir
	t.Cleanup(func() { syncFile, syncDir = oldFile, oldDir })
	syncFile = func(f *os.File) error {
		steps = append(steps, "file:"+readString(t, path)+":"+readString(t, f.Name()))
		return f.Sync()
	}
	syncDir = func(d string) error {
		steps = append(steps, "dir:"+readString(t, path))
		if d != dir {
			t.Errorf("flushed %s, want %s", d, dir)
		}
		return nil
	}
	if err := writeFileAtomic(path, []byte("new")); err != nil {
		t.Fatal(err)
	}
	want := []string{"file:old:new", "dir:new"}
	if len(steps) != 2 || steps[0] != want[0] || steps[1] != want[1] {
		t.Fatalf("steps %q, want %q", steps, want)
	}
	if got := leftovers(t, dir); len(got) != 1 {
		t.Errorf("files left: %v", got)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, %v", fi.Mode(), err)
	}
}

func TestWriteFileAtomicKeepsTheOldFileWhenTheFlushFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldFile := syncFile
	t.Cleanup(func() { syncFile = oldFile })
	boom := errors.New("disk full")
	syncFile = func(*os.File) error { return boom }
	if err := writeFileAtomic(path, []byte("new")); !errors.Is(err, boom) {
		t.Fatalf("error %v, want %v", err, boom)
	}
	if got := readString(t, path); got != "old" {
		t.Errorf("content %q, want old", got)
	}
	if got := leftovers(t, dir); len(got) != 1 {
		t.Errorf("a temporary file is left: %v", got)
	}
}

func TestWriteFileAtomicReportsADirectoryFlushError(t *testing.T) {
	dir := t.TempDir()
	oldDir := syncDir
	t.Cleanup(func() { syncDir = oldDir })
	boom := errors.New("io error")
	syncDir = func(string) error { return boom }
	if err := writeFileAtomic(filepath.Join(dir, "state.json"), []byte("x")); !errors.Is(err, boom) {
		t.Fatalf("error %v, want %v", err, boom)
	}
}

func TestSyncDirectory(t *testing.T) {
	if err := syncDirectory(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := syncDirectory(filepath.Join(t.TempDir(), "missing")); err == nil && os.PathSeparator == '/' {
		t.Error("a missing directory was flushed")
	}
}

package main

import (
	"os"
	"path/filepath"
	"runtime"
)

// Test seams for the two flushes of writeFileAtomic; only tests reassign them.
var (
	syncFile = func(f *os.File) error { return f.Sync() }
	syncDir  = syncDirectory
)

// writeFileAtomic replaces path with b, mode 0600: it writes a temporary file next to path, flushes it, renames it
// over path and flushes the directory, so after a power loss path holds the old content or the new, never a part.
func writeFileAtomic(path string, b []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = syncFile(f)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		os.Remove(f.Name())
		return err
	}
	return syncDir(dir)
}

func syncDirectory(dir string) error {
	if runtime.GOOS == "windows" {
		return nil // Windows cannot open a directory to flush it
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	return err
}

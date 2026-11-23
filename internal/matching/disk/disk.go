// Package disk provides directory durability for matching state files.
package disk

import (
	"errors"
	"os"
	"path/filepath"
)

// EnsureDir creates dir and syncs any newly added entries in the parents.
func EnsureDir(dir string) error {
	dir = filepath.Clean(dir)
	info, err := os.Stat(dir)
	if err == nil && info.IsDir() {
		return nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parent := filepath.Dir(dir)
	if parent != dir {
		if err := EnsureDir(parent); err != nil {
			return err
		}
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		if info, statErr := os.Stat(dir); statErr != nil || !info.IsDir() {
			return err
		}
	}
	return SyncDir(parent)
}

func SyncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}

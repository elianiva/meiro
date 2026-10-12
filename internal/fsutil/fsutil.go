// Package fsutil holds the file operations the app's private data needs.
package fsutil

import (
	"log"
	"os"
	"path/filepath"
)

// WriteFileAtomic writes data to path so that a crash leaves either the old
// file or the new one, never half of one: it goes to a temporary file in the
// same directory, is synced, and replaces the old file by a rename. The
// directory is created, private to the user, when it is missing.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) (err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(file.Name())
		}
	}()
	if _, err = file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Chmod(perm); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

// RemoveAll deletes a directory the app made, and says so when it cannot: a
// copy of someone's session left in a temporary directory is worth knowing
// about.
func RemoveAll(directory string) {
	if err := os.RemoveAll(directory); err != nil {
		log.Printf("removing %s: %v", directory, err)
	}
}

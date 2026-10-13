// Package atomic writes files so that readers see either the previous content or the complete new
// content, never a partial file: temp file in the same directory, write, fsync, chmod, rename.
package atomic

import (
	"fmt"
	"os"
	"path/filepath"
)

// TempPattern is the os.CreateTemp pattern of the temporary files. It deliberately does not end in
// ".html", so a leftover can never be mistaken for a published page.
const TempPattern = ".backsoon-*.tmp"

// File is the subset of *os.File used while writing, so tests can inject failures.
type File interface {
	Write(p []byte) (int, error)
	Sync() error
	Chmod(mode os.FileMode) error
	Close() error
	Name() string
}

// OSCreateTemp is the default Writer.CreateTemp: a real temporary file.
func OSCreateTemp(dir, pattern string) (File, error) {
	return os.CreateTemp(dir, pattern)
}

// Writer writes files atomically. The zero value uses the real file system.
type Writer struct {
	// CreateTemp creates the temporary file; nil means OSCreateTemp.
	CreateTemp func(dir, pattern string) (File, error)
}

// WriteFile atomically replaces path with data and gives it mode perm. The directory must exist.
// On any error the temporary file is removed and path is left untouched.
func (w Writer) WriteFile(path string, data []byte, perm os.FileMode) (err error) {
	create := w.CreateTemp
	if create == nil {
		create = OSCreateTemp
	}
	dir := filepath.Dir(path)
	tmp, err := create(dir, TempPattern)
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", path, err)
	}
	if err = tmp.Chmod(perm); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("rename to %s: %w", path, err)
	}
	syncDir(dir)
	return nil
}

// WriteFile is Writer{}.WriteFile.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	return Writer{}.WriteFile(path, data, perm)
}

// syncDir makes the rename durable. Failure is ignored: the file is already in place.
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
}

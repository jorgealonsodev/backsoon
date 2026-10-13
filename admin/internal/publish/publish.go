// Package publish writes the pages backsoon (nginx) serves: default.html, host/<domain>.html and
// target/<service>_<port>.html. Every file is replaced atomically (RF-20) and files of targets
// that no longer exist are removed.
package publish

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jorgealonsodev/backsoon/admin/internal/pages"
	"github.com/jorgealonsodev/backsoon/admin/internal/publish/atomic"
)

const (
	fileMode = 0o644 // readable by nginx's unprivileged worker
	dirMode  = 0o755
)

// Publisher returns the hook for pages.Options.Publish that publishes a snapshot into pagesDir.
func Publisher(pagesDir string) func(pages.Snapshot) error {
	return func(snap pages.Snapshot) error { return Publish(snap, pagesDir) }
}

// Publish makes pagesDir match snap: it writes default.html and one file per target, then removes
// every *.html file in host/ and target/ that does not belong to a target. Nothing else in pagesDir
// is touched. Publishing is idempotent, so running it at startup repairs an inconsistent volume.
//
// Stale files are only removed after all writes succeeded, so a failed publication never takes a
// working page away.
func Publish(snap pages.Snapshot, pagesDir string) error {
	return publish(atomic.Writer{}, snap, pagesDir)
}

func publish(w atomic.Writer, snap pages.Snapshot, pagesDir string) error {
	var defaultHTML []byte
	haveDefault := false
	for _, p := range snap.Pages {
		if p.Site.ID == pages.DefaultID {
			defaultHTML, haveDefault = p.HTML, true
		}
	}
	if !haveDefault {
		return errors.New("snapshot has no default page")
	}
	if err := os.MkdirAll(pagesDir, dirMode); err != nil {
		return fmt.Errorf("create %s: %w", pagesDir, err)
	}
	for _, sub := range []string{"host", "target"} {
		if err := ensureDir(filepath.Join(pagesDir, sub)); err != nil {
			return err
		}
	}

	wanted := map[string]bool{}
	if err := w.WriteFile(filepath.Join(pagesDir, "default.html"), defaultHTML, fileMode); err != nil {
		return err
	}
	for _, p := range snap.Pages {
		for _, t := range p.Site.Targets {
			name := t.FileName()
			wanted[name] = true
			if err := w.WriteFile(filepath.Join(pagesDir, filepath.FromSlash(name)), p.HTML, fileMode); err != nil {
				return err
			}
		}
	}
	for _, sub := range []string{"host", "target"} {
		if err := removeStale(pagesDir, sub, wanted); err != nil {
			return err
		}
	}
	return nil
}

// ensureDir creates dir with mode 0755 regardless of the process umask.
func ensureDir(dir string) error {
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	if err := os.Chmod(dir, dirMode); err != nil {
		return fmt.Errorf("chmod %s: %w", dir, err)
	}
	return nil
}

// removeStale deletes regular *.html files of pagesDir/sub that are not in wanted.
func removeStale(pagesDir, sub string, wanted map[string]bool) error {
	entries, err := os.ReadDir(filepath.Join(pagesDir, sub))
	if err != nil {
		return fmt.Errorf("list %s: %w", sub, err)
	}
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() || !strings.HasSuffix(name, ".html") || wanted[sub+"/"+name] {
			continue
		}
		if err := os.Remove(filepath.Join(pagesDir, sub, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove stale %s/%s: %w", sub, name, err)
		}
	}
	return nil
}

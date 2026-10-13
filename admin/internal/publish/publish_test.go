package publish

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jorgealonsodev/backsoon/admin/internal/pages"
	"github.com/jorgealonsodev/backsoon/admin/internal/publish/atomic"
)

func snapshot(defaultHTML string, sites ...pages.PageContent) pages.Snapshot {
	return pages.Snapshot{Pages: append([]pages.PageContent{{
		Site: pages.Site{ID: pages.DefaultID, Name: "Default"}, HTML: []byte(defaultHTML),
	}}, sites...)}
}

func site(id, html string, targets ...pages.Target) pages.PageContent {
	return pages.PageContent{Site: pages.Site{ID: id, Name: id, Targets: targets}, HTML: []byte(html)}
}

func host(v string) pages.Target { return pages.Target{Type: pages.TargetHost, Value: v} }
func svc(v string) pages.Target  { return pages.Target{Type: pages.TargetService, Value: v} }

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func listAll(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(root, p)
			out = append(out, rel)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func TestPublishWritesAllFiles(t *testing.T) {
	dir := t.TempDir()
	snap := snapshot("DEFAULT",
		site("a1", "PAGE A", host("app.test"), svc("app:8080")),
		site("b2", "PAGE B", host("other.test")),
	)
	if err := Publish(snap, dir); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"default.html":         "DEFAULT",
		"host/app.test.html":   "PAGE A",
		"target/app_8080.html": "PAGE A",
		"host/other.test.html": "PAGE B",
	}
	for f, c := range want {
		if got := readFile(t, filepath.Join(dir, f)); got != c {
			t.Errorf("%s = %q, want %q", f, got, c)
		}
		st, _ := os.Stat(filepath.Join(dir, f))
		if st.Mode().Perm() != 0o644 {
			t.Errorf("%s mode %v, want 0644", f, st.Mode().Perm())
		}
	}
	for _, d := range []string{"host", "target"} {
		st, err := os.Stat(filepath.Join(dir, d))
		if err != nil || st.Mode().Perm() != 0o755 {
			t.Errorf("%s dir mode: %v %v", d, st, err)
		}
	}
	if got := listAll(t, dir); len(got) != len(want) {
		t.Errorf("unexpected files: %v", got)
	}
}

func TestPublishRemovesStaleTargetsOnly(t *testing.T) {
	dir := t.TempDir()
	if err := Publish(snapshot("D", site("a", "A", host("gone.test"), svc("old:1"), host("stay.test"))), dir); err != nil {
		t.Fatal(err)
	}
	// Files that are not ours must survive.
	os.WriteFile(filepath.Join(dir, "README.txt"), []byte("keep"), 0o644)
	os.WriteFile(filepath.Join(dir, "host", "notes.txt"), []byte("keep"), 0o644)
	os.MkdirAll(filepath.Join(dir, "host", "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "host", "sub", "x.html"), []byte("keep"), 0o644)

	if err := Publish(snapshot("D2", site("a", "A2", host("stay.test"))), dir); err != nil {
		t.Fatal(err)
	}
	got := listAll(t, dir)
	want := []string{"README.txt", "default.html", "host/notes.txt", "host/stay.test.html", "host/sub/x.html"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("files = %v, want %v", got, want)
	}
	if readFile(t, filepath.Join(dir, "host/stay.test.html")) != "A2" || readFile(t, filepath.Join(dir, "default.html")) != "D2" {
		t.Fatal("content not refreshed")
	}
}

// Deleting a page frees its targets: the files disappear so nginx falls back to default.html.
func TestPublishAfterPageDeletion(t *testing.T) {
	dir := t.TempDir()
	Publish(snapshot("D", site("a", "A", host("x.test"))), dir)
	Publish(snapshot("D"), dir)
	if _, err := os.Stat(filepath.Join(dir, "host", "x.test.html")); !os.IsNotExist(err) {
		t.Fatal("stale file must be removed")
	}
}

func TestPublishRecoversInconsistentVolume(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "host"), 0o755)
	os.WriteFile(filepath.Join(dir, "host", "corrupt.test.html"), []byte("half"), 0o600)
	os.WriteFile(filepath.Join(dir, "host", "orphan.test.html"), []byte("orphan"), 0o644)
	if err := Publish(snapshot("D", site("a", "FULL", host("corrupt.test"))), dir); err != nil {
		t.Fatal(err)
	}
	if readFile(t, filepath.Join(dir, "host", "corrupt.test.html")) != "FULL" {
		t.Fatal("corrupt file must be replaced")
	}
	if _, err := os.Stat(filepath.Join(dir, "host", "orphan.test.html")); !os.IsNotExist(err) {
		t.Fatal("orphan must be removed")
	}
	st, _ := os.Stat(filepath.Join(dir, "host", "corrupt.test.html"))
	if st.Mode().Perm() != 0o644 {
		t.Fatalf("mode = %v", st.Mode().Perm())
	}
}

// failingFile writes the first half of the data and then fails, simulating a crash/disk error mid-write.
type failingFile struct {
	atomic.File
	failAfter int
}

func (f *failingFile) Write(p []byte) (int, error) {
	n := f.failAfter
	if n > len(p) {
		n = len(p)
	}
	if n > 0 {
		f.File.Write(p[:n])
	}
	return n, errors.New("injected: disk full mid-write")
}

// CA-22: a failure in the middle of a write never leaves a partial file at a published path.
func TestPublishFailureMidWriteLeavesNoPartialFile(t *testing.T) {
	dir := t.TempDir()
	good := snapshot("OLD DEFAULT", site("a", "OLD PAGE", host("app.test")))
	if err := Publish(good, dir); err != nil {
		t.Fatal(err)
	}
	before := listAll(t, dir)

	w := atomic.Writer{CreateTemp: func(d, pattern string) (atomic.File, error) {
		f, err := atomic.OSCreateTemp(d, pattern)
		if err != nil {
			return nil, err
		}
		return &failingFile{File: f, failAfter: 3}, nil
	}}
	next := snapshot("NEW DEFAULT", site("a", "NEW PAGE CONTENT THAT IS LONG", host("app.test"), host("fresh.test")))
	err := publish(w, next, dir)
	if err == nil || !strings.Contains(err.Error(), "injected") {
		t.Fatalf("expected the injected failure, got %v", err)
	}

	if got := readFile(t, filepath.Join(dir, "default.html")); got != "OLD DEFAULT" {
		t.Errorf("default.html changed: %q", got)
	}
	if got := readFile(t, filepath.Join(dir, "host", "app.test.html")); got != "OLD PAGE" {
		t.Errorf("host/app.test.html changed: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "host", "fresh.test.html")); !os.IsNotExist(err) {
		t.Error("a never-completed file must not exist at its published path")
	}
	after := listAll(t, dir)
	if strings.Join(before, ",") != strings.Join(after, ",") {
		t.Errorf("leftovers (temp files must be cleaned up): before=%v after=%v", before, after)
	}
}

func TestPublishFailureDoesNotRemoveStaleFilesEarly(t *testing.T) {
	dir := t.TempDir()
	Publish(snapshot("D", site("a", "A", host("old.test"))), dir)
	w := atomic.Writer{CreateTemp: func(d, p string) (atomic.File, error) {
		f, _ := atomic.OSCreateTemp(d, p)
		return &failingFile{File: f}, nil
	}}
	if err := publish(w, snapshot("D", site("a", "A", host("new.test"))), dir); err == nil {
		t.Fatal("expected failure")
	}
	if _, err := os.Stat(filepath.Join(dir, "host", "old.test.html")); err != nil {
		t.Fatal("previous file must stay until the new publication completes")
	}
}

func TestPublishIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	snap := snapshot("D", site("a", "A", host("x.test"), svc("s:1")))
	for range 3 {
		if err := Publish(snap, dir); err != nil {
			t.Fatal(err)
		}
	}
	if got := listAll(t, dir); len(got) != 3 {
		t.Fatalf("%v", got)
	}
}

func TestPublishRequiresDefault(t *testing.T) {
	if err := Publish(pages.Snapshot{}, t.TempDir()); err == nil {
		t.Fatal("snapshot without the default page must be rejected")
	}
}

// Store + publisher together: what T3 wires up.
func TestStoreIntegration(t *testing.T) {
	d := t.TempDir()
	pagesDir := filepath.Join(d, "pages")
	s, err := pages.Open(pages.Options{
		MetaDir: filepath.Join(d, "meta"), PagesDir: pagesDir,
		TemplatePath: "../../../templates/maintenance.html", Refresh: 10,
		Publish: Publisher(pagesDir),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readFile(t, filepath.Join(pagesDir, "default.html")), "Este servicio") {
		t.Fatal("startup must publish default.html")
	}
	site, err := s.Create("Nueva", []string{"nueva.test"}, []byte("<p>nueva</p>"), false)
	if err != nil {
		t.Fatal(err)
	}
	if readFile(t, filepath.Join(pagesDir, "host", "nueva.test.html")) != "<p>nueva</p>" {
		t.Fatal("create not published")
	}
	if err := s.Delete(site.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(pagesDir, "host", "nueva.test.html")); !os.IsNotExist(err) {
		t.Fatal("delete not published")
	}
}

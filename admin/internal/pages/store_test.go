package pages

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type env struct {
	meta, pages string
	published   []Snapshot
	failPublish error
}

func (e *env) open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(Options{
		MetaDir: e.meta, PagesDir: e.pages, TemplatePath: templatePath, Refresh: 10,
		Publish: func(sn Snapshot) error {
			if e.failPublish != nil {
				return e.failPublish
			}
			e.published = append(e.published, sn)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func newEnv(t *testing.T) *env {
	d := t.TempDir()
	return &env{meta: filepath.Join(d, "meta"), pages: filepath.Join(d, "pages")}
}

const page = "<!doctype html><title>x</title><p>hi</p>"

func ids(sites []Site) []string {
	var out []string
	for _, s := range sites {
		out = append(out, s.ID)
	}
	return out
}

func TestBootstrapRendersDefaultFromTemplate(t *testing.T) {
	e := newEnv(t)
	s := e.open(t)
	list := s.List()
	if len(list) != 1 || list[0].ID != DefaultID || len(list[0].Targets) != 0 {
		t.Fatalf("unexpected bootstrap list: %+v", list)
	}
	html, err := s.HTML(DefaultID)
	if err != nil || !strings.Contains(string(html), "Este servicio") {
		t.Fatalf("default html: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.meta, "sites.json")); err != nil {
		t.Fatal("sites.json must be written")
	}
	if _, err := os.Stat(filepath.Join(e.meta, "pages", "default.html")); err != nil {
		t.Fatal("meta page must be written")
	}
	if len(e.published) != 1 {
		t.Fatalf("startup must publish once, got %d", len(e.published))
	}
}

func TestBootstrapKeepsSeededDefault(t *testing.T) {
	e := newEnv(t)
	if err := os.MkdirAll(e.pages, 0o755); err != nil {
		t.Fatal(err)
	}
	seeded := "<html>seeded by backsoon-init</html>"
	if err := os.WriteFile(filepath.Join(e.pages, "default.html"), []byte(seeded), 0o644); err != nil {
		t.Fatal(err)
	}
	s := e.open(t)
	html, _ := s.HTML(DefaultID)
	if string(html) != seeded {
		t.Fatalf("seeded default.html must be preserved, got %q", html)
	}
}

func TestReopenPreservesState(t *testing.T) {
	e := newEnv(t)
	s := e.open(t)
	site, err := s.Create("Shop", []string{"Shop.Test", "shop:8080"}, []byte(page), false)
	if err != nil {
		t.Fatal(err)
	}
	s2 := e.open(t)
	got, err := s2.Get(site.ID)
	if err != nil || got.Name != "Shop" || len(got.Targets) != 2 || !got.UpdatedAt.Equal(site.UpdatedAt) {
		t.Fatalf("state lost on reopen: %+v %v", got, err)
	}
	html, _ := s2.HTML(site.ID)
	if string(html) != page {
		t.Fatal("html lost")
	}
	// stored JSON follows the PRD shape.
	raw, _ := os.ReadFile(filepath.Join(e.meta, "sites.json"))
	var doc struct {
		Sites []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Targets []struct {
				Type  string `json:"type"`
				Value string `json:"value"`
			} `json:"targets"`
			UpdatedAt string `json:"updated_at"`
		} `json:"sites"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil || len(doc.Sites) != 2 {
		t.Fatalf("sites.json shape: %v %s", err, raw)
	}
}

func TestOpenRejectsCorruptSitesJSON(t *testing.T) {
	e := newEnv(t)
	if err := os.MkdirAll(e.meta, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.meta, "sites.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(Options{MetaDir: e.meta, PagesDir: e.pages, TemplatePath: templatePath, Refresh: 10}); err == nil {
		t.Fatal("corrupt sites.json must not be silently replaced")
	}
}

func TestCreateListOrderAndFields(t *testing.T) {
	e := newEnv(t)
	s := e.open(t)
	b, _ := s.Create("beta", []string{"b.test"}, []byte(page), false)
	a, _ := s.Create("Alpha", []string{"a.test"}, []byte(page), false)
	if len(a.ID) != 16 || len(b.ID) != 16 || a.ID == b.ID || a.ID == DefaultID {
		t.Fatalf("ids must be random filename-safe hex: %q %q", a.ID, b.ID)
	}
	if strings.Trim(a.ID, "0123456789abcdef") != "" {
		t.Fatalf("id not hex: %q", a.ID)
	}
	got := ids(s.List())
	if len(got) != 3 || got[0] != DefaultID || got[1] != a.ID || got[2] != b.ID {
		t.Fatalf("order must be default, then by name: %v", got)
	}
	if a.UpdatedAt.IsZero() {
		t.Fatal("updated_at must be set")
	}
}

func TestCreateValidation(t *testing.T) {
	s := newEnv(t).open(t)
	if _, err := s.Create("x", nil, []byte(page), false); !errors.Is(err, ErrNoTargets) {
		t.Errorf("no targets: %v", err)
	}
	if _, err := s.Create("x", []string{"a b"}, []byte(page), false); !errors.Is(err, ErrInvalidTarget) {
		t.Errorf("bad target: %v", err)
	}
	if _, err := s.Create("  ", []string{"a.test"}, []byte(page), false); !errors.Is(err, ErrInvalidName) {
		t.Errorf("blank name: %v", err)
	}
	if _, err := s.Create("x", []string{"a.test"}, []byte("\xff"), false); !errors.Is(err, ErrNotUTF8) {
		t.Errorf("bad html: %v", err)
	}
	if len(s.List()) != 1 {
		t.Fatal("failed creates must leave no trace")
	}
}

func TestDuplicateTargetsWithinOnePageCollapse(t *testing.T) {
	s := newEnv(t).open(t)
	site, err := s.Create("x", []string{"A.test", "a.test"}, []byte(page), false)
	if err != nil || len(site.Targets) != 1 {
		t.Fatalf("%+v %v", site, err)
	}
}

// CA-20: an already assigned target asks for confirmation and, once confirmed, moves.
func TestTargetConflictAndMove(t *testing.T) {
	e := newEnv(t)
	s := e.open(t)
	one, _ := s.Create("One", []string{"app.test", "keep.test"}, []byte(page), false)

	_, err := s.Create("Two", []string{"APP.test"}, []byte(page), false)
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("expected ConflictError, got %v", err)
	}
	if len(ce.Conflicts) != 1 || ce.Conflicts[0].OwnerID != one.ID || ce.Conflicts[0].OwnerName != "One" ||
		ce.Conflicts[0].Target.Value != "app.test" || !strings.Contains(err.Error(), "One") {
		t.Fatalf("conflict does not name the owner: %+v / %v", ce, err)
	}
	if len(s.List()) != 2 {
		t.Fatal("rejected create must not persist")
	}

	two, err := s.Create("Two", []string{"app.test"}, []byte(page), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(two.Targets) != 1 {
		t.Fatalf("%+v", two)
	}
	prev, _ := s.Get(one.ID)
	if len(prev.Targets) != 1 || prev.Targets[0].Value != "keep.test" {
		t.Fatalf("target must be removed from previous owner: %+v", prev)
	}
}

// Moving the last target of a page is allowed: the page stays (listed, editable) with no targets
// until the user assigns new ones.
func TestMoveLastTargetLeavesPreviousOwnerEmpty(t *testing.T) {
	s := newEnv(t).open(t)
	one, _ := s.Create("One", []string{"app.test"}, []byte(page), false)
	if _, err := s.Create("Two", []string{"app.test"}, []byte(page), true); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(one.ID)
	if err != nil || len(got.Targets) != 0 {
		t.Fatalf("previous owner must keep existing with no targets: %+v %v", got, err)
	}
	name := "Renamed"
	if _, err := s.Update(one.ID, Update{Name: &name}, false); err != nil {
		t.Fatalf("a target-less page can still be renamed: %v", err)
	}
}

func TestUpdate(t *testing.T) {
	e := newEnv(t)
	s := e.open(t)
	site, _ := s.Create("Old", []string{"a.test"}, []byte(page), false)
	name := "New"
	targets := []string{"b.test", "svc:81"}
	html := []byte("<p>new</p>")
	got, err := s.Update(site.ID, Update{Name: &name, Targets: &targets, HTML: html}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "New" || len(got.Targets) != 2 || got.Targets[0].Value != "b.test" {
		t.Fatalf("%+v", got)
	}
	if h, _ := s.HTML(site.ID); string(h) != "<p>new</p>" {
		t.Fatal("html not replaced")
	}
	// keeping its own targets is not a conflict
	same := []string{"b.test"}
	if _, err := s.Update(site.ID, Update{Targets: &same}, false); err != nil {
		t.Fatalf("own targets must not conflict: %v", err)
	}
	empty := []string{}
	if _, err := s.Update(site.ID, Update{Targets: &empty}, false); !errors.Is(err, ErrNoTargets) {
		t.Fatalf("empty targets: %v", err)
	}
	if _, err := s.Update("nope", Update{Name: &name}, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
	bad := []byte("\xff")
	if _, err := s.Update(site.ID, Update{HTML: bad}, false); !errors.Is(err, ErrNotUTF8) {
		t.Fatalf("bad html: %v", err)
	}
}

func TestUpdateTargetConflict(t *testing.T) {
	s := newEnv(t).open(t)
	one, _ := s.Create("One", []string{"one.test"}, []byte(page), false)
	two, _ := s.Create("Two", []string{"two.test"}, []byte(page), false)
	steal := []string{"one.test", "two.test"}
	_, err := s.Update(two.ID, Update{Targets: &steal}, false)
	var ce *ConflictError
	if !errors.As(err, &ce) || ce.Conflicts[0].OwnerID != one.ID {
		t.Fatalf("expected conflict with One: %v", err)
	}
}

func TestDefaultPageRules(t *testing.T) {
	s := newEnv(t).open(t)
	if err := s.Delete(DefaultID); !errors.Is(err, ErrDefaultImmutable) {
		t.Fatalf("default must not be deletable: %v", err)
	}
	targets := []string{"a.test"}
	if _, err := s.Update(DefaultID, Update{Targets: &targets}, false); !errors.Is(err, ErrDefaultImmutable) {
		t.Fatalf("default owns no targets: %v", err)
	}
	if _, err := s.Update(DefaultID, Update{HTML: []byte("<p>mine</p>")}, false); err != nil {
		t.Fatalf("default html can be replaced: %v", err)
	}
}

func TestDelete(t *testing.T) {
	e := newEnv(t)
	s := e.open(t)
	site, _ := s.Create("x", []string{"a.test"}, []byte(page), false)
	if err := s.Delete(site.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(site.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("still there")
	}
	if _, err := os.Stat(filepath.Join(e.meta, "pages", site.ID+".html")); !os.IsNotExist(err) {
		t.Fatal("meta html must be removed")
	}
	if err := s.Delete(site.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	// The freed target is assignable again without confirmation.
	if _, err := s.Create("y", []string{"a.test"}, []byte(page), false); err != nil {
		t.Fatalf("freed target must be reusable: %v", err)
	}
	last := e.published[len(e.published)-1]
	if len(last.Pages) != 2 {
		t.Fatalf("snapshot must contain default + y: %d", len(last.Pages))
	}
}

func TestPublishHookRunsAfterEveryChangeAndFailureIsTyped(t *testing.T) {
	e := newEnv(t)
	s := e.open(t)
	before := len(e.published)
	site, _ := s.Create("x", []string{"a.test"}, []byte(page), false)
	nm := "y"
	s.Update(site.ID, Update{Name: &nm}, false)
	s.Delete(site.ID)
	if len(e.published)-before != 3 {
		t.Fatalf("expected 3 publications, got %d", len(e.published)-before)
	}
	snap := e.published[before]
	if snap.Pages[0].Site.ID != DefaultID || string(snap.Pages[1].HTML) != page {
		t.Fatalf("snapshot content wrong: %+v", snap)
	}

	e.failPublish = errors.New("disk full")
	got, err := s.Create("z", []string{"z.test"}, []byte(page), false)
	var pe *PublishError
	if !errors.As(err, &pe) {
		t.Fatalf("expected PublishError: %v", err)
	}
	if got.ID == "" {
		t.Fatal("state was saved, the site must be returned alongside the error")
	}
	if _, err := s.Get(got.ID); err != nil {
		t.Fatal("state must stay persisted when only publishing failed")
	}
}

func TestMetaWritesAreAtomic(t *testing.T) {
	e := newEnv(t)
	s := e.open(t)
	s.Create("x", []string{"a.test"}, []byte(page), false)
	entries, _ := os.ReadDir(e.meta)
	for _, en := range entries {
		if strings.HasPrefix(en.Name(), ".") {
			t.Errorf("leftover temp file %s", en.Name())
		}
	}
}

func TestStoreRender(t *testing.T) {
	s := newEnv(t).open(t)
	out, err := s.Render(TemplateParams{AppName: "Shop", Message: "soon"})
	if err != nil || !strings.Contains(string(out), `content="10"`) {
		t.Fatalf("store refresh default: %v", err)
	}
}

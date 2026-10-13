package web

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jorgealonsodev/backsoon/admin/internal/pages"
)

func templateFields(name, destinations string) map[string]string {
	return map[string]string{
		"name": name, "destinations": destinations, "source": "template",
		"app_name": name, "message": "We will be right back.", "lang": "es", "icon": "",
	}
}

func targetValues(site pages.Site) []string {
	var out []string
	for _, t := range site.Targets {
		out = append(out, t.Value)
	}
	return out
}

func siteByName(t *testing.T, hn *harness, name string) pages.Site {
	t.Helper()
	for _, s := range hn.store.List() {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no page named %q in %v", name, hn.store.List())
	return pages.Site{}
}

// CA-19: create from the template.
func TestCreateFromTemplate(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	f := templateFields("Acme <b>", "Nueva.Test")
	f["message"] = `Back at <i>noon</i> & "soon"`
	w := hn.postMultipart("/pages", s, f, nil)
	wantStatus(t, w, http.StatusSeeOther)
	loc := w.Header().Get("Location")
	site := siteByName(t, hn, "Acme <b>")
	if !strings.HasPrefix(loc, "/?notice=created") || !strings.Contains(loc, "focus="+site.ID) {
		t.Fatalf("Location = %q", loc)
	}
	if got := targetValues(site); len(got) != 1 || got[0] != "nueva.test" {
		t.Fatalf("targets = %v", got)
	}
	published := readFile(t, filepath.Join(hn.pagesDir, "host", "nueva.test.html"))
	mustContainRaw(t, published, "Acme &lt;b&gt;", "Back at &lt;i&gt;noon&lt;/i&gt; &amp; &#34;soon&#34;")
	mustContain(t, hn.get(loc, s).Body.String(), "Page created", "Acme &lt;b&gt;")
	mustContainRaw(t, hn.logs.String(), "page created", "id="+site.ID, "nueva.test")
}

func TestCreateFromTemplateValidationKeepsInput(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	f := templateFields("Acme", "app.test")
	f["app_name"] = ""
	f["message"] = "Keep this text"
	w := hn.postMultipart("/pages", s, f, nil)
	wantStatus(t, w, http.StatusUnprocessableEntity)
	body := w.Body.String()
	mustContain(t, body, "application name is required", `aria-invalid="true"`, "Keep this text", `value="Acme"`, "app.test",
		`aria-describedby="app_name-error`)
	if len(hn.store.List()) != 1 {
		t.Fatal("nothing must be created")
	}
}

func TestCreateRequiresNameAndDestination(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	w := hn.postMultipart("/pages", s, templateFields("", "app.test"), nil)
	wantStatus(t, w, http.StatusUnprocessableEntity)
	mustContain(t, w.Body.String(), "name is required")

	w = hn.postMultipart("/pages", s, templateFields("Acme", "  \n "), nil)
	wantStatus(t, w, http.StatusUnprocessableEntity)
	mustContain(t, w.Body.String(), "at least one destination")
	if len(hn.store.List()) != 1 {
		t.Fatal("nothing must be created")
	}
}

func TestCreateByUploadingHTML(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	w := hn.postMultipart("/pages", s, map[string]string{"name": "Uploaded", "destinations": "up.test, svc:81"},
		&upload{name: "page.html", data: []byte(validPage)})
	wantStatus(t, w, http.StatusSeeOther)
	site := siteByName(t, hn, "Uploaded")
	if got := targetValues(site); len(got) != 2 || got[0] != "up.test" || got[1] != "svc:81" {
		t.Fatalf("targets = %v", got)
	}
	if got := readFile(t, filepath.Join(hn.pagesDir, "target", "svc_81.html")); got != validPage {
		t.Fatalf("published = %q", got)
	}
}

func TestUploadTooLargeIsACleanMessage(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	for name, size := range map[string]int{"600 KB": 600 * 1024, "3 MB": 3 << 20} {
		t.Run(name, func(t *testing.T) {
			big := append([]byte("<!doctype html><p>"), bytes.Repeat([]byte("a"), size)...)
			w := hn.postMultipart("/pages", s, map[string]string{"name": "Big", "destinations": "big.test"},
				&upload{name: "big.html", data: big})
			wantStatus(t, w, http.StatusRequestEntityTooLarge)
			mustContain(t, w.Body.String(), "larger than 512 KB", "Remove", `data-view="new"`)
			if len(hn.store.List()) != 1 {
				t.Fatal("nothing must be created")
			}
		})
	}
}

func TestUploadNotUTF8IsRejected(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	w := hn.postMultipart("/pages", s, map[string]string{"name": "Latin", "destinations": "l.test"},
		&upload{name: "latin.html", data: []byte("<p>caf\xe9</p>")})
	wantStatus(t, w, http.StatusUnprocessableEntity)
	mustContain(t, w.Body.String(), "not valid UTF-8", "Save the file as UTF-8", `value="Latin"`, "l.test")
	if len(hn.store.List()) != 1 {
		t.Fatal("nothing must be created")
	}
}

func TestUploadChecksFileKind(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	w := hn.postMultipart("/pages", s, map[string]string{"name": "T", "destinations": "t.test", "source": "upload"}, nil)
	wantStatus(t, w, http.StatusUnprocessableEntity)
	mustContain(t, w.Body.String(), "Choose a .html file")

	w = hn.postMultipart("/pages", s, map[string]string{"name": "T", "destinations": "t.test"},
		&upload{name: "notes.txt", data: []byte("hello")})
	wantStatus(t, w, http.StatusUnprocessableEntity)
	mustContain(t, w.Body.String(), "Only .html files")

	w = hn.postMultipart("/pages", s, map[string]string{"name": "T", "destinations": "t.test"},
		&upload{name: "empty.html", data: nil})
	wantStatus(t, w, http.StatusUnprocessableEntity)
}

// CA-14 / RF-17: warnings ask for confirmation without a second upload.
func TestUploadWarningsNeedConfirmationWithoutReupload(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	html := `<!doctype html><title>x</title><link rel="stylesheet" href="https://cdn.example/x.css"><script>1</script><p>ok</p>`
	w := hn.postMultipart("/pages", s, map[string]string{"name": "Ext", "destinations": "ext.test"},
		&upload{name: "ext.html", data: []byte(html)})
	wantStatus(t, w, http.StatusOK)
	body := w.Body.String()
	mustContain(t, body, `data-view="confirm-warnings"`, "external resources", "&lt;script&gt;", "Publish anyway")
	if len(hn.store.List()) != 1 {
		t.Fatal("nothing is created before confirmation")
	}
	draft := hiddenValue(draftRe, body)
	if draft == "" {
		t.Fatal("the confirmation form has no draft id")
	}
	mustNotContain(t, body, `type="file"`) // the confirmation step must not ask for the file again

	w = hn.postForm("/pages", s, url.Values{"draft": {draft}, "confirm": {"warnings"}})
	wantStatus(t, w, http.StatusSeeOther)
	site := siteByName(t, hn, "Ext")
	if got, _ := hn.store.HTML(site.ID); string(got) != html {
		t.Fatalf("stored html = %q", got)
	}
	// The draft is single use.
	wantStatus(t, hn.postForm("/pages", s, url.Values{"draft": {draft}, "confirm": {"warnings"}}), http.StatusGone)
}

func TestExternalResourceWarningNamesTheProblem(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	w := hn.postMultipart("/pages", s, map[string]string{"name": "L", "destinations": "l.test"},
		&upload{name: "l.html", data: []byte(`<link href="https://x.example/a.css"><p>x</p>`)})
	wantStatus(t, w, http.StatusOK)
	mustContain(t, w.Body.String(), "&lt;link&gt;", "self-contained")
}

func TestWarningConfirmationBackToEditKeepsInput(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	w := hn.postMultipart("/pages", s, map[string]string{"name": "Ext", "destinations": "ext.test"},
		&upload{name: "ext.html", data: []byte(`<script>1</script>`)})
	draft := hiddenValue(draftRe, w.Body.String())
	mustContain(t, w.Body.String(), `href="/pages/new?draft=`+draft+`"`)
	back := hn.get("/pages/new?draft="+draft, s)
	wantStatus(t, back, 200)
	mustContain(t, back.Body.String(), `value="Ext"`, "ext.test", "ext.html", `name="draft" value="`+draft+`"`)
}

// CA-20: normalization, rejection with reasons, input kept.
func TestDestinationValidation(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	w := hn.postMultipart("/pages", s, templateFields("Acme", "APP.Test\napp:99999\na b"), nil)
	wantStatus(t, w, http.StatusUnprocessableEntity)
	body := w.Body.String()
	mustContain(t, body, "app:99999", "port must be between 1 and 65535", "a b", "letters, digits, dots and hyphens",
		`aria-describedby="destinations-error`)
	mustContain(t, body, "APP.Test\napp:99999\na b") // the textarea keeps exactly what was typed
	if len(hn.store.List()) != 1 {
		t.Fatal("an invalid destination rejects the whole request")
	}

	w = hn.postMultipart("/pages", s, templateFields("Acme", "APP.Test"), nil)
	wantStatus(t, w, http.StatusSeeOther)
	if got := targetValues(siteByName(t, hn, "Acme")); len(got) != 1 || got[0] != "app.test" {
		t.Fatalf("targets = %v", got)
	}
}

func TestDestinationsAcceptLinesAndCommas(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	w := hn.postMultipart("/pages", s, templateFields("Multi", "one.test, two.test\r\nthree.test\n\n  svc:80 ,"), nil)
	wantStatus(t, w, http.StatusSeeOther)
	got := targetValues(siteByName(t, hn, "Multi"))
	if strings.Join(got, " ") != "one.test two.test three.test svc:80" {
		t.Fatalf("targets = %v", got)
	}
}

func TestKeptUploadSurvivesADestinationError(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	w := hn.postMultipart("/pages", s, map[string]string{"name": "Keep", "destinations": "bad dest"},
		&upload{name: "keep.html", data: []byte(validPage)})
	wantStatus(t, w, http.StatusUnprocessableEntity)
	body := w.Body.String()
	draft := hiddenValue(draftRe, body)
	if draft == "" {
		t.Fatal("a valid upload must be kept in a draft when the form is rejected")
	}
	mustContain(t, body, "keep.html", "already uploaded")

	w = hn.postMultipart("/pages", s, map[string]string{"name": "Keep", "destinations": "good.test", "draft": draft}, nil)
	wantStatus(t, w, http.StatusSeeOther)
	site := siteByName(t, hn, "Keep")
	if got, _ := hn.store.HTML(site.ID); string(got) != validPage {
		t.Fatalf("stored html = %q", got)
	}
}

// CA-20: a destination owned by another page asks for confirmation, then moves.
func TestDestinationConflictConfirmationThenMove(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	hn.createPage("Alpha", "app.test")

	w := hn.postMultipart("/pages", s, templateFields("Bravo", "App.Test\nother.test"), nil)
	wantStatus(t, w, http.StatusConflict)
	body := w.Body.String()
	mustContain(t, body, `data-view="confirm-move"`, "app.test", "Alpha", "no destinations", "Move destinations")
	if len(hn.store.List()) != 2 {
		t.Fatal("nothing changes before the confirmation")
	}
	draft := hiddenValue(draftRe, body)

	w = hn.postForm("/pages", s, url.Values{"draft": {draft}, "confirm": {"move"}})
	wantStatus(t, w, http.StatusSeeOther)
	if got := targetValues(siteByName(t, hn, "Alpha")); len(got) != 0 {
		t.Fatalf("Alpha targets = %v", got)
	}
	if got := targetValues(siteByName(t, hn, "Bravo")); strings.Join(got, " ") != "app.test other.test" {
		t.Fatalf("Bravo targets = %v", got)
	}
	mustContainRaw(t, hn.logs.String(), "page created")
}

func TestConflictDoesNotWarnWhenOwnerKeepsDestinations(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	hn.createPage("Alpha", "app.test", "keep.test")
	w := hn.postMultipart("/pages", s, templateFields("Bravo", "app.test"), nil)
	wantStatus(t, w, http.StatusConflict)
	mustContain(t, w.Body.String(), "Alpha")
	mustNotContain(t, w.Body.String(), "no destinations")
}

func TestConflictBackToEditKeepsInput(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	hn.createPage("Alpha", "app.test")
	w := hn.postMultipart("/pages", s, templateFields("Bravo", "app.test"), nil)
	draft := hiddenValue(draftRe, w.Body.String())
	back := hn.get("/pages/new?draft="+draft, s)
	wantStatus(t, back, 200)
	mustContain(t, back.Body.String(), `value="Bravo"`, "app.test")
}

// confirm=true on a complete submission is the one-shot form used by scripts.
func TestConfirmTrueOnACompleteSubmissionConfirmsEverything(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	hn.createPage("Alpha", "app.test")
	f := map[string]string{"name": "Bravo", "destinations": "app.test", "confirm": "true"}
	w := hn.postMultipart("/pages", s, f, &upload{name: "b.html", data: []byte(`<script>1</script>`)})
	wantStatus(t, w, http.StatusSeeOther)
	if got := targetValues(siteByName(t, hn, "Bravo")); len(got) != 1 {
		t.Fatalf("Bravo targets = %v", got)
	}
}

// CA-21: no token, no change.
func TestStateChangingRequestsNeedTheCSRFToken(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	a := hn.createPage("Alpha", "app.test")
	for _, token := range []string{"-", "wrong"} {
		f := templateFields("Bravo", "b.test")
		f["csrf"] = token
		wantStatus(t, hn.postMultipart("/pages", s, f, nil), http.StatusForbidden)
		wantStatus(t, hn.postMultipart("/pages/"+a.ID, s, map[string]string{"name": "X", "destinations": "app.test", "csrf": token}, nil), http.StatusForbidden)
		wantStatus(t, hn.postForm("/pages/"+a.ID+"/delete", s, url.Values{"csrf": {token}, "confirm": {"true"}}), http.StatusForbidden)
	}
	if got := hn.store.List(); len(got) != 2 {
		t.Fatalf("pages changed without a valid token: %v", got)
	}
	if site, _ := hn.store.Get(a.ID); site.Name != "Alpha" {
		t.Fatal("page changed without a valid token")
	}
	// A token from another session is not valid either.
	other := hn.login()
	f := templateFields("Bravo", "b.test")
	f["csrf"] = other.csrf
	wantStatus(t, hn.postMultipart("/pages", s, f, nil), http.StatusForbidden)
}

func TestCSRFFailureSaysWhatToDo(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	w := hn.postForm("/pages", s, url.Values{"csrf": {"-"}})
	wantStatus(t, w, http.StatusForbidden)
	mustContain(t, w.Body.String(), "expired", "Reload")
}

func TestDraftsAreBoundToTheSessionAndExpire(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	w := hn.postMultipart("/pages", s, map[string]string{"name": "Ext", "destinations": "ext.test"},
		&upload{name: "ext.html", data: []byte(`<script>1</script>`)})
	draft := hiddenValue(draftRe, w.Body.String())

	other := hn.login()
	wantStatus(t, hn.postForm("/pages", other, url.Values{"draft": {draft}, "confirm": {"warnings"}}), http.StatusGone)

	hn.clock.Advance(draftTTL + time.Second)
	w = hn.postForm("/pages", s, url.Values{"draft": {draft}, "confirm": {"warnings"}})
	wantStatus(t, w, http.StatusGone)
	mustContain(t, w.Body.String(), "expired", "upload")
	if len(hn.store.List()) != 1 {
		t.Fatal("nothing must be created")
	}
}

// RF-18 / CA-21: the preview is served only under the sandbox policy.
func TestPreviewIsSandboxed(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	const evil = `<!doctype html><title>x</title><script>alert(1)</script><p>hi</p>`
	site, err := hn.store.Create("Evil", []string{"evil.test"}, []byte(evil), false)
	if err != nil {
		t.Fatal(err)
	}
	w := hn.get("/pages/"+site.ID+"/preview", s)
	wantStatus(t, w, 200)
	if w.Body.String() != evil {
		t.Fatalf("preview body = %q", w.Body.String())
	}
	want := "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:; frame-ancestors 'self'"
	if got := w.Header().Get("Content-Security-Policy"); got != want {
		t.Fatalf("CSP = %q, want %q", got, want)
	}
	for h, v := range map[string]string{
		"X-Content-Type-Options": "nosniff", "Cache-Control": "no-store",
		"Content-Type": "text/html; charset=utf-8",
	} {
		if got := w.Header().Get(h); got != v {
			t.Errorf("%s = %q, want %q", h, got, v)
		}
	}
	if w.Header().Get("Content-Security-Policy") == "" || len(w.Result().Header.Values("Content-Security-Policy")) != 1 {
		t.Error("exactly one CSP header expected")
	}

	// Default page too; unknown page 404; no session blocks it.
	wantStatus(t, hn.get("/pages/default/preview", s), 200)
	wantStatus(t, hn.get("/pages/nope/preview", s), 404)
	wantStatus(t, hn.get("/pages/"+site.ID+"/preview", session{}), http.StatusUnauthorized)

	// The panel embeds it without any sandbox relaxation and with a title.
	edit := hn.get("/pages/"+site.ID, s).Body.String()
	mustContain(t, edit, `<iframe`, `sandbox`, `src="/pages/`+site.ID+`/preview"`, `title="Preview of Evil"`)
	mustNotContain(t, edit, "allow-scripts", "allow-same-origin", "allow-top-navigation", "allow-forms", "allow-popups")
}

func TestEditRenamesChangesDestinationsAndReplacesHTML(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	a := hn.createPage("Alpha", "app.test")
	hn.clock.Advance(time.Hour)

	page := hn.get("/pages/"+a.ID, s)
	wantStatus(t, page, 200)
	mustContain(t, page.Body.String(), `data-view="edit"`, `>Alpha</h1>`, `name="destinations"`, "app.test", `type="file"`,
		`action="/pages/`+a.ID+`/delete"`)

	const replacement = "<!doctype html><title>r</title><p>replaced</p>"
	w := hn.postMultipart("/pages/"+a.ID, s, map[string]string{"name": "Alpha 2", "destinations": "new.test\nsvc:90"},
		&upload{name: "r.html", data: []byte(replacement)})
	wantStatus(t, w, http.StatusSeeOther)
	site, _ := hn.store.Get(a.ID)
	if site.Name != "Alpha 2" || strings.Join(targetValues(site), " ") != "new.test svc:90" {
		t.Fatalf("site = %+v", site)
	}
	if got, _ := hn.store.HTML(a.ID); string(got) != replacement {
		t.Fatalf("html = %q", got)
	}
	if fileExists(t, filepath.Join(hn.pagesDir, "host", "app.test.html")) {
		t.Fatal("the old destination file must be gone")
	}
	if got := readFile(t, filepath.Join(hn.pagesDir, "host", "new.test.html")); got != replacement {
		t.Fatalf("published = %q", got)
	}
	mustContainRaw(t, hn.logs.String(), "page updated", "id="+a.ID, "new.test")
}

func TestEditWithoutNewFileKeepsHTML(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	a := hn.createPage("Alpha", "app.test")
	wantStatus(t, hn.postMultipart("/pages/"+a.ID, s, map[string]string{"name": "Renamed", "destinations": "app.test"}, nil), http.StatusSeeOther)
	if got, _ := hn.store.HTML(a.ID); string(got) != validPage {
		t.Fatalf("html = %q", got)
	}
}

func TestEditValidation(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	a := hn.createPage("Alpha", "app.test")

	w := hn.postMultipart("/pages/"+a.ID, s, map[string]string{"name": "Alpha", "destinations": "a b"}, nil)
	wantStatus(t, w, http.StatusUnprocessableEntity)
	mustContain(t, w.Body.String(), `data-view="edit"`, "a b", "letters, digits")

	w = hn.postMultipart("/pages/"+a.ID, s, map[string]string{"name": "Alpha", "destinations": ""}, nil)
	wantStatus(t, w, http.StatusUnprocessableEntity)
	mustContain(t, w.Body.String(), "at least one destination", "delete the page")

	w = hn.postMultipart("/pages/"+a.ID, s, map[string]string{"name": "  ", "destinations": "app.test"}, nil)
	wantStatus(t, w, http.StatusUnprocessableEntity)
	mustContain(t, w.Body.String(), "name is required")

	w = hn.postMultipart("/pages/"+a.ID, s, map[string]string{"name": "Alpha", "destinations": "app.test"},
		&upload{name: "x.html", data: []byte("\xff\xfe")})
	wantStatus(t, w, http.StatusUnprocessableEntity)
	mustContain(t, w.Body.String(), "not valid UTF-8")
	if site, _ := hn.store.Get(a.ID); site.Name != "Alpha" {
		t.Fatal("invalid edits must not change the page")
	}
}

func TestEditPageWithoutDestinationsCanBeRenamed(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	a := hn.createPage("Alpha", "app.test")
	if _, err := hn.store.Create("Bravo", []string{"app.test"}, []byte(validPage), true); err != nil {
		t.Fatal(err)
	}
	page := hn.get("/pages/"+a.ID, s).Body.String()
	mustContain(t, page, "No destinations — visitors never see this page")
	wantStatus(t, hn.postMultipart("/pages/"+a.ID, s, map[string]string{"name": "Alpha 2", "destinations": ""}, nil), http.StatusSeeOther)
	site, _ := hn.store.Get(a.ID)
	if site.Name != "Alpha 2" || len(site.Targets) != 0 {
		t.Fatalf("site = %+v", site)
	}
}

func TestEditConflictAsksThenMoves(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	hn.createPage("Alpha", "app.test")
	b := hn.createPage("Bravo", "b.test")
	w := hn.postMultipart("/pages/"+b.ID, s, map[string]string{"name": "Bravo", "destinations": "b.test\napp.test"}, nil)
	wantStatus(t, w, http.StatusConflict)
	body := w.Body.String()
	mustContain(t, body, "confirm-move", "Alpha", "no destinations")
	draft := hiddenValue(draftRe, body)
	mustContain(t, body, `action="/pages/`+b.ID+`"`)
	wantStatus(t, hn.postForm("/pages/"+b.ID, s, url.Values{"draft": {draft}, "confirm": {"move"}}), http.StatusSeeOther)
	site, _ := hn.store.Get(b.ID)
	if strings.Join(targetValues(site), " ") != "b.test app.test" {
		t.Fatalf("targets = %v", targetValues(site))
	}
}

func TestEditWarningsNeedConfirmation(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	a := hn.createPage("Alpha", "app.test")
	w := hn.postMultipart("/pages/"+a.ID, s, map[string]string{"name": "Alpha", "destinations": "app.test"},
		&upload{name: "s.html", data: []byte(`<script>1</script>`)})
	wantStatus(t, w, http.StatusOK)
	mustContain(t, w.Body.String(), "confirm-warnings")
	if got, _ := hn.store.HTML(a.ID); string(got) != validPage {
		t.Fatal("html replaced before confirmation")
	}
	draft := hiddenValue(draftRe, w.Body.String())
	wantStatus(t, hn.postForm("/pages/"+a.ID, s, url.Values{"draft": {draft}, "confirm": {"warnings"}}), http.StatusSeeOther)
	if got, _ := hn.store.HTML(a.ID); string(got) != `<script>1</script>` {
		t.Fatalf("html = %q", got)
	}
}

func TestDefaultPageEditing(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	page := hn.get("/pages/default", s)
	wantStatus(t, page, 200)
	body := page.Body.String()
	mustContain(t, body, `name="name"`, `type="file"`, "Everything else")
	mustNotContain(t, body, `name="destinations"`, `/pages/default/delete`)

	const replacement = "<!doctype html><title>d</title><p>default v2</p>"
	w := hn.postMultipart("/pages/default", s, map[string]string{"name": "Fallback"},
		&upload{name: "d.html", data: []byte(replacement)})
	wantStatus(t, w, http.StatusSeeOther)
	if got := readFile(t, filepath.Join(hn.pagesDir, "default.html")); got != replacement {
		t.Fatalf("default.html = %q", got)
	}
	if site, _ := hn.store.Get(pages.DefaultID); site.Name != "Fallback" {
		t.Fatalf("name = %q", site.Name)
	}

	w = hn.postMultipart("/pages/default", s, map[string]string{"name": "Fallback", "destinations": "x.test"}, nil)
	wantStatus(t, w, http.StatusConflict)
	mustContain(t, w.Body.String(), "default page")
}

// RF-19 / CA-6: delete asks first, frees the destinations.
func TestDeleteConfirmsThenFallsBackToDefault(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	a := hn.createPage("Alpha", "app.test", "web:8080")
	file := filepath.Join(hn.pagesDir, "host", "app.test.html")
	if !fileExists(t, file) {
		t.Fatal("setup: page not published")
	}

	w := hn.postForm("/pages/"+a.ID+"/delete", s, url.Values{})
	wantStatus(t, w, http.StatusOK)
	mustContain(t, w.Body.String(), `data-view="confirm-delete"`, "Delete “Alpha”?", "app.test", "web:8080",
		"default page", `name="confirm" value="true"`, "Keep page")
	if _, err := hn.store.Get(a.ID); err != nil {
		t.Fatal("the confirm step must not delete")
	}

	w = hn.postForm("/pages/"+a.ID+"/delete", s, url.Values{"confirm": {"true"}})
	wantStatus(t, w, http.StatusSeeOther)
	if loc := w.Header().Get("Location"); loc != "/?notice=deleted" {
		t.Fatalf("Location = %q", loc)
	}
	if _, err := hn.store.Get(a.ID); !errors.Is(err, pages.ErrNotFound) {
		t.Fatalf("page still exists: %v", err)
	}
	if fileExists(t, file) {
		t.Fatal("published file must be removed")
	}
	if !fileExists(t, filepath.Join(hn.pagesDir, "default.html")) {
		t.Fatal("default.html must remain")
	}
	mustContainRaw(t, hn.logs.String(), "page deleted", "id="+a.ID, "app.test", "web:8080")
}

func TestDefaultPageCannotBeDeleted(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	for _, f := range []url.Values{{}, {"confirm": {"true"}}} {
		w := hn.postForm("/pages/default/delete", s, f)
		wantStatus(t, w, http.StatusConflict)
		mustContain(t, w.Body.String(), "default page cannot be deleted")
	}
	if _, err := hn.store.Get(pages.DefaultID); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownPagesAre404(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	wantStatus(t, hn.get("/pages/zzzz", s), 404)
	wantStatus(t, hn.postMultipart("/pages/zzzz", s, map[string]string{"name": "x", "destinations": "x.test"}, nil), 404)
	wantStatus(t, hn.postForm("/pages/zzzz/delete", s, url.Values{"confirm": {"true"}}), 404)
	wantStatus(t, hn.get("/no/such/route", s), 404)
	w := hn.get("/pages/zzzz", s)
	mustContain(t, w.Body.String(), "Page not found", "Back to the directory")
}

func TestPublishFailureIsReportedWithoutLeakingInternals(t *testing.T) {
	hn := newHarnessWith(t, errors.New("disk on fire at /secret/volume/path"))
	s := hn.login()
	w := hn.postMultipart("/pages", s, templateFields("Acme", "app.test"), nil)
	wantStatus(t, w, http.StatusSeeOther)
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "notice=publish-failed") {
		t.Fatalf("Location = %q", loc)
	}
	if _, err := hn.store.Get(siteByName(t, hn, "Acme").ID); err != nil {
		t.Fatal("the page is saved even when publishing fails")
	}
	body := hn.get(loc, s).Body.String()
	mustContain(t, body, "could not publish")
	mustNotContain(t, body, "disk on fire", "/secret/volume/path", hn.pagesDir, hn.metaDir)
	mustContainRaw(t, hn.logs.String(), "publish failed", "disk on fire")
}

func TestOversizedFormBodyIsRejectedCleanlyOnLogin(t *testing.T) {
	hn := newHarness(t)
	cookie, token := hn.loginForm("")
	form := url.Values{"user": {strings.Repeat("a", 1<<20)}, "password": {"x"}, "login_token": {token}}
	r := navigate(httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode())))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: cookie.Name, Value: cookie.Value})
	w := hn.serve(r)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d", w.Code)
	}
}

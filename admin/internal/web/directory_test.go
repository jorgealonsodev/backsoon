package web

import (
	"regexp"
	"strings"
	"testing"

	"github.com/jorgealonsodev/backsoon/admin/internal/pages"
)

func TestDirectoryEmptyStateTeachesTheFirstAction(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	w := hn.get("/", s)
	wantStatus(t, w, 200)
	body := w.Body.String()
	mustContain(t, body, `data-view="directory"`, `lang="en"`, "Create a page", `href="/pages/new"`,
		"Default page", "Everything else")
	if strings.Contains(strings.ToLower(body), "nothing here") {
		t.Error("empty state must teach the first action, not say 'nothing here'")
	}
	if n := strings.Count(body, "<h1"); n != 1 {
		t.Errorf("h1 count = %d", n)
	}
	// The sign-out control is a POST form with a CSRF token.
	mustContain(t, body, `action="/logout"`)
}

func TestDirectoryHidesTeachingOnceAPageExists(t *testing.T) {
	hn := newHarness(t)
	hn.createPage("Alpha", "app.test")
	w := hn.get("/", hn.login())
	mustNotContain(t, w.Body.String(), "Create a page for")
}

// CA-18: three pages with varied destinations plus the default page.
func TestDirectoryListsEveryPage(t *testing.T) {
	hn := newHarness(t)
	a := hn.createPage("Alpha", "app.test", "web:8080")
	hn.createPage("Charlie", "c.example.org")
	hn.createPage("Bravo", "bravo.test", "b-svc:81")
	w := hn.get("/", hn.login())
	wantStatus(t, w, 200)
	body := w.Body.String()

	pos := func(s string) int {
		i := strings.Index(body, s)
		if i < 0 {
			t.Errorf("directory lacks %q", s)
		}
		return i
	}
	order := []int{pos(">Alpha<"), pos(">Bravo<"), pos(">Charlie<"), pos(">Default page<")}
	for i := 1; i < len(order); i++ {
		if order[i-1] >= order[i] {
			t.Errorf("pages are not ordered by name with the default page last: %v", order)
		}
	}
	mustContain(t, body, "app.test", "web:8080", "bravo.test", "b-svc:81", "c.example.org",
		`href="/pages/`+a.ID+`"`, `href="/?focus=`+a.ID+`#page-`+a.ID+`"`,
		`<time datetime="2026-10-13T21:04:30Z">2026-10-13 21:04 UTC</time>`,
		"mark-domain", "mark-service")
	// Domain and service:port are told apart by text as well as by their mark.
	mustContain(t, body, `<span class="sr">domain: </span>`, `<span class="sr">service:port: </span>`)
	mustContain(t, body, `href="/pages/default"`)
	// No preview iframe until one is asked for.
	mustNotContain(t, body, "<iframe")
}

func TestDirectoryShowsPageWithoutDestinations(t *testing.T) {
	hn := newHarness(t)
	a := hn.createPage("Alpha", "app.test")
	if _, err := hn.store.Create("Bravo", []string{"app.test"}, []byte(validPage), true); err != nil {
		t.Fatal(err)
	}
	w := hn.get("/", hn.login())
	body := w.Body.String()
	mustContain(t, body, "No destinations", "Visitors never see this page", `id="page-`+a.ID+`"`)
}

func TestDirectoryFocusExpandsPreview(t *testing.T) {
	hn := newHarness(t)
	a := hn.createPage("Alpha", "app.test")
	b := hn.createPage("Bravo", "bravo.test")
	body := hn.get("/?focus="+a.ID, hn.login()).Body.String()
	mustContain(t, body, `src="/pages/`+a.ID+`/preview"`, `sandbox`, "is-focus")
	mustNotContain(t, body, `src="/pages/`+b.ID+`/preview"`, "allow-scripts", "allow-same-origin")
	if n := strings.Count(body, "<iframe"); n != 1 {
		t.Errorf("iframes = %d", n)
	}
	// Unknown focus ids are ignored.
	mustNotContain(t, hn.get("/?focus=nope", hn.login()).Body.String(), "<iframe")
}

func TestNoticeKeysRenderFixedMessagesOnly(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	mustContain(t, hn.get("/?notice=deleted", s).Body.String(), "Page deleted")
	body := hn.get("/?notice=%3Cb%3Ehax%3C%2Fb%3E", s).Body.String()
	mustNotContain(t, body, "hax", `role="status"`)
}

var hitLine = func(dest string) *regexp.Regexp {
	return regexp.MustCompile(`(?s)<li class="chip is-hit">.{0,600}?<code>` + regexp.QuoteMeta(dest))
}

func TestLookupDomainHit(t *testing.T) {
	hn := newHarness(t)
	hn.createPage("Alpha", "app.test", "web:8080")
	hn.createPage("Bravo", "bravo.test")
	w := hn.get("/?lookup=APP.Test", hn.login())
	wantStatus(t, w, 200)
	body := w.Body.String()
	mustContain(t, body, `role="status"`, `id="lookup-answer"`, `value="app.test"`, `<strong>Alpha</strong>`)
	if strings.Count(body, `class="entry is-hit"`) != 1 {
		t.Errorf("exactly one row must be highlighted")
	}
	mustContain(t, body, `<span class="badge badge-orange">Match</span>`)
	if !hitLine("app.test").MatchString(body) {
		t.Errorf("the matching destination line is not lit\n%s", excerpt(body))
	}
	if strings.Count(body, `class="chip is-hit"`) != 1 {
		t.Errorf("exactly one destination must be lit")
	}
}

func TestLookupServiceHit(t *testing.T) {
	hn := newHarness(t)
	hn.createPage("Alpha", "app.test", "web:8080")
	body := hn.get("/?lookup=web:8080", hn.login()).Body.String()
	if !hitLine("web:8080").MatchString(body) {
		t.Errorf("service:port line is not lit\n%s", excerpt(body))
	}
	mustContain(t, body, "domain has no page of its own")
}

func TestLookupMissFallsBackToDefaultAndSaysWhy(t *testing.T) {
	hn := newHarness(t)
	hn.createPage("Alpha", "app.test")
	body := hn.get("/?lookup=other.test", hn.login()).Body.String()
	mustContain(t, body, "No page is assigned to", "NPM proxy host", "default page", `class="entry is-default is-hit"`)
	// The lit chip is the default page's "everything else" one.
	if !regexp.MustCompile(`(?s)<li class="chip chip-else is-hit">.{0,300}?Everything else`).MatchString(body) {
		t.Errorf("the default line is not lit\n%s", excerpt(body))
	}
	if hitLine("app.test").MatchString(body) {
		t.Error("an unrelated destination is lit")
	}
}

func TestLookupServiceMissFallsBackToDefault(t *testing.T) {
	hn := newHarness(t)
	body := hn.get("/?lookup=svc:99", hn.login()).Body.String()
	mustContain(t, body, "No page is assigned to", `class="chip chip-else is-hit"`)
}

func TestLookupPrefersDomainOverServiceOrder(t *testing.T) {
	hn := newHarness(t)
	// A domain and a service:port are different destinations; the lookup of a bare name never
	// matches a service page.
	hn.createPage("Alpha", "web:8080")
	body := hn.get("/?lookup=web", hn.login()).Body.String()
	if hitLine("web:8080").MatchString(body) {
		t.Error("a domain lookup must not light a service:port line")
	}
}

func TestLookupInvalidInputExplainsWhy(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	cases := map[string]string{
		"a b":                   "letters, digits, dots and hyphens",
		"app:99999":             "port must be between 1 and 65535",
		"https://app.test/path": "without http://",
		"app.test:":             "port",
		"UPPER_CASE.test":       "letters, digits, dots and hyphens",
	}
	for input, want := range cases {
		w := hn.get("/?lookup="+strings.ReplaceAll(input, " ", "%20"), s)
		wantStatus(t, w, 200)
		body := w.Body.String()
		mustContain(t, body, want, `aria-invalid="true"`, `aria-describedby="lookup-error`)
		mustNotContain(t, body, "is-hit", `id="lookup-answer"`)
	}
}

func TestLookupBlankIsIgnored(t *testing.T) {
	hn := newHarness(t)
	body := hn.get("/?lookup=%20", hn.login()).Body.String()
	mustNotContain(t, body, "is-hit", `id="lookup-answer"`, `aria-invalid`)
}

func TestLookupEscapesInput(t *testing.T) {
	hn := newHarness(t)
	body := hn.get(`/?lookup=%22%3E%3Cscript%3Ealert(1)%3C/script%3E`, hn.login()).Body.String()
	mustNotContain(t, body, "<script>alert")
}

var _ = pages.DefaultID

// The home view follows the table grammar of the tools it sits beside.
func TestDirectoryTableGrammar(t *testing.T) {
	hn := newHarness(t)
	a := hn.createPage("Alpha", "app.test", "web:8080")
	body := hn.get("/", hn.login()).Body.String()

	mustContain(t, body, `<h1 class="card-title">Maintenance pages</h1>`, `class="card-status"`,
		`>Name</th>`, `>Destinations</th>`, `>Preview</th>`, `<span class="sr">Actions</span>`,
		`<label class="sr" for="lookup">Which page does … show?</label>`, `type="search"`, "Add page", `<span class="who">admin</span>`)

	// Each row has a no-JS actions menu: edit, preview full size and, except for the default page, delete.
	menu := regexp.MustCompile(`(?s)<tr[^>]*id="page-` + a.ID + `".*?</tr>`).FindString(body)
	mustContain(t, menu, `<details class="menu"`, `aria-label="Actions for Alpha"`, `>Edit<`, "Preview full size",
		`action="/pages/`+a.ID+`/delete"`, `name="csrf"`)
	def := regexp.MustCompile(`(?s)<tr[^>]*id="page-default".*?</tr>`).FindString(body)
	mustContain(t, def, "Everything else", "Preview full size", ">Edit<")
	mustNotContain(t, def, "/delete")
}

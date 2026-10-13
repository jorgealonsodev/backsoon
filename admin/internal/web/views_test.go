package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

var (
	tagRe       = regexp.MustCompile(`(?s)<(/?)([a-zA-Z][a-zA-Z0-9-]*)((?:[^>"']|"[^"]*"|'[^']*')*)>`)
	voidTags    = map[string]bool{"meta": true, "link": true, "input": true, "br": true, "hr": true, "img": true}
	idAttrRe    = regexp.MustCompile(`\sid="([^"]+)"`)
	forAttrRe   = regexp.MustCompile(`<label[^>]*\sfor="([^"]+)"`)
	describedRe = regexp.MustCompile(`aria-describedby="([^"]+)"`)
	inputRe     = regexp.MustCompile(`<(input|textarea|select)\b([^>]*)>`)
	typeAttrRe  = regexp.MustCompile(`\stype="([^"]+)"`)
	onAttrRe    = regexp.MustCompile(`\son[a-z]+=`)
	formRe      = regexp.MustCompile(`(?s)<form\b([^>]*)>(.*?)</form>`)
)

// checkWellFormed verifies tags balance, ignoring void elements and the stylesheet text.
func checkWellFormed(t *testing.T, name, body string) {
	t.Helper()
	body = styleRe.ReplaceAllString(body, "<style></style>")
	var stack []string
	for _, m := range tagRe.FindAllStringSubmatch(body, -1) {
		closing, tag, attrs := m[1] == "/", strings.ToLower(m[2]), m[3]
		switch {
		case closing:
			if len(stack) == 0 || stack[len(stack)-1] != tag {
				t.Errorf("%s: unbalanced </%s> (open: %v)", name, tag, stack)
				return
			}
			stack = stack[:len(stack)-1]
		case voidTags[tag] || strings.HasSuffix(strings.TrimSpace(attrs), "/"):
		default:
			stack = append(stack, tag)
		}
	}
	if len(stack) != 0 {
		t.Errorf("%s: unclosed tags %v", name, stack)
	}
}

func checkView(t *testing.T, name string, w *httptest.ResponseRecorder, wantLogin bool) {
	t.Helper()
	body := w.Body.String()
	noStyle := styleRe.ReplaceAllString(body, "<style></style>")
	checkWellFormed(t, name, body)
	for _, bad := range []string{"{{", "<script", "style=\""} {
		if strings.Contains(body, bad) {
			t.Errorf("%s: contains %q", name, bad)
		}
	}
	for _, bad := range []string{"}}", "<no value>", "%!", "&lt;nil&gt;", "ZgotmplZ"} {
		if strings.Contains(noStyle, bad) {
			t.Errorf("%s: contains %q", name, bad)
		}
	}
	if onAttrRe.MatchString(noStyle) {
		t.Errorf("%s: inline event handler", name)
	}
	mustContainRaw(t, noStyle, `<html lang="en"`, `<meta name="viewport" content="width=device-width, initial-scale=1">`, "<title>")
	if n := strings.Count(noStyle, "<h1"); n != 1 {
		t.Errorf("%s: %d h1 elements", name, n)
	}
	if csp := w.Header().Get("Content-Security-Policy"); strings.Contains(csp, "'unsafe-inline'") || !strings.Contains(csp, "script-src 'none'") {
		t.Errorf("%s: CSP = %q", name, csp)
	}

	ids := map[string]bool{}
	for _, m := range idAttrRe.FindAllStringSubmatch(noStyle, -1) {
		if ids[m[1]] {
			t.Errorf("%s: duplicate id %q", name, m[1])
		}
		ids[m[1]] = true
	}
	labels := map[string]bool{}
	for _, m := range forAttrRe.FindAllStringSubmatch(noStyle, -1) {
		labels[m[1]] = true
	}
	for _, m := range inputRe.FindAllStringSubmatch(noStyle, -1) {
		if tm := typeAttrRe.FindStringSubmatch(m[2]); tm != nil && tm[1] == "hidden" {
			continue
		}
		idm := idAttrRe.FindStringSubmatch(m[2])
		if idm == nil || !labels[idm[1]] {
			t.Errorf("%s: control without a <label for>: <%s%s>", name, m[1], m[2])
		}
	}
	for _, m := range describedRe.FindAllStringSubmatch(noStyle, -1) {
		for _, id := range strings.Fields(m[1]) {
			if !ids[id] {
				t.Errorf("%s: aria-describedby points to missing id %q", name, id)
			}
		}
	}
	for _, m := range formRe.FindAllStringSubmatch(noStyle, -1) {
		if strings.Contains(m[1], `method="post"`) && !strings.Contains(m[2], `name="csrf"`) && !strings.Contains(m[2], `name="login_token"`) {
			t.Errorf("%s: POST form without a CSRF token: %s", name, m[1])
		}
	}
	// No font (or any other static asset) is requested: the panel uses the system stacks.
	mustNotContainRaw(t, body, "/assets/", "@font-face", ".woff", "font/woff")
}

// Every view renders, parses, and carries no template residue.
func TestEveryViewRenders(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	views := map[string]*httptest.ResponseRecorder{}

	views["login"] = hn.serve(navigate(httptest.NewRequest("GET", "/login", nil)))
	views["login failure"] = hn.postLogin(testUser, "nope", "203.0.113.50")
	views["directory empty"] = hn.get("/", s)
	views["new"] = hn.get("/pages/new", s)

	a := hn.createPage("Alpha", "app.test", "web:8080")
	hn.createPage("Bravo", "bravo.test")
	views["directory"] = hn.get("/", s)
	views["directory focus"] = hn.get("/?focus="+a.ID+"&notice=created", s)
	views["lookup hit"] = hn.get("/?lookup=app.test", s)
	views["lookup service hit"] = hn.get("/?lookup=web:8080", s)
	views["lookup miss"] = hn.get("/?lookup=zzz.test", s)
	views["lookup invalid"] = hn.get("/?lookup=a%20b", s)
	views["notice publish"] = hn.get("/?notice=publish-failed", s)
	views["edit"] = hn.get("/pages/"+a.ID, s)
	views["edit default"] = hn.get("/pages/default", s)

	views["new errors"] = hn.postMultipart("/pages", s, templateFields("", "a b\napp:99999"), nil)
	views["edit errors"] = hn.postMultipart("/pages/"+a.ID, s, map[string]string{"name": "", "destinations": "a b"}, nil)
	views["too large"] = hn.postMultipart("/pages", s, map[string]string{"name": "Big", "destinations": "big.test"},
		&upload{name: "big.html", data: []byte("<p>" + strings.Repeat("a", 600*1024))})
	views["not utf8"] = hn.postMultipart("/pages", s, map[string]string{"name": "L", "destinations": "l.test"},
		&upload{name: "l.html", data: []byte("\xff")})
	views["warning confirm"] = hn.postMultipart("/pages", s, map[string]string{"name": "W", "destinations": "w.test"},
		&upload{name: "w.html", data: []byte(`<link href="https://x/a.css"><script>1</script>`)})
	views["conflict confirm"] = hn.postMultipart("/pages", s, templateFields("Charlie", "bravo.test, app.test"), nil)
	views["delete confirm"] = hn.postForm("/pages/"+a.ID+"/delete", s, url.Values{})
	views["default delete"] = hn.postForm("/pages/default/delete", s, url.Values{})
	views["404"] = hn.get("/pages/none", s)
	views["csrf"] = hn.postForm("/pages", s, url.Values{"csrf": {"-"}})
	views["draft gone"] = hn.postForm("/pages", s, url.Values{"draft": {"missing"}, "confirm": {"warnings"}})
	for i := 0; i < 5; i++ {
		hn.postLogin(testUser, "bad", "203.0.113.77")
	}
	views["rate limited"] = hn.postLogin(testUser, "bad", "203.0.113.77")

	wantCodes := map[string]int{
		"login": 200, "login failure": 401, "directory empty": 200, "new": 200, "directory": 200,
		"directory focus": 200, "lookup hit": 200, "lookup service hit": 200, "lookup miss": 200,
		"lookup invalid": 200, "notice publish": 200, "edit": 200, "edit default": 200,
		"new errors": 422, "edit errors": 422, "too large": 413, "not utf8": 422,
		"warning confirm": 200, "conflict confirm": 409, "delete confirm": 200, "default delete": 409,
		"404": 404, "csrf": 403, "draft gone": 410, "rate limited": 429,
	}
	for name, w := range views {
		t.Run(name, func(t *testing.T) {
			if want := wantCodes[name]; w.Code != want {
				t.Fatalf("status = %d, want %d\n%s", w.Code, want, excerpt(w.Body.String()))
			}
			checkView(t, name, w, strings.HasPrefix(name, "login") || name == "rate limited")
		})
	}
	if len(views) != len(wantCodes) {
		t.Errorf("views %d vs expectations %d", len(views), len(wantCodes))
	}
}

func TestPanelPagesInlineTheStylesheet(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	for name, body := range map[string]string{
		"directory": hn.get("/", s).Body.String(),
		"login":     hn.serve(navigate(httptest.NewRequest("GET", "/login", nil))).Body.String(),
	} {
		mustContainRaw(t, body, "font-variant-numeric", "prefers-color-scheme: dark", "prefers-reduced-motion", "caret-color",
			"::selection", "scrollbar-color", "text-underline-offset", "accent-color",
			`system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", sans-serif`, "ui-monospace")
		mustNotContainRaw(t, strings.ReplaceAll(body, `<link rel="icon" href="data:,">`, ""), "<link", "@import", "url(http", "@font-face", "Archivo", "Martian")
		_ = name
	}
}

// The self-hosted fonts are gone: their route no longer exists, behind the session gate.
func TestFontsRouteIsGone(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	for _, p := range []string{"/assets/fonts/archivo.woff2", "/assets/fonts/martian-mono.woff2", "/assets/fonts/Archivo-OFL.txt", "/assets/panel.css"} {
		wantStatus(t, hn.get(p, s), 404)
		wantStatus(t, hn.get(p, session{}), http.StatusSeeOther) // the gate still comes first
		wantStatus(t, hn.serve(fetchReq(httptest.NewRequest("GET", p, nil))), http.StatusUnauthorized)
	}
}

func TestDateIsAbsoluteUTC(t *testing.T) {
	hn := newHarness(t)
	hn.clock.Advance(26*time.Hour + 57*time.Minute)
	hn.createPage("Late", "late.test")
	mustContain(t, hn.get("/", hn.login()).Body.String(), `<time datetime="2026-10-15T00:01:30Z">2026-10-15 00:01 UTC</time>`)
}

// Browsers ask for /favicon.ico on every page; an inline icon keeps the console clean without
// opening a route outside the session gate.
func TestEveryLayoutDeclaresAnInlineFavicon(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	for name, w := range map[string]*httptest.ResponseRecorder{
		"login":     hn.serve(navigate(httptest.NewRequest("GET", "/login", nil))),
		"directory": hn.get("/", s),
		"edit":      hn.get("/pages/default", s),
		"404":       hn.get("/pages/none", s),
	} {
		mustContain(t, w.Body.String(), `<link rel="icon" href="data:,">`)
		if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "img-src 'self' data:") {
			t.Errorf("%s: CSP does not allow data: icons: %s", name, csp)
		}
	}
}

package web

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/jorgealonsodev/backsoon/admin/internal/pages"
	"github.com/jorgealonsodev/backsoon/admin/internal/publish"
)

const (
	testUser     = "admin"
	testPassword = "correct horse battery"
	templatePath = "../../../templates/maintenance.html"
)

var testSecret = []byte("0123456789abcdef0123456789abcdef-secret")

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

type harness struct {
	t        *testing.T
	h        http.Handler
	store    *pages.Store
	pagesDir string
	metaDir  string
	clock    *clock
	logs     *lockedBuffer
	failPub  error
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessWith(t, nil)
}

func newHarnessWith(t *testing.T, failPub error) *harness {
	t.Helper()
	dir := t.TempDir()
	hn := &harness{
		t:        t,
		pagesDir: filepath.Join(dir, "pages"),
		metaDir:  filepath.Join(dir, "meta"),
		clock:    &clock{t: time.Date(2026, 10, 13, 21, 4, 30, 0, time.UTC)},
		logs:     &lockedBuffer{},
		failPub:  failPub,
	}
	real := publish.Publisher(hn.pagesDir)
	store, err := pages.Open(pages.Options{
		MetaDir: hn.metaDir, PagesDir: hn.pagesDir, TemplatePath: templatePath, Refresh: 10,
		Now: hn.clock.Now,
		Publish: func(s pages.Snapshot) error {
			if hn.failPub != nil && len(s.Pages) > 1 {
				return hn.failPub
			}
			return real(s)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	hn.store = store
	hash, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	h, err := New(Options{
		Store: store, User: testUser, PasswordHash: hash, SessionSecret: testSecret,
		Logger: log.New(hn.logs, "", 0), Now: hn.clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	hn.h = h
	return hn
}

func (hn *harness) serve(r *http.Request) *httptest.ResponseRecorder {
	hn.t.Helper()
	if r.RemoteAddr == "" || r.RemoteAddr == "192.0.2.1:1234" {
		r.RemoteAddr = "198.51.100.7:40000"
	}
	w := httptest.NewRecorder()
	hn.h.ServeHTTP(w, r)
	return w
}

// navigate marks r as a browser navigation.
func navigate(r *http.Request) *http.Request {
	r.Header.Set("Sec-Fetch-Mode", "navigate")
	r.Header.Set("Accept", "text/html,application/xhtml+xml")
	return r
}

// fetchReq marks r as a fetch()/XHR call.
func fetchReq(r *http.Request) *http.Request {
	r.Header.Set("Sec-Fetch-Mode", "cors")
	r.Header.Set("Accept", "application/json")
	return r
}

type session struct {
	cookie *http.Cookie
	csrf   string
}

func (s session) apply(r *http.Request) *http.Request {
	if s.cookie != nil {
		r.AddCookie(&http.Cookie{Name: s.cookie.Name, Value: s.cookie.Value})
	}
	return r
}

var (
	csrfRe  = regexp.MustCompile(`name="csrf" value="([^"]+)"`)
	draftRe = regexp.MustCompile(`name="draft" value="([^"]+)"`)
)

func hiddenValue(re *regexp.Regexp, body string) string {
	m := re.FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return m[1]
}

// loginForm fetches the login form and returns its pre-session token cookie and field.
func (hn *harness) loginForm(ip string) (cookie *http.Cookie, token string) {
	hn.t.Helper()
	r := navigate(httptest.NewRequest(http.MethodGet, "/login", nil))
	if ip != "" {
		r.Header.Set("X-Real-IP", ip)
	}
	w := hn.serve(r)
	if w.Code != 200 {
		hn.t.Fatalf("GET /login = %d", w.Code)
	}
	for _, c := range w.Result().Cookies() {
		cookie = c
	}
	token = hiddenValue(regexp.MustCompile(`name="login_token" value="([^"]+)"`), w.Body.String())
	if cookie == nil || token == "" {
		hn.t.Fatalf("login form has no token cookie/field: %v %q", w.Result().Cookies(), token)
	}
	return cookie, token
}

func (hn *harness) postLogin(user, password, ip string) *httptest.ResponseRecorder {
	hn.t.Helper()
	cookie, token := hn.loginForm(ip)
	form := url.Values{"user": {user}, "password": {password}, "login_token": {token}}
	r := navigate(httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode())))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: cookie.Name, Value: cookie.Value})
	if ip != "" {
		r.Header.Set("X-Real-IP", ip)
	}
	return hn.serve(r)
}

// login signs in and returns a session with its CSRF token.
func (hn *harness) login() session {
	hn.t.Helper()
	w := hn.postLogin(testUser, testPassword, "")
	if w.Code != http.StatusSeeOther {
		hn.t.Fatalf("login = %d: %s", w.Code, w.Body.String())
	}
	var s session
	for _, c := range w.Result().Cookies() {
		if c.Name == "backsoon_session" {
			s.cookie = c
		}
	}
	if s.cookie == nil {
		hn.t.Fatal("login set no session cookie")
	}
	page := hn.get("/pages/new", s)
	s.csrf = hiddenValue(csrfRe, page.Body.String())
	if s.csrf == "" {
		hn.t.Fatalf("no csrf token in /pages/new: %d", page.Code)
	}
	return s
}

func (hn *harness) get(path string, s session) *httptest.ResponseRecorder {
	hn.t.Helper()
	return hn.serve(s.apply(navigate(httptest.NewRequest(http.MethodGet, path, nil))))
}

func (hn *harness) postForm(path string, s session, form url.Values) *httptest.ResponseRecorder {
	hn.t.Helper()
	if _, set := form["csrf"]; !set && s.csrf != "" {
		form.Set("csrf", s.csrf)
	} else if form.Get("csrf") == "-" {
		form.Del("csrf") // "-" means: send no token at all
	}
	r := navigate(httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode())))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return hn.serve(s.apply(r))
}

type upload struct {
	name string
	data []byte
}

func (hn *harness) postMultipart(path string, s session, fields map[string]string, file *upload) *httptest.ResponseRecorder {
	hn.t.Helper()
	body, ctype := multipartBody(hn.t, s, fields, file)
	r := navigate(httptest.NewRequest(http.MethodPost, path, body))
	r.Header.Set("Content-Type", ctype)
	return hn.serve(s.apply(r))
}

func multipartBody(t *testing.T, s session, fields map[string]string, file *upload) (io.Reader, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if _, set := fields["csrf"]; !set && s.csrf != "" {
		fields["csrf"] = s.csrf
	}
	for k, v := range fields {
		if v == "-" && k == "csrf" {
			continue
		}
		if err := mw.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if file != nil {
		fw, err := mw.CreateFormFile("file", file.name)
		if err != nil {
			t.Fatal(err)
		}
		fw.Write(file.data)
	}
	mw.Close()
	return &buf, mw.FormDataContentType()
}

const validPage = "<!doctype html><html><head><title>Hi</title></head><body><p>hello</p></body></html>"

// createPage adds a page straight through the store.
func (hn *harness) createPage(name string, targets ...string) pages.Site {
	hn.t.Helper()
	site, err := hn.store.Create(name, targets, []byte(validPage), false)
	if err != nil {
		hn.t.Fatalf("create %q: %v", name, err)
	}
	return site
}

var styleRe = regexp.MustCompile(`(?s)<style[^>]*>.*?</style>`)

// mustContain and mustNotContain look at the markup only: the inline stylesheet mentions every
// class name. Use mustContainRaw / mustNotContainRaw to include it.
func mustContain(t *testing.T, body string, want ...string) {
	t.Helper()
	mustContainRaw(t, styleRe.ReplaceAllString(body, "<style></style>"), want...)
}

func mustNotContain(t *testing.T, body string, unwanted ...string) {
	t.Helper()
	mustNotContainRaw(t, styleRe.ReplaceAllString(body, "<style></style>"), unwanted...)
}

func mustContainRaw(t *testing.T, body string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("body does not contain %q\n%s", w, excerpt(body))
		}
	}
}

func mustNotContainRaw(t *testing.T, body string, unwanted ...string) {
	t.Helper()
	for _, w := range unwanted {
		if strings.Contains(body, w) {
			t.Errorf("body contains %q\n%s", w, excerpt(body))
		}
	}
}

func excerpt(body string) string {
	// Drop the inline stylesheet so failures stay readable.
	body = styleRe.ReplaceAllString(body, "<style>…</style>")
	if len(body) > 3000 {
		body = body[:3000] + "…"
	}
	return body
}

func wantStatus(t *testing.T, w *httptest.ResponseRecorder, code int) {
	t.Helper()
	if w.Code != code {
		t.Fatalf("status = %d, want %d\n%s", w.Code, code, excerpt(w.Body.String()))
	}
}

func fileExists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return err == nil
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var _ = fmt.Sprintf

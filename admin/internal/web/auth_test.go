package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestHealthzNeedsNoSession(t *testing.T) {
	hn := newHarness(t)
	w := hn.serve(httptest.NewRequest(http.MethodGet, "/healthz", nil))
	wantStatus(t, w, 200)
	if got := strings.TrimSpace(w.Body.String()); got != "ok" {
		t.Fatalf("body = %q", got)
	}
}

// CA-17 / RF-13: every route but /login and /healthz refuses without a valid session.
func TestNoSessionRoutesRedirectOrReject(t *testing.T) {
	hn := newHarness(t)
	routes := []struct{ method, path string }{
		{"GET", "/"}, {"GET", "/pages/new"}, {"POST", "/pages"}, {"GET", "/pages/abc"},
		{"POST", "/pages/abc"}, {"POST", "/pages/abc/delete"}, {"GET", "/pages/abc/preview"},
		{"POST", "/logout"}, {"GET", "/assets/fonts/archivo.woff2"}, {"GET", "/no-such-route"},
	}
	for _, rt := range routes {
		t.Run("navigation "+rt.method+" "+rt.path, func(t *testing.T) {
			if strings.HasSuffix(rt.path, "/preview") {
				t.Skip("the preview path answers a frameable 401 notice: see TestSessionlessFrameRequestsGetAFrameableNotice")
			}
			w := hn.serve(navigate(httptest.NewRequest(rt.method, rt.path, nil)))
			wantStatus(t, w, http.StatusSeeOther)
			if loc := w.Header().Get("Location"); loc != "/login" {
				t.Fatalf("Location = %q", loc)
			}
		})
		t.Run("fetch "+rt.method+" "+rt.path, func(t *testing.T) {
			wantStatus(t, hn.serve(fetchReq(httptest.NewRequest(rt.method, rt.path, nil))), http.StatusUnauthorized)
		})
		t.Run("bare client "+rt.method+" "+rt.path, func(t *testing.T) {
			// curl: no Sec-Fetch-Mode, Accept */*
			r := httptest.NewRequest(rt.method, rt.path, nil)
			r.Header.Set("Accept", "*/*")
			wantStatus(t, hn.serve(r), http.StatusUnauthorized)
		})
	}
}

func TestSessionFetchModeWinsOverAccept(t *testing.T) {
	hn := newHarness(t)
	r := httptest.NewRequest(http.MethodPost, "/pages", nil)
	r.Header.Set("Sec-Fetch-Mode", "same-origin")
	r.Header.Set("Accept", "text/html")
	wantStatus(t, hn.serve(r), http.StatusUnauthorized)
}

func TestTamperedAndExpiredSessionsAreRejected(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()

	bad := session{cookie: &http.Cookie{Name: s.cookie.Name, Value: s.cookie.Value + "x"}}
	wantStatus(t, hn.get("/", bad), http.StatusSeeOther)

	wantStatus(t, hn.get("/", s), 200)
	hn.clock.Advance(12*time.Hour + time.Minute)
	w := hn.get("/", s)
	wantStatus(t, w, http.StatusSeeOther)
}

func TestLoginPageIsStandalone(t *testing.T) {
	hn := newHarness(t)
	w := hn.serve(navigate(httptest.NewRequest(http.MethodGet, "/login", nil)))
	wantStatus(t, w, 200)
	body := w.Body.String()
	mustContain(t, body, `lang="en"`, `name="user"`, `name="password"`, `type="password"`, `autocomplete="current-password"`)
	mustNotContainRaw(t, body, "/assets/", "@font-face", ".woff2")
	for _, c := range w.Result().Cookies() {
		if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode {
			t.Errorf("cookie %s is not HttpOnly+Secure+SameSite=Strict: %+v", c.Name, c)
		}
	}
}

func TestLoginSuccessAndFailureAreLogged(t *testing.T) {
	hn := newHarness(t)
	w := hn.postLogin(testUser, "wrong password", "203.0.113.5")
	wantStatus(t, w, http.StatusUnauthorized)
	mustContain(t, w.Body.String(), "Wrong user or password")
	for _, c := range w.Result().Cookies() {
		if c.Name == "backsoon_session" {
			t.Fatal("failed login must not set a session")
		}
	}
	mustContain(t, hn.logs.String(), "login failed", "ip=203.0.113.5")
	mustNotContain(t, hn.logs.String(), "wrong password", testPassword)

	w = hn.postLogin(testUser, testPassword, "203.0.113.5")
	wantStatus(t, w, http.StatusSeeOther)
	if loc := w.Header().Get("Location"); loc != "/" {
		t.Fatalf("Location = %q", loc)
	}
	var sess *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "backsoon_session" {
			sess = c
		}
	}
	if sess == nil || !sess.HttpOnly || !sess.Secure || sess.SameSite != http.SameSiteStrictMode || sess.MaxAge <= 0 || sess.MaxAge > 12*3600 {
		t.Fatalf("bad session cookie: %+v", sess)
	}
	mustContain(t, hn.logs.String(), "login ok", "ip=203.0.113.5")
	mustNotContain(t, hn.logs.String(), testPassword)
}

func TestLoginRedirectsWhenAlreadySignedIn(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	w := hn.get("/login", s)
	wantStatus(t, w, http.StatusSeeOther)
	if w.Header().Get("Location") != "/" {
		t.Fatalf("Location = %q", w.Header().Get("Location"))
	}
}

// Login CSRF: signed double-submit cookie plus Sec-Fetch-Site.
func TestLoginCSRFProtection(t *testing.T) {
	hn := newHarness(t)
	cookie, token := hn.loginForm("")

	post := func(mod func(*http.Request, url.Values)) *httptest.ResponseRecorder {
		form := url.Values{"user": {testUser}, "password": {testPassword}, "login_token": {token}}
		r := navigate(httptest.NewRequest(http.MethodPost, "/login", nil))
		r.AddCookie(&http.Cookie{Name: cookie.Name, Value: cookie.Value})
		mod(r, form)
		r.Body = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode())).Body
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return hn.serve(r)
	}

	cases := map[string]func(*http.Request, url.Values){
		"missing field":     func(_ *http.Request, f url.Values) { f.Del("login_token") },
		"wrong field":       func(_ *http.Request, f url.Values) { f.Set("login_token", "nope") },
		"missing cookie":    func(r *http.Request, _ url.Values) { r.Header.Del("Cookie") },
		"tampered cookie":   func(r *http.Request, _ url.Values) { r.Header.Set("Cookie", cookie.Name+"="+cookie.Value+"x") },
		"cross-site":        func(r *http.Request, _ url.Values) { r.Header.Set("Sec-Fetch-Site", "cross-site") },
		"same-site sibling": func(r *http.Request, _ url.Values) { r.Header.Set("Sec-Fetch-Site", "same-site") },
	}
	for name, mod := range cases {
		t.Run(name, func(t *testing.T) {
			w := post(mod)
			wantStatus(t, w, http.StatusForbidden)
			for _, c := range w.Result().Cookies() {
				if c.Name == "backsoon_session" {
					t.Fatal("rejected login must not set a session")
				}
			}
		})
	}
	t.Run("same-origin passes", func(t *testing.T) {
		wantStatus(t, post(func(r *http.Request, _ url.Values) { r.Header.Set("Sec-Fetch-Site", "same-origin") }), http.StatusSeeOther)
	})
}

// CA-24: the sixth failed attempt in 15 minutes is rejected even with the right password.
func TestLoginRateLimit(t *testing.T) {
	hn := newHarness(t)
	for i := 1; i <= 5; i++ {
		w := hn.postLogin(testUser, "bad", "203.0.113.9")
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d", i, w.Code)
		}
	}
	w := hn.postLogin(testUser, testPassword, "203.0.113.9")
	wantStatus(t, w, http.StatusTooManyRequests)
	mustContain(t, w.Body.String(), "Too many sign-in attempts", "15 minutes")
	if ra := w.Header().Get("Retry-After"); ra != "900" {
		t.Fatalf("Retry-After = %q", ra)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == "backsoon_session" {
			t.Fatal("blocked login must not set a session")
		}
	}
	mustContain(t, hn.logs.String(), "login blocked", "ip=203.0.113.9")

	// A different client is not affected.
	wantStatus(t, hn.postLogin(testUser, testPassword, "203.0.113.10"), http.StatusSeeOther)

	// The wait text follows the clock.
	hn.clock.Advance(10 * time.Minute)
	w = hn.postLogin(testUser, testPassword, "203.0.113.9")
	wantStatus(t, w, http.StatusTooManyRequests)
	mustContain(t, w.Body.String(), "5 minutes")

	hn.clock.Advance(5*time.Minute + time.Second)
	wantStatus(t, hn.postLogin(testUser, testPassword, "203.0.113.9"), http.StatusSeeOther)
}

func TestLoginRateLimitFallsBackToRemoteAddr(t *testing.T) {
	hn := newHarness(t)
	for i := 0; i < 5; i++ {
		hn.postLogin(testUser, "bad", "")
	}
	wantStatus(t, hn.postLogin(testUser, testPassword, ""), http.StatusTooManyRequests)
	// The same RemoteAddr host with another source port is the same client.
	cookie, token := hn.loginForm("")
	form := url.Values{"user": {testUser}, "password": {testPassword}, "login_token": {token}}
	r := navigate(httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode())))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: cookie.Name, Value: cookie.Value})
	r.RemoteAddr = "198.51.100.7:55555"
	wantStatus(t, hn.serve(r), http.StatusTooManyRequests)
}

func TestClientIP(t *testing.T) {
	npm := []netip.Prefix{netip.MustParsePrefix("172.18.0.0/16"), netip.MustParsePrefix("fd00::/8")}
	cases := []struct {
		name, remote, realIP, want string
		trusted                    []netip.Prefix
	}{
		{"x-real-ip wins", "10.0.0.2:5000", "203.0.113.9", "203.0.113.9", nil},
		{"remote host without port", "10.0.0.2:5000", "", "10.0.0.2", nil},
		{"garbage x-real-ip is ignored", "10.0.0.2:5000", "not an ip", "10.0.0.2", nil},
		{"ipv6", "[2001:db8::1]:443", "", "2001:db8::1", nil},
		{"ipv6 x-real-ip is canonical", "10.0.0.2:5000", "2001:DB8:0::1", "2001:db8::1", nil},
		{"remote without port", "10.0.0.2", "", "10.0.0.2", nil},
		{"trusted peer: x-real-ip wins", "172.18.0.3:5000", "203.0.113.9", "203.0.113.9", npm},
		{"trusted ipv6 peer: x-real-ip wins", "[fd00::3]:5000", "203.0.113.9", "203.0.113.9", npm},
		{"trusted ipv4-mapped peer: x-real-ip wins", "[::ffff:172.18.0.3]:5000", "203.0.113.9", "203.0.113.9", npm},
		{"untrusted peer: x-real-ip is ignored", "172.19.0.4:5000", "203.0.113.9", "172.19.0.4", npm},
		{"untrusted peer without port", "172.19.0.4", "203.0.113.9", "172.19.0.4", npm},
		{"trusted peer, garbage x-real-ip", "172.18.0.3:5000", "not an ip", "172.18.0.3", npm},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = c.remote
			if c.realIP != "" {
				r.Header.Set("X-Real-IP", c.realIP)
			}
			if got := clientIP(r, c.trusted); got != c.want {
				t.Fatalf("clientIP = %q, want %q", got, c.want)
			}
		})
	}
}

// A peer outside BACKSOON_TRUSTED_PROXIES cannot dodge the limit by forging X-Real-IP.
func TestLoginRateLimitIgnoresForgedRealIPFromUntrustedPeer(t *testing.T) {
	hn := newHarnessTrusting(t, netip.MustParsePrefix("10.0.0.0/8")) // the harness peer is 198.51.100.7
	for i := 0; i < 5; i++ {
		wantStatus(t, hn.postLogin(testUser, "bad", fmt.Sprintf("203.0.113.%d", i+1)), http.StatusUnauthorized)
	}
	wantStatus(t, hn.postLogin(testUser, testPassword, "203.0.113.99"), http.StatusTooManyRequests)
	if logs := hn.logs.String(); !strings.Contains(logs, "login blocked ip=198.51.100.7") {
		t.Fatalf("the block must be keyed on the peer address: %q", logs)
	}
}

// A trusted proxy keeps one counter per forwarded client.
func TestLoginRateLimitUsesRealIPFromTrustedPeer(t *testing.T) {
	hn := newHarnessTrusting(t, netip.MustParsePrefix("198.51.100.0/24"))
	for i := 0; i < 5; i++ {
		hn.postLogin(testUser, "bad", "203.0.113.9")
	}
	wantStatus(t, hn.postLogin(testUser, testPassword, "203.0.113.9"), http.StatusTooManyRequests)
	wantStatus(t, hn.postLogin(testUser, testPassword, "203.0.113.10"), http.StatusSeeOther)
}

func TestLogout(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()

	// CA-21 applies to logout too.
	wantStatus(t, hn.postForm("/logout", s, url.Values{"csrf": {"-"}}), http.StatusForbidden)
	wantStatus(t, hn.postForm("/logout", s, url.Values{"csrf": {"wrong"}}), http.StatusForbidden)

	w := hn.postForm("/logout", s, url.Values{})
	wantStatus(t, w, http.StatusSeeOther)
	if w.Header().Get("Location") != "/login" {
		t.Fatalf("Location = %q", w.Header().Get("Location"))
	}
	var cleared bool
	for _, c := range w.Result().Cookies() {
		if c.Name == "backsoon_session" && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("logout did not clear the session cookie")
	}

	// A copy of the old cookie no longer opens the panel.
	for _, w := range []*httptest.ResponseRecorder{hn.get("/", s), hn.postForm("/pages/abc/delete", s, url.Values{})} {
		wantStatus(t, w, http.StatusSeeOther)
		if w.Header().Get("Location") != "/login" {
			t.Fatalf("old cookie after logout: Location = %q", w.Header().Get("Location"))
		}
	}

	// Signing in again gives a working session.
	wantStatus(t, hn.get("/", hn.login()), http.StatusOK)
}

var cspNonceRe = regexp.MustCompile(`style-src 'nonce-([^']+)'`)

func TestPanelSecurityHeaders(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	for _, path := range []string{"/", "/pages/new", "/pages/default", "/pages/missing"} {
		w := hn.get(path, s)
		csp := w.Header().Get("Content-Security-Policy")
		for _, want := range []string{
			"default-src 'none'", "script-src 'none'", "font-src 'self'", "img-src 'self' data:",
			"frame-src 'self'", "form-action 'self'", "frame-ancestors 'none'", "base-uri 'none'",
		} {
			if !strings.Contains(csp, want) {
				t.Errorf("%s: CSP lacks %q: %s", path, want, csp)
			}
		}
		if strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "unsafe-eval") {
			t.Errorf("%s: CSP allows unsafe sources: %s", path, csp)
		}
		m := cspNonceRe.FindStringSubmatch(csp)
		if m == nil || !strings.Contains(w.Body.String(), `<style nonce="`+m[1]+`">`) {
			t.Errorf("%s: style nonce in CSP does not match the inline stylesheet (%s)", path, csp)
		}
		for h, want := range map[string]string{
			"X-Content-Type-Options": "nosniff", "Referrer-Policy": "no-referrer", "Cache-Control": "no-store",
		} {
			if got := w.Header().Get(h); got != want {
				t.Errorf("%s: %s = %q, want %q", path, h, got, want)
			}
		}
	}
}

func TestNonceChangesPerResponse(t *testing.T) {
	hn := newHarness(t)
	a := cspNonceRe.FindStringSubmatch(hn.serve(navigate(httptest.NewRequest("GET", "/login", nil))).Header().Get("Content-Security-Policy"))
	b := cspNonceRe.FindStringSubmatch(hn.serve(navigate(httptest.NewRequest("GET", "/login", nil))).Header().Get("Content-Security-Policy"))
	if a == nil || b == nil || a[1] == b[1] {
		t.Fatalf("nonces must differ per response: %v %v", a, b)
	}
}

// A link clicked inside the sandboxed preview navigates the frame without the Strict cookie. The
// frame must show a plain notice, not a redirect to the login page (which refuses framing).
func TestSessionlessFrameRequestsGetAFrameableNotice(t *testing.T) {
	hn := newHarness(t)
	site := hn.createPage("Alpha", "app.test")
	for name, mod := range map[string]func(*http.Request){
		"iframe destination": func(r *http.Request) { r.Header.Set("Sec-Fetch-Dest", "iframe") },
		"preview path":       func(r *http.Request) {},
	} {
		t.Run(name, func(t *testing.T) {
			r := navigate(httptest.NewRequest(http.MethodGet, "/pages/"+site.ID+"/preview", nil))
			mod(r)
			w := hn.serve(r)
			wantStatus(t, w, http.StatusUnauthorized)
			if w.Header().Get("Location") != "" {
				t.Fatal("no redirect expected")
			}
			csp := w.Header().Get("Content-Security-Policy")
			if csp != frameNoticeCSP || !strings.Contains(csp, "frame-ancestors 'self'") || strings.Contains(csp, "script") {
				t.Fatalf("CSP = %q", csp)
			}
			body := w.Body.String()
			mustContain(t, body, "Links inside the preview don't work", "reopen the preview", `lang="en"`)
			mustNotContain(t, body, "app.test", "hello", site.ID)
		})
	}
	r := navigate(httptest.NewRequest(http.MethodGet, "/", nil))
	r.Header.Set("Sec-Fetch-Dest", "iframe")
	wantStatus(t, hn.serve(r), http.StatusUnauthorized)
}

package web

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// The panel speaks English and Spanish. Every response picks its language from, in order, the
// backsoon_lang cookie, the Accept-Language header and English; the ES/EN links of the navbar and
// the login page set the cookie through GET /lang, a route that needs no session.
const (
	langCookieName   = "backsoon_lang"
	langCookieMaxAge = 365 * 24 * 3600
	langRoute        = "/lang"
	langEN           = "en"
	langES           = "es"
	nextMaxLen       = 2048
	varyLocalized    = "Accept-Language, Cookie"
)

func supportedLang(tag string) bool { return tag == langEN || tag == langES }

// locale is the language of one response; its T looks messages up in the catalogue.
type locale string

// T returns the message for key in this language, formatted with args. A missing key shows up as
// ⟦key⟧ so it cannot go unnoticed (the catalogue test keeps both languages complete).
func (l locale) T(key string, args ...any) string {
	msg, ok := catalog[string(l)][key]
	if !ok {
		return "⟦" + key + "⟧"
	}
	if len(args) == 0 {
		return msg
	}
	return fmt.Sprintf(msg, args...)
}

// localeOf resolves the language of a request: a valid cookie, then Accept-Language, then English.
func localeOf(r *http.Request) locale {
	if c, err := r.Cookie(langCookieName); err == nil && supportedLang(c.Value) {
		return locale(c.Value)
	}
	if tag := acceptedLang(r.Header.Get("Accept-Language")); tag != "" {
		return locale(tag)
	}
	return langEN
}

// acceptedLang returns the supported language the header prefers (highest q-value, header order
// on ties), or "" when it names none. A region subtag matches its primary language (es-ES → es);
// q=0, malformed weights and unsupported languages are skipped.
func acceptedLang(header string) string {
	const maxLen, maxEntries = 1024, 32
	if len(header) > maxLen {
		header = header[:maxLen]
	}
	best, bestQ := "", 0.0
	for i, part := range strings.Split(header, ",") {
		if i >= maxEntries {
			break
		}
		tag, params, _ := strings.Cut(part, ";")
		primary, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(tag)), "-")
		if !supportedLang(primary) {
			continue
		}
		q, ok := qValue(params)
		if !ok || q <= bestQ {
			continue
		}
		best, bestQ = primary, q
	}
	return best
}

// qValue reads the weight from the parameters of one Accept-Language entry (1 when absent).
func qValue(params string) (float64, bool) {
	for _, p := range strings.Split(params, ";") {
		name, value, found := strings.Cut(strings.TrimSpace(p), "=")
		if !found || !strings.EqualFold(strings.TrimSpace(name), "q") {
			continue
		}
		q, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil || q < 0 || q > 1 {
			return 0, false
		}
		return q, true
	}
	return 1, true
}

// safeNext keeps the return target of the selector local: a path starting with a single "/", with
// no backslash, control character, scheme or host. Anything else returns to "/".
func safeNext(next string) string {
	if next == "" || len(next) > nextMaxLen || next[0] != '/' {
		return "/"
	}
	if len(next) > 1 && next[1] == '/' {
		return "/"
	}
	for i := 0; i < len(next); i++ {
		if c := next[i]; c < 0x20 || c == 0x7f || c == '\\' {
			return "/"
		}
	}
	if u, err := url.Parse(next); err != nil || u.Scheme != "" || u.Host != "" {
		return "/"
	}
	return next
}

// returnPath is the GET page a language link on this response should come back to: the request
// itself for a GET, or the page behind a form that was answered with a rendered view.
func returnPath(r *http.Request) string {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		u := *r.URL
		q := u.Query()
		if _, known := notices[q.Get("notice")]; q.Has("notice") && !known {
			q.Del("notice") // only the fixed notice keys are worth carrying across
			u.RawQuery = q.Encode()
		}
		return safeNext(u.RequestURI())
	}
	p := r.URL.Path
	switch {
	case p == "/login":
		return "/login"
	case p == "/pages":
		return "/pages/new"
	case strings.HasSuffix(p, "/delete"):
		return safeNext(strings.TrimSuffix(p, "/delete"))
	case strings.HasPrefix(p, "/pages/"):
		return safeNext(p)
	}
	return "/"
}

// setLanguage is GET /lang?to=es|en&next=/path: it remembers the choice in a cookie and returns to
// next. It is reachable without a session (the login page needs it), so it does nothing else: an
// unsupported language changes nothing, and next can only be a local path.
func (s *Server) setLanguage(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w.Header())
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	if to := q["to"]; len(to) == 1 && supportedLang(to[0]) {
		http.SetCookie(w, &http.Cookie{
			Name: langCookieName, Value: to[0], Path: "/", MaxAge: langCookieMaxAge,
			HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
		})
	}
	http.Redirect(w, r, safeNext(q.Get("next")), http.StatusSeeOther)
}

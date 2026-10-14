package web

import (
	"net/http"
	"regexp"
	"strings"
)

// A link followed inside the sandboxed preview navigates the frame from an opaque origin, so the
// SameSite=Strict session cookie is not sent. Redirecting that request to the login form would
// show the browser's "refused to connect" page (the login form cannot be framed), so a request
// aimed at a frame, or at the preview path, gets this static notice instead. It carries no page
// data and may be framed by the panel only.
const frameNoticeCSP = "sandbox; default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'self'"

const frameNoticeHTML = `<!doctype html>
<html lang="{{LANG}}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<link rel="icon" href="data:,">
<title>{{TITLE}}</title>
<style>
:root { color-scheme: light dark; }
body { margin: 0; padding: 1.5rem; font: 1rem/1.5 system-ui, -apple-system, "Segoe UI", Roboto, sans-serif; background: Canvas; color: CanvasText; }
h1 { margin: 0 0 .5rem; font-size: 1.2rem; }
p { margin: 0; max-width: 40ch; }
</style>
</head>
<body>
<h1>{{HEADING}}</h1>
<p>{{BODY}}</p>
</body>
</html>
`

var previewPathRe = regexp.MustCompile(`^/pages/[^/]+/preview$`)

func isFrameRequest(r *http.Request) bool {
	return r.Header.Get("Sec-Fetch-Dest") == "iframe" || previewPathRe.MatchString(r.URL.Path)
}

func (s *Server) frameNotice(w http.ResponseWriter, r *http.Request) {
	loc := localeOf(r)
	page := strings.NewReplacer(
		"{{LANG}}", string(loc), "{{TITLE}}", loc.T("frame.title"),
		"{{HEADING}}", loc.T("frame.heading"), "{{BODY}}", loc.T("frame.body"),
	).Replace(frameNoticeHTML)
	h := w.Header()
	setSecurityHeaders(h)
	h.Set("Vary", varyLocalized)
	h.Set("Content-Security-Policy", frameNoticeCSP)
	h.Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	w.Write([]byte(page))
}

package web

import (
	"bytes"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/panel.css
var staticFS embed.FS

// siteStyle returns the stylesheet inlined into every page (the panel serves no static assets).
func siteStyle() (template.CSS, error) {
	css, err := fs.ReadFile(staticFS, "static/panel.css")
	if err != nil {
		return "", err
	}
	return template.CSS(css), nil
}

// parseTemplates parses the templates for one language: {{t "key" args...}} reads the catalogue.
func parseTemplates(l locale) (*template.Template, error) {
	return template.New("panel").Funcs(template.FuncMap{"t": l.T}).ParseFS(templateFS, "templates/*.html")
}

// base carries what the shared page frame needs.
type base struct {
	Title  string
	Nonce  string
	Style  template.CSS
	Authed bool
	Who    string // signed-in user name, shown in the header
	CSRF   string
	Notice *noticeView

	Lang     string // language of the response: the page's lang attribute and the selector's state
	LangNext string // where the language links return to
}

func (b *base) common() *base { return b }

// LangHref is the link of the ES/EN selector that switches the panel to lang.
func (b *base) LangHref(lang string) string {
	return langRoute + "?to=" + lang + "&next=" + url.QueryEscape(b.LangNext)
}

type viewer interface{ common() *base }

type noticeView struct {
	Text string
	Warn bool
}

// notices are the only messages a ?notice= key can show (catalogue key "notice.<key>").
var notices = map[string]bool{"created": false, "saved": false, "deleted": false, "publish-failed": true}

type loginView struct {
	base
	User  string
	Token string
	Error string
}

type destView struct {
	Value     string
	Kind      string
	IsService bool
	Hit       bool
}

// ChipLabel is the text a screen reader hears before the destination value.
func (d destView) ChipLabel() string { return d.Kind + ": " }

type entryView struct {
	ID          string
	Name        string
	UpdatedISO  string
	UpdatedText string
	Dests       []destView
	NoDests     bool
	IsDefault   bool
	Hit         bool
	Focus       bool
}

type answerView struct {
	Dest      string
	IsService bool
	PageName  string
	Note      string
}

type lookupView struct {
	Query  string
	Error  string
	Answer *answerView
}

type directoryView struct {
	base
	Lookup  lookupView
	Entries []entryView
	Empty   bool
	Focus   *focusView
}

type focusView struct {
	ID    string
	Name  string
	Title string
}

type issue struct {
	Subject string
	Text    string
}

type summaryItem struct {
	Anchor string
	Text   string
}

type keptFile struct {
	Name string
	Size string
}

type formView struct {
	base
	Edit        bool
	IsDefault   bool
	Action      string
	PageID      string
	PageName    string
	UpdatedISO  string
	UpdatedText string
	NoDests     bool
	Form        formValues
	Errs        map[string][]issue
	Summary     []summaryItem
	DraftID     string
	Kept        *keptFile
	PreviewSrc  string
	DeleteURL   string
	BackToDraft string
}

// Issues returns the problems found in one field.
func (v *formView) Issues(id string) []issue { return v.Errs[id] }

// Describe returns the aria attributes tying a control to its hint and to its error text.
func (v *formView) Describe(id string, hint bool) template.HTMLAttr {
	var ids []string
	attr := ""
	if len(v.Errs[id]) > 0 {
		attr = ` aria-invalid="true"`
		ids = append(ids, id+"-error")
	}
	if hint {
		ids = append(ids, id+"-hint")
	}
	if len(ids) > 0 {
		attr += ` aria-describedby="` + strings.Join(ids, " ") + `"`
	}
	return template.HTMLAttr(attr)
}

type moveGroup struct {
	OwnerName string
	OwnerID   string
	Dests     []destView
	LeftEmpty bool
}

type confirmView struct {
	base
	Kind        string // warnings, move or delete
	Action      string
	DraftID     string
	PageName    string
	Edit        bool
	Warnings    []string
	Groups      []moveGroup
	Dests       []destView
	BackHref    string
	DefaultName string
}

type errorView struct {
	base
	Heading   string
	Message   string
	BackHref  string
	BackLabel string
}

func newNonce() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(raw[:])
}

const panelCSPFormat = "default-src 'none'; script-src 'none'; style-src 'nonce-%s'; font-src 'self'; img-src 'self' data:; " +
	"frame-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'"

// previewCSP is the exact policy of GET /pages/{id}/preview: the document runs in a sandbox
// without scripts, may load nothing but inline styles and data: images, and may only be framed by
// the panel itself.
const previewCSP = "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:; frame-ancestors 'self'"

func setSecurityHeaders(h http.Header) {
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
}

// render executes the named template, in the language of the request, into a buffer first, so a
// template failure becomes a clean 500 instead of a half-written page.
func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, name string, v viewer) {
	loc := localeOf(r)
	b := v.common()
	b.Nonce = newNonce()
	b.Style = s.style
	b.Lang = string(loc)
	b.LangNext = returnPath(r)
	var buf bytes.Buffer
	if err := s.tpl[b.Lang].ExecuteTemplate(&buf, name, v); err != nil {
		s.log.Printf("render %s failed: %v", name, err)
		setSecurityHeaders(w.Header())
		w.Header().Set("Vary", varyLocalized)
		http.Error(w, loc.T("render.failed"), http.StatusInternalServerError)
		return
	}
	h := w.Header()
	setSecurityHeaders(h)
	h.Set("Vary", varyLocalized)
	h.Set("Content-Security-Policy", fmt.Sprintf(panelCSPFormat, b.Nonce))
	h.Set("X-Frame-Options", "DENY")
	h.Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	w.Write(buf.Bytes())
}

// Package web is the panel's HTTP layer: login, the page directory, the create/edit/delete flows
// and the sandboxed preview, rendered with html/template and no client-side script.
package web

import (
	"context"
	"errors"
	"html/template"
	"log"
	"net/http"
	"net/netip"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/jorgealonsodev/backsoon/admin/internal/auth"
	"github.com/jorgealonsodev/backsoon/admin/internal/pages"
	"github.com/jorgealonsodev/backsoon/admin/internal/users"
)

// Options configure New.
type Options struct {
	Store *pages.Store
	// Users are the users added in the panel (see users.Open).
	Users *users.Store
	// User and PasswordHash are the owner (see config.Config).
	User         string
	PasswordHash []byte
	// PasswordFingerprint identifies the configured owner password across restarts
	// (config.Config.PasswordFingerprint); the owner's sessions end when it changes. Empty means
	// the hash itself.
	PasswordFingerprint []byte
	// SessionSecret signs sessions, CSRF tokens and the login form token.
	SessionSecret []byte
	// Logger receives the plain-line log (RNF-9). Defaults to log.Default().
	Logger *log.Logger
	// Now overrides the clock (tests).
	Now func() time.Time
	// TrustedProxies are the peers whose X-Real-IP the login rate limit believes; nil means any
	// peer (see clientIP).
	TrustedProxies []netip.Prefix
}

// Server is the panel handler.
type Server struct {
	store     *pages.Store
	users     *users.Store
	ownerName string
	accounts  *auth.Accounts
	sessions  *auth.Manager
	limiter   *auth.Limiter
	drafts    *draftStore
	secret    []byte
	log       *log.Logger
	now       func() time.Time
	trusted   []netip.Prefix

	tpl           map[string]*template.Template // one parsed set per language
	style         template.CSS
	mux           *http.ServeMux
	uploadBodyMax int64
}

type sessionKey struct{}

// New builds the panel handler.
func New(o Options) (http.Handler, error) {
	switch {
	case o.Store == nil:
		return nil, errors.New("web: a page store is required")
	case o.Users == nil:
		return nil, errors.New("web: a user store is required")
	case o.User == "" || len(o.PasswordHash) == 0:
		return nil, errors.New("web: the administrator credentials are required")
	case len(o.SessionSecret) < 16:
		return nil, errors.New("web: the session secret is too short")
	}
	if o.Logger == nil {
		o.Logger = log.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	tpl := make(map[string]*template.Template, len(catalog))
	for l := range catalog {
		t, err := parseTemplates(locale(l))
		if err != nil {
			return nil, err
		}
		tpl[l] = t
	}
	style, err := siteStyle()
	if err != nil {
		return nil, err
	}
	accounts := auth.NewAccounts(o.User, o.PasswordHash, o.PasswordFingerprint, o.SessionSecret, o.Users)
	s := &Server{
		store:         o.Store,
		users:         o.Users,
		ownerName:     o.User,
		accounts:      accounts,
		sessions:      auth.NewManager(o.SessionSecret, o.Now, accounts),
		limiter:       auth.NewLimiter(auth.LoginMaxAttempts, auth.LoginWindow, o.Now),
		drafts:        newDraftStore(o.Now),
		secret:        append([]byte(nil), o.SessionSecret...),
		log:           o.Logger,
		now:           o.Now,
		trusted:       slices.Clone(o.TrustedProxies),
		tpl:           tpl,
		style:         style,
		uploadBodyMax: pages.MaxHTMLBytes + 320<<10,
	}
	s.mux = http.NewServeMux()
	s.routes()
	return s, nil
}

func (s *Server) routes() {
	m := s.mux
	m.HandleFunc("GET /healthz", s.healthz)
	m.HandleFunc(langRoute, s.setLanguage)
	m.HandleFunc("GET /login", s.loginPage)
	m.HandleFunc("POST /login", s.loginSubmit)
	m.HandleFunc("POST /logout", s.signedIn(s.logout))
	m.HandleFunc("GET /{$}", s.signedIn(s.directory))
	m.HandleFunc("GET /pages/new", s.signedIn(s.newPage))
	m.HandleFunc("POST /pages", s.signedIn(s.createPage))
	m.HandleFunc("GET /pages/{id}", s.signedIn(s.editPage))
	m.HandleFunc("POST /pages/{id}", s.signedIn(s.updatePage))
	m.HandleFunc("POST /pages/{id}/delete", s.signedIn(s.deletePage))
	m.HandleFunc("GET /pages/{id}/preview", s.signedIn(s.preview))
	m.HandleFunc("GET /users", s.ownerOnly(s.listUsers))
	m.HandleFunc("POST /users", s.ownerOnly(s.createUser))
	m.HandleFunc("GET /users/password", s.ownerOnly(s.userPasswordForm))
	m.HandleFunc("POST /users/password", s.ownerOnly(s.setUserPassword))
	m.HandleFunc("POST /users/delete", s.ownerOnly(s.deleteUser))
	m.HandleFunc("GET /account", s.signedIn(s.account))
	m.HandleFunc("POST /account/password", s.signedIn(s.changeOwnPassword))
	m.HandleFunc("/", s.signedIn(s.notFound))
}

// ServeHTTP is the session gate: nothing but /login, /healthz and the language selector (/lang) is
// reachable without a valid session, whatever the route or method, including unknown paths.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/healthz", "/login", langRoute:
		s.mux.ServeHTTP(w, r)
		return
	}
	sess, err := s.sessions.FromRequest(r)
	if err != nil {
		s.unauthorized(w, r)
		return
	}
	// Paths the mux would redirect to a cleaned form are not routes of the panel.
	if p := r.URL.Path; p != path.Clean(p) {
		s.notFound(w, r, sess)
		return
	}
	s.mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionKey{}, sess)))
}

// signedIn adapts a handler that needs the verified session from the gate.
func (s *Server) signedIn(h func(http.ResponseWriter, *http.Request, auth.Session)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok := r.Context().Value(sessionKey{}).(auth.Session)
		if !ok {
			s.unauthorized(w, r)
			return
		}
		h(w, r, sess)
	}
}

// unauthorized answers a request without a valid session: a browser navigation goes to the login
// form, anything else (fetch, scripts, curl) gets 401.
func (s *Server) unauthorized(w http.ResponseWriter, r *http.Request) {
	if isFrameRequest(r) {
		s.frameNotice(w, r)
		return
	}
	setSecurityHeaders(w.Header())
	if isNavigation(r) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	w.Header().Set("Vary", varyLocalized)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	w.Write([]byte(localeOf(r).T("plain.401")))
}

func isNavigation(r *http.Request) bool {
	mode := r.Header.Get("Sec-Fetch-Mode")
	return (mode == "" || mode == "navigate") && strings.Contains(r.Header.Get("Accept"), "text/html")
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	setSecurityHeaders(h)
	h.Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte("ok\n"))
}

func (s *Server) sessionOf(r *http.Request) auth.Session {
	sess, _ := r.Context().Value(sessionKey{}).(auth.Session)
	return sess
}

func (s *Server) base(sess auth.Session, title string) base {
	return base{Title: title + " · backsoon", Authed: true, Who: sess.User, Owner: sess.Owner, CSRF: s.sessions.CSRFToken(sess)}
}

func (s *Server) errorPage(w http.ResponseWriter, r *http.Request, sess auth.Session, status int, heading, message, backHref, backLabel string) {
	s.render(w, r, status, "error", &errorView{
		base: s.base(sess, heading), Heading: heading, Message: message, BackHref: backHref, BackLabel: backLabel,
	})
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	loc := localeOf(r)
	s.errorPage(w, r, sess, http.StatusNotFound, loc.T("err.notfound.title"), loc.T("err.notfound.body"), "/", loc.T("back.dir"))
}

func (s *Server) internalError(w http.ResponseWriter, r *http.Request, sess auth.Session, what string, err error) {
	s.log.Printf("%s failed: %v", what, err)
	loc := localeOf(r)
	s.errorPage(w, r, sess, http.StatusInternalServerError, loc.T("err.internal.title"), loc.T("err.internal.body"), "/", loc.T("back.dir"))
}

// csrfFailed answers a state-changing request without a valid token (CA-21).
func (s *Server) csrfFailed(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	loc := localeOf(r)
	s.errorPage(w, r, sess, http.StatusForbidden, loc.T("err.csrf.title"), loc.T("err.csrf.body"), "/", loc.T("back.dir"))
}

func (s *Server) validCSRF(sess auth.Session, r *http.Request) bool {
	token := r.PostFormValue("csrf")
	return token != "" && s.sessions.VerifyCSRF(sess, token)
}

// Package web is the panel's HTTP layer: login, the page directory, the create/edit/delete flows
// and the sandboxed preview, rendered with html/template and no client-side script.
package web

import (
	"context"
	"errors"
	"html/template"
	"log"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/jorgealonsodev/backsoon/admin/internal/auth"
	"github.com/jorgealonsodev/backsoon/admin/internal/pages"
)

// Options configure New.
type Options struct {
	Store *pages.Store
	// User and PasswordHash are the single administrator (see config.Config).
	User         string
	PasswordHash []byte
	// SessionSecret signs sessions, CSRF tokens and the login form token.
	SessionSecret []byte
	// Logger receives the plain-line log (RNF-9). Defaults to log.Default().
	Logger *log.Logger
	// Now overrides the clock (tests).
	Now func() time.Time
}

// Server is the panel handler.
type Server struct {
	store    *pages.Store
	creds    *auth.Credentials
	sessions *auth.Manager
	limiter  *auth.Limiter
	drafts   *draftStore
	secret   []byte
	user     string
	log      *log.Logger
	now      func() time.Time

	tpl           *template.Template
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
	tpl, err := parseTemplates()
	if err != nil {
		return nil, err
	}
	style, err := siteStyle()
	if err != nil {
		return nil, err
	}
	s := &Server{
		store:         o.Store,
		creds:         auth.NewCredentials(o.User, o.PasswordHash),
		sessions:      auth.NewManager(o.SessionSecret, o.Now),
		limiter:       auth.NewLimiter(auth.LoginMaxAttempts, auth.LoginWindow, o.Now),
		drafts:        newDraftStore(o.Now),
		secret:        append([]byte(nil), o.SessionSecret...),
		user:          o.User,
		log:           o.Logger,
		now:           o.Now,
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
	m.HandleFunc("/", s.signedIn(s.notFound))
}

// ServeHTTP is the session gate: nothing but /login and /healthz is reachable without a valid
// session, whatever the route or method, including unknown paths.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/healthz", "/login":
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
		s.frameNotice(w)
		return
	}
	setSecurityHeaders(w.Header())
	if isNavigation(r) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	w.Write([]byte("Authentication required. Sign in at /login.\n"))
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
	return base{Title: title + " · backsoon", Authed: true, Who: s.user, CSRF: s.sessions.CSRFToken(sess)}
}

func (s *Server) errorPage(w http.ResponseWriter, sess auth.Session, status int, heading, message, backHref, backLabel string) {
	s.render(w, status, "error", &errorView{
		base: s.base(sess, heading), Heading: heading, Message: message, BackHref: backHref, BackLabel: backLabel,
	})
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	s.errorPage(w, sess, http.StatusNotFound, "Page not found", "There is nothing at this address.", "/", "Back to the directory")
}

func (s *Server) internalError(w http.ResponseWriter, sess auth.Session, what string, err error) {
	s.log.Printf("%s failed: %v", what, err)
	s.errorPage(w, sess, http.StatusInternalServerError, "Something went wrong",
		"The change was not applied. Try again; if it keeps failing, check the panel's log.", "/", "Back to the directory")
}

// csrfFailed answers a state-changing request without a valid token (CA-21).
func (s *Server) csrfFailed(w http.ResponseWriter, sess auth.Session) {
	s.errorPage(w, sess, http.StatusForbidden, "This form has expired",
		"The form expired or did not come from this panel, so nothing was changed. Reload the page and try again.", "/", "Back to the directory")
}

func (s *Server) validCSRF(sess auth.Session, r *http.Request) bool {
	token := r.PostFormValue("csrf")
	return token != "" && s.sessions.VerifyCSRF(sess, token)
}

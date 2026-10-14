package web

import (
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/jorgealonsodev/backsoon/admin/internal/auth"
	"github.com/jorgealonsodev/backsoon/admin/internal/users"
)

// User management (S3, S4, S6). The owner manages the users; every signed-in editor changes their
// own password. A user name travels in a form field or the query, never in the path: "." and ".."
// are valid names, and the session gate refuses paths that need cleaning.

const (
	// ownPasswordKeyPrefix keys the limiter of the current-password check, one budget per user.
	ownPasswordKeyPrefix = "password:"
	accountNotice        = "/account?notice=password-changed"
)

// fieldErrors are the problems found in the fields of a form, by field id.
type fieldErrors map[string][]issue

// Issues returns the problems found in one field.
func (e fieldErrors) Issues(id string) []issue { return e[id] }

// Describe returns the aria attributes tying a control to its hint and to its error text.
func (e fieldErrors) Describe(id string, hint bool) template.HTMLAttr {
	var ids []string
	attr := ""
	if len(e[id]) > 0 {
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

// summarize lists the problems in the order of fields; labels are catalogue keys.
func (e fieldErrors) summarize(loc locale, fields ...string) []summaryItem {
	var out []summaryItem
	for _, f := range fields {
		if issues := e[f]; len(issues) > 0 {
			out = append(out, summaryItem{Anchor: f, Text: loc.T("user.field."+f) + ": " + issues[0].Text})
		}
	}
	return out
}

type userRow struct {
	Name        string
	CreatedISO  string
	CreatedText string
	ChangedISO  string
	ChangedText string
}

type usersView struct {
	base
	Owner   string
	Users   []userRow
	Name    string // the name typed in the add form
	Errs    fieldErrors
	Summary []summaryItem
}

// userActionView is the confirmation of setting a password ("password") or deleting ("delete").
type userActionView struct {
	base
	Kind    string
	User    string
	Errs    fieldErrors
	Summary []summaryItem
}

type accountView struct {
	base
	IsOwner bool
	Errs    fieldErrors
	Summary []summaryItem
}

var (
	createFields   = []string{"name", "password", "confirm_password"}
	passwordFields = []string{"password", "confirm_password"}
	ownFields      = []string{"current", "password", "confirm_password"}
)

// ownerOnly lets the handler run only for the owner: an editor gets 403 before anything is read
// from the request, so nothing can change (S6).
func (s *Server) ownerOnly(h func(http.ResponseWriter, *http.Request, auth.Session)) http.HandlerFunc {
	return s.signedIn(func(w http.ResponseWriter, r *http.Request, sess auth.Session) {
		if !sess.Owner {
			s.log.Printf("forbidden user=%q %s %s", sess.User, r.Method, r.URL.Path)
			loc := localeOf(r)
			s.errorPage(w, r, sess, http.StatusForbidden, loc.T("err.forbidden.title"), loc.T("err.forbidden.users"), "/", loc.T("back.dir"))
			return
		}
		h(w, r, sess)
	})
}

// isOwnerName reports whether name is the owner's, which the panel never deletes or re-passwords.
func (s *Server) isOwnerName(name string) bool { return strings.EqualFold(name, s.ownerName) }

func (s *Server) ownerProtected(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	loc := localeOf(r)
	s.errorPage(w, r, sess, http.StatusForbidden, loc.T("err.owner.title"), loc.T("err.owner.body"), "/users", loc.T("back.users"))
}

// readPost parses a small form and checks its CSRF token; it answers 403 itself when it fails.
func (s *Server) readPost(w http.ResponseWriter, r *http.Request, sess auth.Session) bool {
	r.Body = http.MaxBytesReader(w, r.Body, smallBodyMax)
	if err := r.ParseForm(); err != nil || !s.validCSRF(sess, r) {
		s.csrfFailed(w, r, sess)
		return false
	}
	return true
}

func (s *Server) newUsersView(r *http.Request, sess auth.Session, name string, errs fieldErrors) *usersView {
	loc := localeOf(r)
	v := &usersView{base: s.base(sess, loc.T("title.users")), Owner: s.ownerName, Name: name, Errs: errs}
	v.Nav = "users"
	v.Notice = noticeFor(loc, r.URL.Query().Get("notice"))
	for _, u := range s.users.List() {
		row := userRow{Name: u.Name}
		row.CreatedISO, row.CreatedText = dateParts(u.Created)
		row.ChangedISO, row.ChangedText = dateParts(u.PasswordChanged)
		v.Users = append(v.Users, row)
	}
	if errs == nil {
		v.Errs = fieldErrors{}
	}
	v.Summary = v.Errs.summarize(loc, createFields...)
	return v
}

// listUsers is GET /users.
func (s *Server) listUsers(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	s.render(w, r, http.StatusOK, "users", s.newUsersView(r, sess, "", nil))
}

// createUser is POST /users.
func (s *Server) createUser(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	if !s.readPost(w, r, sess) {
		return
	}
	loc := localeOf(r)
	name, password := r.PostFormValue("name"), r.PostFormValue("password")
	errs := fieldErrors{}
	status := http.StatusUnprocessableEntity
	if password != r.PostFormValue("confirm_password") {
		errs["confirm_password"] = []issue{{Text: loc.T("val.user.password.mismatch")}}
	} else if _, err := s.users.Create(name, password); err != nil {
		switch {
		case errors.Is(err, users.ErrInvalidName):
			errs["name"] = []issue{{Text: loc.T("val.user.name.invalid")}}
		case errors.Is(err, users.ErrOwnerName):
			errs["name"] = []issue{{Text: loc.T("val.user.name.owner")}}
		case errors.Is(err, users.ErrExists):
			errs["name"] = []issue{{Text: loc.T("val.user.exists")}}
			status = http.StatusConflict
		case errors.Is(err, users.ErrPasswordShort), errors.Is(err, users.ErrPasswordLong):
			errs["password"] = []issue{{Text: passwordIssue(loc, err)}}
		default:
			s.internalError(w, r, sess, "create user", err)
			return
		}
	}
	if len(errs) > 0 {
		s.render(w, r, status, "users", s.newUsersView(r, sess, name, errs))
		return
	}
	s.log.Printf("user created by=%q user=%q", sess.User, strings.ToLower(name))
	http.Redirect(w, r, "/users?notice=user-created", http.StatusSeeOther)
}

func passwordIssue(loc locale, err error) string {
	if errors.Is(err, users.ErrPasswordLong) {
		return loc.T("val.user.password.long")
	}
	return loc.T("val.user.password.short")
}

// target resolves the user a request names. It answers 403 for the owner and 404 for anybody
// that is not stored, and returns false then.
func (s *Server) target(w http.ResponseWriter, r *http.Request, sess auth.Session, name string) (users.User, bool) {
	if s.isOwnerName(name) {
		s.ownerProtected(w, r, sess)
		return users.User{}, false
	}
	u, ok := s.users.Lookup(name)
	if !ok {
		s.notFound(w, r, sess)
		return users.User{}, false
	}
	return u, true
}

func (s *Server) newActionView(r *http.Request, sess auth.Session, kind string, u users.User, errs fieldErrors) *userActionView {
	loc := localeOf(r)
	title := loc.T("title.userpassword", u.Name)
	if kind == "delete" {
		title = loc.T("title.userdelete", u.Name)
	}
	v := &userActionView{base: s.base(sess, title), Kind: kind, User: u.Name, Errs: errs}
	v.Nav = "users"
	if errs == nil {
		v.Errs = fieldErrors{}
	}
	v.Summary = v.Errs.summarize(loc, passwordFields...)
	return v
}

// userPasswordForm is GET /users/password?user=NAME: the confirmation of setting a password.
func (s *Server) userPasswordForm(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	u, ok := s.target(w, r, sess, r.URL.Query().Get("user"))
	if !ok {
		return
	}
	s.render(w, r, http.StatusOK, "userpassword", s.newActionView(r, sess, "password", u, nil))
}

// setUserPassword is POST /users/password. Without confirm=true it only shows the confirmation.
func (s *Server) setUserPassword(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	if !s.readPost(w, r, sess) {
		return
	}
	loc := localeOf(r)
	u, ok := s.target(w, r, sess, r.PostFormValue("user"))
	if !ok {
		return
	}
	if r.PostFormValue("confirm") != "true" {
		s.render(w, r, http.StatusOK, "userpassword", s.newActionView(r, sess, "password", u, nil))
		return
	}
	password := r.PostFormValue("password")
	errs := fieldErrors{}
	if password != r.PostFormValue("confirm_password") {
		errs["confirm_password"] = []issue{{Text: loc.T("val.user.password.mismatch")}}
	} else if err := s.users.SetPassword(u.Name, password); err != nil {
		switch {
		case errors.Is(err, users.ErrPasswordShort), errors.Is(err, users.ErrPasswordLong):
			errs["password"] = []issue{{Text: passwordIssue(loc, err)}}
		case errors.Is(err, users.ErrNotFound):
			s.notFound(w, r, sess)
			return
		default:
			s.internalError(w, r, sess, "set user password", err)
			return
		}
	}
	if len(errs) > 0 {
		s.render(w, r, http.StatusUnprocessableEntity, "userpassword", s.newActionView(r, sess, "password", u, errs))
		return
	}
	s.log.Printf("user password set by=%q user=%q", sess.User, u.Name)
	http.Redirect(w, r, "/users?notice=user-password", http.StatusSeeOther)
}

// deleteUser is POST /users/delete. Without confirm=true it only shows the confirmation.
func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	if !s.readPost(w, r, sess) {
		return
	}
	u, ok := s.target(w, r, sess, r.PostFormValue("user"))
	if !ok {
		return
	}
	if r.PostFormValue("confirm") != "true" {
		s.render(w, r, http.StatusOK, "userdelete", s.newActionView(r, sess, "delete", u, nil))
		return
	}
	if err := s.users.Delete(u.Name); err != nil {
		if errors.Is(err, users.ErrNotFound) {
			s.notFound(w, r, sess)
			return
		}
		s.internalError(w, r, sess, "delete user", err)
		return
	}
	s.log.Printf("user deleted by=%q user=%q", sess.User, u.Name)
	http.Redirect(w, r, "/users?notice=user-deleted", http.StatusSeeOther)
}

func (s *Server) newAccountView(r *http.Request, sess auth.Session, errs fieldErrors) *accountView {
	loc := localeOf(r)
	v := &accountView{base: s.base(sess, loc.T("title.account")), IsOwner: sess.Owner, Errs: errs}
	v.Nav = "account"
	v.Notice = noticeFor(loc, r.URL.Query().Get("notice"))
	if errs == nil {
		v.Errs = fieldErrors{}
	}
	v.Summary = v.Errs.summarize(loc, ownFields...)
	return v
}

// account is GET /account: the own-password form of an editor, or where the owner's password is set.
func (s *Server) account(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	s.render(w, r, http.StatusOK, "account", s.newAccountView(r, sess, nil))
}

// changeOwnPassword is POST /account/password (S4): the current password and the new one twice.
// Every other session of the user ends; the current browser gets a fresh cookie and stays in.
func (s *Server) changeOwnPassword(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	loc := localeOf(r)
	if sess.Owner {
		s.ownerProtected(w, r, sess)
		return
	}
	if !s.readPost(w, r, sess) {
		return
	}
	fail := func(status int, errs fieldErrors) {
		s.render(w, r, status, "account", s.newAccountView(r, sess, errs))
	}
	password := r.PostFormValue("password")
	if password != r.PostFormValue("confirm_password") {
		fail(http.StatusUnprocessableEntity, fieldErrors{"confirm_password": {{Text: loc.T("val.user.password.mismatch")}}})
		return
	}

	// Somebody holding a stolen session must not be able to guess the current password.
	key := ownPasswordKeyPrefix + sess.User
	allowed, wait := s.limiter.Attempt(key)
	if !allowed {
		s.log.Printf("password change blocked user=%q ip=%s retry_in=%s", sess.User, clientIP(r, s.trusted), wait.Round(time.Second))
		w.Header().Set("Retry-After", fmt.Sprint(int(wait.Seconds()+0.999)))
		fail(http.StatusTooManyRequests, fieldErrors{"current": {{Text: loc.T("account.err.throttled", waitText(loc, wait))}}})
		return
	}
	if _, _, ok := s.users.Authenticate(sess.User, r.PostFormValue("current")); !ok {
		s.log.Printf("password change failed user=%q ip=%s", sess.User, clientIP(r, s.trusted))
		fail(http.StatusUnprocessableEntity, fieldErrors{"current": {{Text: loc.T("val.user.current.wrong")}}})
		return
	}
	s.limiter.Success(key)

	if err := s.users.SetPassword(sess.User, password); err != nil {
		switch {
		case errors.Is(err, users.ErrPasswordShort), errors.Is(err, users.ErrPasswordLong):
			fail(http.StatusUnprocessableEntity, fieldErrors{"password": {{Text: passwordIssue(loc, err)}}})
		case errors.Is(err, users.ErrNotFound): // deleted while the form was open
			s.notFound(w, r, sess)
		default:
			s.internalError(w, r, sess, "change own password", err)
		}
		return
	}
	s.log.Printf("password changed user=%q", sess.User)

	s.sessions.Revoke(sess)
	setSecurityHeaders(w.Header())
	if stamp, ok := s.users.Stamp(sess.User); ok {
		_, cookie, err := s.sessions.Issue(auth.Identity{User: sess.User, Stamp: stamp})
		if err == nil {
			http.SetCookie(w, cookie)
			http.Redirect(w, r, accountNotice, http.StatusSeeOther)
			return
		}
		s.log.Printf("password changed but no new session could be issued: %v", err)
	}
	http.SetCookie(w, s.sessions.ExpiredCookie())
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

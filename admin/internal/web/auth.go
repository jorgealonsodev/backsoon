package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/jorgealonsodev/backsoon/admin/internal/auth"
)

// Login CSRF has no session to hang a token on, so the form carries a signed double-submit
// token: a random nonce is sent both as a cookie (nonce.signature, signed with the session
// secret, HttpOnly, Secure, SameSite=Strict, scoped to /login) and as a hidden field, and POST
// /login requires the two to match. A cross-site page can neither read the nonce nor set a
// validly signed cookie. As a second layer, a browser that says the request is not same-origin
// (Sec-Fetch-Site) is refused outright.
const (
	loginCookieName = "backsoon_login"
	loginTokenField = "login_token"
	loginTokenTTL   = 15 * time.Minute
	loginBodyMax    = 16 << 10
)

func (s *Server) loginSignature(nonce string) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte("login-csrf"))
	mac.Write([]byte{0})
	mac.Write([]byte(nonce))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// newLoginToken returns the cookie that proves a login form was issued by this panel and the
// matching field value.
func (s *Server) newLoginToken() (*http.Cookie, string) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	nonce := hex.EncodeToString(raw[:])
	return &http.Cookie{
		Name:     loginCookieName,
		Value:    nonce + "." + s.loginSignature(nonce),
		Path:     "/login",
		MaxAge:   int(loginTokenTTL.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	}, nonce
}

func (s *Server) validLoginToken(r *http.Request) bool {
	field := r.PostFormValue(loginTokenField)
	c, err := r.Cookie(loginCookieName)
	if err != nil || field == "" {
		return false
	}
	nonce, sig, ok := strings.Cut(c.Value, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(s.loginSignature(nonce))) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(nonce), []byte(field)) == 1
}

func (s *Server) renderLogin(w http.ResponseWriter, status int, user, message string) {
	cookie, token := s.newLoginToken()
	http.SetCookie(w, cookie)
	s.render(w, status, "login", &loginView{
		base: base{Title: "Sign in · backsoon"}, User: user, Token: token, Error: message,
	})
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if _, err := s.sessions.FromRequest(r); err == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.renderLogin(w, http.StatusOK, "", "")
}

func (s *Server) loginSubmit(w http.ResponseWriter, r *http.Request) {
	const expired = "The sign-in form expired. Try again."
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
	default:
		s.renderLogin(w, http.StatusForbidden, "", expired)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, loginBodyMax)
	if err := r.ParseForm(); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			s.renderLogin(w, http.StatusRequestEntityTooLarge, "", "That request was too large. Enter only your user and password.")
			return
		}
		s.renderLogin(w, http.StatusBadRequest, "", "That request could not be read. Try again.")
		return
	}
	if !s.validLoginToken(r) {
		s.renderLogin(w, http.StatusForbidden, "", expired)
		return
	}

	ip := clientIP(r)
	user := r.PostFormValue("user")
	allowed, wait := s.limiter.Attempt(ip)
	if !allowed {
		s.log.Printf("login blocked ip=%s retry_in=%s", ip, wait.Round(time.Second))
		w.Header().Set("Retry-After", fmt.Sprint(int(wait.Seconds()+0.999)))
		s.renderLogin(w, http.StatusTooManyRequests, user,
			"Too many sign-in attempts. Try again in "+waitText(wait)+".")
		return
	}
	if !s.creds.Check(user, r.PostFormValue("password")) {
		s.log.Printf("login failed ip=%s", ip)
		s.renderLogin(w, http.StatusUnauthorized, user, "Wrong user or password.")
		return
	}

	s.limiter.Success(ip)
	_, cookie, err := s.sessions.Issue()
	if err != nil {
		s.log.Printf("login failed to issue a session: %v", err)
		s.renderLogin(w, http.StatusInternalServerError, user, "Something went wrong. Try again.")
		return
	}
	http.SetCookie(w, cookie)
	http.SetCookie(w, &http.Cookie{Name: loginCookieName, Path: "/login", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	s.log.Printf("login ok user=%q ip=%s", s.user, ip)
	setSecurityHeaders(w.Header())
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	r.Body = http.MaxBytesReader(w, r.Body, loginBodyMax)
	if err := r.ParseForm(); err != nil || !s.validCSRF(sess, r) {
		s.csrfFailed(w, sess)
		return
	}
	s.sessions.Revoke(sess)
	http.SetCookie(w, s.sessions.ExpiredCookie())
	s.log.Printf("logout ip=%s", clientIP(r))
	setSecurityHeaders(w.Header())
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// waitText rounds the wait up to whole minutes ("1 minute", "15 minutes").
func waitText(d time.Duration) string {
	minutes := int((d + time.Minute - 1) / time.Minute)
	if minutes < 1 {
		minutes = 1
	}
	if minutes == 1 {
		return "1 minute"
	}
	return fmt.Sprintf("%d minutes", minutes)
}

// clientIP is the key of the login limiter: X-Real-IP, which Nginx Proxy Manager sets and
// overwrites (the panel publishes no port, so nothing else can reach it), else the host of the
// connection's remote address.
func clientIP(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get("X-Real-IP")); v != "" {
		if ip := net.ParseIP(v); ip != nil {
			return ip.String()
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	return host
}

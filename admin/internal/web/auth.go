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
	"net/netip"
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

func (s *Server) renderLogin(w http.ResponseWriter, r *http.Request, status int, user, message string) {
	cookie, token := s.newLoginToken()
	http.SetCookie(w, cookie)
	s.render(w, r, status, "login", &loginView{
		base: base{Title: localeOf(r).T("title.login") + " · backsoon"}, User: user, Token: token, Error: message,
	})
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if _, err := s.sessions.FromRequest(r); err == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.renderLogin(w, r, http.StatusOK, "", "")
}

func (s *Server) loginSubmit(w http.ResponseWriter, r *http.Request) {
	loc := localeOf(r)
	expired := loc.T("login.err.expired")
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
	default:
		s.renderLogin(w, r, http.StatusForbidden, "", expired)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, loginBodyMax)
	if err := r.ParseForm(); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			s.renderLogin(w, r, http.StatusRequestEntityTooLarge, "", loc.T("login.err.toolarge"))
			return
		}
		s.renderLogin(w, r, http.StatusBadRequest, "", loc.T("request.unreadable"))
		return
	}
	if !s.validLoginToken(r) {
		s.renderLogin(w, r, http.StatusForbidden, "", expired)
		return
	}

	ip := clientIP(r, s.trusted)
	user := r.PostFormValue("user")
	allowed, wait := s.limiter.Attempt(ip)
	if !allowed {
		s.log.Printf("login blocked ip=%s retry_in=%s", ip, wait.Round(time.Second))
		w.Header().Set("Retry-After", fmt.Sprint(int(wait.Seconds()+0.999)))
		s.renderLogin(w, r, http.StatusTooManyRequests, user, loc.T("login.err.throttled", waitText(loc, wait)))
		return
	}
	who, ok := s.accounts.Authenticate(user, r.PostFormValue("password"))
	if !ok {
		s.log.Printf("login failed ip=%s", ip)
		s.renderLogin(w, r, http.StatusUnauthorized, user, loc.T("login.err.wrong"))
		return
	}

	s.limiter.Success(ip)
	_, cookie, err := s.sessions.Issue(who)
	if err != nil {
		s.log.Printf("login failed to issue a session: %v", err)
		s.renderLogin(w, r, http.StatusInternalServerError, user, loc.T("login.err.session"))
		return
	}
	http.SetCookie(w, cookie)
	http.SetCookie(w, &http.Cookie{Name: loginCookieName, Path: "/login", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	s.log.Printf("login ok user=%q ip=%s", who.User, ip)
	setSecurityHeaders(w.Header())
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	r.Body = http.MaxBytesReader(w, r.Body, loginBodyMax)
	if err := r.ParseForm(); err != nil || !s.validCSRF(sess, r) {
		s.csrfFailed(w, r, sess)
		return
	}
	s.sessions.Revoke(sess)
	http.SetCookie(w, s.sessions.ExpiredCookie())
	s.log.Printf("logout user=%q ip=%s", sess.User, clientIP(r, s.trusted))
	setSecurityHeaders(w.Header())
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// waitText rounds the wait up to whole minutes ("1 minute", "15 minutes").
func waitText(loc locale, d time.Duration) string {
	minutes := int((d + time.Minute - 1) / time.Minute)
	if minutes < 1 {
		minutes = 1
	}
	if minutes == 1 {
		return loc.T("wait.one")
	}
	return loc.T("wait.many", minutes)
}

// clientIP is the key of the login limiter: the address of the connection's peer, unless that
// peer is a trusted proxy, in which case X-Real-IP, which Nginx Proxy Manager sets and
// overwrites. A nil trusted list trusts every peer (BACKSOON_TRUSTED_PROXIES unset): any
// container on its network, or client of its published host port, could then forge X-Real-IP.
func clientIP(r *http.Request, trusted []netip.Prefix) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, peerErr := netip.ParseAddr(host)
	peer = peer.Unmap()
	if trusted == nil || (peerErr == nil && containsAddr(trusted, peer)) {
		if v := strings.TrimSpace(r.Header.Get("X-Real-IP")); v != "" {
			if ip := net.ParseIP(v); ip != nil {
				return ip.String()
			}
		}
	}
	if peerErr == nil {
		return peer.String()
	}
	return host
}

func containsAddr(prefixes []netip.Prefix, addr netip.Addr) bool {
	for _, p := range prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	// SessionCookieName is the name of the session cookie.
	SessionCookieName = "backsoon_session"
	// SessionLifetime is how long a session stays valid (RNF-8).
	SessionLifetime = 12 * time.Hour
)

// ErrInvalidSession is returned for a missing, malformed, tampered or expired session.
var ErrInvalidSession = errors.New("invalid session")

// Session is the verified content of a session cookie.
type Session struct {
	// ID is a random identifier (32 hex characters).
	ID string
	// Expires is the absolute expiry time.
	Expires time.Time
}

// Manager issues and verifies sessions signed with HMAC-SHA256. Sessions are stateless: the
// cookie value is "<id>.<expiry unix seconds>.<base64url signature>". The same secret derives
// CSRF tokens under a separate domain prefix.
type Manager struct {
	secret []byte
	now    func() time.Time
}

// NewManager returns a Manager using secret as the signing key. A nil clock means time.Now.
func NewManager(secret []byte, now func() time.Time) *Manager {
	if now == nil {
		now = time.Now
	}
	return &Manager{secret: append([]byte(nil), secret...), now: now}
}

// Issue creates a new session and the cookie that carries it.
func (m *Manager) Issue() (Session, *http.Cookie, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return Session{}, nil, err
	}
	s := Session{ID: hex.EncodeToString(raw[:]), Expires: m.now().Add(SessionLifetime).Truncate(time.Second)}
	return s, m.Cookie(s), nil
}

// Cookie builds the session cookie: HttpOnly, Secure, SameSite=Strict, Path=/.
func (m *Manager) Cookie(s Session) *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    s.ID + "." + strconv.FormatInt(s.Expires.Unix(), 10) + "." + m.sign("session", s.ID+"."+strconv.FormatInt(s.Expires.Unix(), 10)),
		Path:     "/",
		Expires:  s.Expires,
		MaxAge:   int(s.Expires.Sub(m.now()).Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	}
}

// ExpiredCookie returns a cookie that makes the browser drop the session (logout).
func (m *Manager) ExpiredCookie() *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	}
}

// Verify parses a cookie value and checks its signature and expiry.
func (m *Manager) Verify(value string) (Session, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 3 || len(parts[0]) != 32 {
		return Session{}, ErrInvalidSession
	}
	want := m.sign("session", parts[0]+"."+parts[1])
	if !hmac.Equal([]byte(want), []byte(parts[2])) {
		return Session{}, ErrInvalidSession
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return Session{}, ErrInvalidSession
	}
	s := Session{ID: parts[0], Expires: time.Unix(exp, 0)}
	if !m.now().Before(s.Expires) {
		return Session{}, ErrInvalidSession
	}
	return s, nil
}

// FromRequest verifies the session cookie of r.
func (m *Manager) FromRequest(r *http.Request) (Session, error) {
	c, err := r.Cookie(SessionCookieName)
	if err != nil {
		return Session{}, ErrInvalidSession
	}
	return m.Verify(c.Value)
}

// CSRFToken returns the CSRF token bound to s: an HMAC of the session id. It is stable for the
// whole session and different for every session.
func (m *Manager) CSRFToken(s Session) string { return m.sign("csrf", s.ID) }

// VerifyCSRF reports, in constant time, whether token is the CSRF token of s.
func (m *Manager) VerifyCSRF(s Session, token string) bool {
	return hmac.Equal([]byte(m.CSRFToken(s)), []byte(token))
}

func (m *Manager) sign(domain, payload string) string {
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(domain))
	mac.Write([]byte{0})
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

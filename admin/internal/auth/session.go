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
	"sync"
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

// Identity is who a session belongs to: a signed-in user and the stamp that user had at sign-in.
type Identity struct {
	// User is the user name (the owner's configured name, or a stored user's lowercase name).
	User string
	// Stamp is the user's session stamp when the session was issued.
	Stamp string
	// Owner reports that User is the owner.
	Owner bool
}

// Directory says who exists and what their current stamp is. A session is valid only while its
// user exists and its stamp is still the current one, so deleting a user or setting a password
// ends the sessions, across restarts, without any list of revoked sessions.
type Directory interface {
	// Resolve returns the current stamp of user and whether user is the owner; ok is false when the
	// user does not exist.
	Resolve(user string) (stamp string, owner, ok bool)
}

// Session is the verified content of a session cookie.
type Session struct {
	// ID is a random identifier (32 hex characters).
	ID string
	// Expires is the absolute expiry time.
	Expires time.Time
	// User is the signed-in user's name.
	User string
	// Stamp is the user's session stamp the session was issued with.
	Stamp string
	// Owner reports that User is the owner. It is read from the Directory by Verify, not from the
	// cookie.
	Owner bool
}

// Manager issues and verifies sessions signed with HMAC-SHA256. Sessions are stateless: the
// cookie value is "<id>.<expiry unix seconds>.<base64url user>.<stamp>.<base64url signature>", so
// they survive a panel restart. The same secret derives CSRF tokens under a separate domain prefix.
// Cookies of the earlier three-part format are invalid.
//
// Logout revokes a session by remembering its id in memory until the session would have expired.
// The list is not persisted: a restart forgets it, and only a new BACKSOON_SESSION_SECRET
// invalidates every session issued before. Verify also asks the Directory, which is persistent.
type Manager struct {
	secret []byte
	now    func() time.Time
	dir    Directory

	mu      sync.Mutex
	revoked map[string]time.Time // session id -> expiry
}

// NewManager returns a Manager using secret as the signing key and dir to check users. A nil
// clock means time.Now. With a nil Directory no session verifies.
func NewManager(secret []byte, now func() time.Time, dir Directory) *Manager {
	if now == nil {
		now = time.Now
	}
	return &Manager{secret: append([]byte(nil), secret...), now: now, dir: dir, revoked: make(map[string]time.Time)}
}

// Revoke makes Verify reject s from now on. Each call drops the revocations of sessions that have
// expired since, so the list holds at most the sessions closed within SessionLifetime before the
// latest logout.
func (m *Manager) Revoke(s Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	for id, exp := range m.revoked {
		if !now.Before(exp) {
			delete(m.revoked, id)
		}
	}
	if now.Before(s.Expires) {
		m.revoked[s.ID] = s.Expires
	}
}

func (m *Manager) isRevoked(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.revoked[id]
	return ok
}

func (m *Manager) revokedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.revoked)
}

// Issue creates a new session for id and the cookie that carries it.
func (m *Manager) Issue(id Identity) (Session, *http.Cookie, error) {
	if id.User == "" || id.Stamp == "" {
		return Session{}, nil, errors.New("a session needs a user and a stamp")
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return Session{}, nil, err
	}
	s := Session{
		ID:      hex.EncodeToString(raw[:]),
		Expires: m.now().Add(SessionLifetime).Truncate(time.Second),
		User:    id.User, Stamp: id.Stamp, Owner: id.Owner,
	}
	return s, m.Cookie(s), nil
}

// payload is the signed part of the cookie. The user name is base64url so that it can hold any
// character; the stamp is hexadecimal.
func payload(s Session) string {
	return s.ID + "." + strconv.FormatInt(s.Expires.Unix(), 10) + "." +
		base64.RawURLEncoding.EncodeToString([]byte(s.User)) + "." + s.Stamp
}

// Cookie builds the session cookie: HttpOnly, Secure, SameSite=Strict, Path=/.
func (m *Manager) Cookie(s Session) *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    payload(s) + "." + m.sign("session", payload(s)),
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

// Verify parses a cookie value and checks its signature, expiry, revocation and that its user still
// exists with the same stamp.
func (m *Manager) Verify(value string) (Session, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 5 || len(parts[0]) != 32 || parts[3] == "" || m.dir == nil {
		return Session{}, ErrInvalidSession
	}
	want := m.sign("session", strings.Join(parts[:4], "."))
	if !hmac.Equal([]byte(want), []byte(parts[4])) {
		return Session{}, ErrInvalidSession
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return Session{}, ErrInvalidSession
	}
	user, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(user) == 0 {
		return Session{}, ErrInvalidSession
	}
	s := Session{ID: parts[0], Expires: time.Unix(exp, 0), User: string(user), Stamp: parts[3]}
	if !m.now().Before(s.Expires) || m.isRevoked(s.ID) {
		return Session{}, ErrInvalidSession
	}
	stamp, owner, ok := m.dir.Resolve(s.User)
	if !ok || !hmac.Equal([]byte(stamp), []byte(s.Stamp)) {
		return Session{}, ErrInvalidSession
	}
	s.Owner = owner
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

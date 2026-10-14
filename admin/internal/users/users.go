// Package users stores the panel's added users: the people the owner lets sign in to manage pages.
// The owner itself is configured through the stack variables and is not stored here.
//
// Users live in <meta>/users.json, written atomically with mode 0600. Only bcrypt hashes are kept.
// Every user carries a session stamp that changes when the password is set and is gone when the
// user is deleted; a session is valid only while its stamp is the current one, which is how
// sessions are ended across restarts.
package users

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"

	"github.com/jorgealonsodev/backsoon/admin/internal/publish/atomic"
)

// Limits of S2.
const (
	MaxNameLength    = 64
	MinPasswordRunes = 10
	MaxPasswordBytes = 72 // the bcrypt limit
	// Cost is the bcrypt cost of the stored hashes.
	Cost = 12

	fileName    = "users.json"
	fileVersion = 1
	epochBytes  = 16
)

// Errors returned by the store; callers match them with errors.Is.
var (
	ErrInvalidName   = errors.New("user name must be 1 to 64 characters from a-z, 0-9, dot, underscore and hyphen")
	ErrOwnerName     = errors.New("user name is the owner's")
	ErrExists        = errors.New("user already exists")
	ErrNotFound      = errors.New("user not found")
	ErrPasswordShort = errors.New("password is shorter than 10 characters")
	ErrPasswordLong  = errors.New("password is longer than 72 bytes")
)

// compareHash is bcrypt.CompareHashAndPassword; tests count its calls.
var compareHash = bcrypt.CompareHashAndPassword

// User is what the panel shows about a user. It never includes the hash or the stamp.
type User struct {
	Name            string
	Created         time.Time
	PasswordChanged time.Time
}

// Options configure Open.
type Options struct {
	// MetaDir holds users.json. It must exist when the first user is written.
	MetaDir string
	// Owner is the owner's user name; no stored user may have it (compared case-insensitively).
	Owner string
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// Cost is the bcrypt cost; zero means Cost (12). Tests lower it.
	Cost int
}

// Store is the set of added users. It is safe for concurrent use.
type Store struct {
	path  string
	owner string
	now   func() time.Time
	cost  int
	// dummy is a hash of the right cost for the comparison made when the user does not exist. It is
	// computed in the background at Open so that starting the panel does not wait for it; ready is
	// closed once it is set.
	dummy []byte
	ready chan struct{}

	mu    sync.RWMutex
	users map[string]*record
}

type record struct {
	Name            string    `json:"name"`
	Hash            string    `json:"hash"`
	Epoch           string    `json:"epoch"`
	Created         time.Time `json:"created"`
	PasswordChanged time.Time `json:"password_changed"`
}

type file struct {
	Version int       `json:"version"`
	Users   []*record `json:"users"`
}

// Open loads <MetaDir>/users.json. A missing file means no users; a corrupt or invalid one is an
// error, so the panel refuses to start rather than run with users it cannot trust.
func Open(o Options) (*Store, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Cost == 0 {
		o.Cost = Cost
	}
	s := &Store{
		path:  filepath.Join(o.MetaDir, fileName),
		owner: asciiLower(o.Owner),
		now:   o.Now,
		cost:  o.Cost,
		users: make(map[string]*record),
		ready: make(chan struct{}),
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return nil, fmt.Errorf("random: %w", err)
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	go func() {
		defer close(s.ready)
		// The password is random and discarded: no input matches this hash. An invalid cost makes
		// the library fall back to its default.
		s.dummy, _ = bcrypt.GenerateFromPassword([]byte(hex.EncodeToString(random)), s.cost)
	}()
	return s, nil
}

func (s *Store) load() error {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", fileName, err)
	}
	var f file
	if err := json.Unmarshal(raw, &f); err != nil {
		return fmt.Errorf("%s is corrupt (fix or remove it; removing it deletes every added user): %w", fileName, err)
	}
	if f.Version != fileVersion {
		return fmt.Errorf("%s is invalid: unsupported version %d", fileName, f.Version)
	}
	if f.Users == nil {
		return fmt.Errorf("%s is invalid: no user list", fileName)
	}
	for i, r := range f.Users {
		if err := r.validate(); err != nil {
			return fmt.Errorf("%s is invalid: entry %d: %w", fileName, i+1, err)
		}
		if _, dup := s.users[r.Name]; dup {
			return fmt.Errorf("%s is invalid: entry %d repeats a user name", fileName, i+1)
		}
		s.users[r.Name] = r
	}
	return nil
}

func (r *record) validate() error {
	switch {
	case r == nil:
		return errors.New("empty entry")
	case !validName(r.Name) || r.Name != asciiLower(r.Name):
		return errors.New("user name is not valid")
	case r.Epoch == "" || len(r.Epoch) > 128 || strings.Trim(r.Epoch, "0123456789abcdef") != "":
		return errors.New("session stamp is not valid")
	case r.Created.IsZero() || r.PasswordChanged.IsZero():
		return errors.New("a time is missing")
	}
	if _, err := bcrypt.Cost([]byte(r.Hash)); err != nil {
		return errors.New("password hash is not a bcrypt hash")
	}
	return nil
}

// Create adds a user. The name is stored lowercase. The password is hashed with bcrypt; the
// plaintext is neither kept nor logged.
func (s *Store) Create(name, password string) (User, error) {
	if !validName(name) {
		return User{}, ErrInvalidName
	}
	name = asciiLower(name)
	if name == s.owner {
		return User{}, ErrOwnerName
	}
	if err := checkPassword(password); err != nil {
		return User{}, err
	}
	s.mu.RLock()
	_, exists := s.users[name]
	s.mu.RUnlock()
	if exists {
		return User{}, ErrExists
	}
	hash, epoch, err := s.credentials(password)
	if err != nil {
		return User{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.users[name]; exists {
		return User{}, ErrExists
	}
	now := s.now().UTC()
	r := &record{Name: name, Hash: hash, Epoch: epoch, Created: now, PasswordChanged: now}
	if err := s.persist(func(m map[string]*record) { m[name] = r }); err != nil {
		return User{}, err
	}
	return r.user(), nil
}

// SetPassword replaces the password of name and changes its session stamp, which ends every
// session of the user.
func (s *Store) SetPassword(name, password string) error {
	name = asciiLower(name)
	if err := checkPassword(password); err != nil {
		return err
	}
	s.mu.RLock()
	_, exists := s.users[name]
	s.mu.RUnlock()
	if !exists {
		return ErrNotFound
	}
	hash, epoch, err := s.credentials(password)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	old, exists := s.users[name]
	if !exists {
		return ErrNotFound
	}
	updated := *old
	updated.Hash, updated.Epoch, updated.PasswordChanged = hash, epoch, s.now().UTC()
	return s.persist(func(m map[string]*record) { m[name] = &updated })
}

// Delete removes name. Its sessions end, and a user created later with the same name starts with a
// new stamp, so they do not come back.
func (s *Store) Delete(name string) error {
	name = asciiLower(name)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.users[name]; !exists {
		return ErrNotFound
	}
	return s.persist(func(m map[string]*record) { delete(m, name) })
}

// List returns the users ordered by name.
func (s *Store) List() []User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]User, 0, len(s.users))
	for _, r := range s.users {
		out = append(out, r.user())
	}
	slices.SortFunc(out, func(a, b User) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// Lookup returns the user called name (case-insensitive).
func (s *Store) Lookup(name string) (User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.users[asciiLower(name)]
	if !ok {
		return User{}, false
	}
	return r.user(), true
}

// Stamp returns the current session stamp of name: it differs after every password set and for a
// user recreated after a delete. A session is valid only while it carries the current stamp.
func (s *Store) Stamp(name string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.users[asciiLower(name)]
	if !ok {
		return "", false
	}
	return r.Epoch, true
}

// Authenticate checks a sign-in. It returns the canonical (lowercase) name and the current stamp.
// It always makes exactly one bcrypt comparison, against a dummy hash for an unknown user, so the
// response time does not reveal which names exist.
func (s *Store) Authenticate(name, password string) (canonical, stamp string, ok bool) {
	canonical = asciiLower(name)
	s.mu.RLock()
	r, found := s.users[canonical]
	var hash []byte
	if found {
		hash, stamp = []byte(r.Hash), r.Epoch
	}
	s.mu.RUnlock()
	if !found {
		<-s.ready
		hash = s.dummy
	}

	matched := compareHash(hash, []byte(password)) == nil
	if !found || !matched {
		return "", "", false
	}
	return canonical, stamp, true
}

func (r *record) user() User {
	return User{Name: r.Name, Created: r.Created, PasswordChanged: r.PasswordChanged}
}

// credentials hashes password and draws a new session stamp.
func (s *Store) credentials(password string) (hash, epoch string, err error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), s.cost)
	if err != nil {
		return "", "", fmt.Errorf("hash password: %w", err)
	}
	raw := make([]byte, epochBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("random: %w", err)
	}
	return string(h), hex.EncodeToString(raw), nil
}

// persist writes the users as they would be after change and, only if the write succeeded, makes
// the change. The caller holds the write lock.
func (s *Store) persist(change func(map[string]*record)) error {
	next := make(map[string]*record, len(s.users)+1)
	for k, v := range s.users {
		next[k] = v
	}
	change(next)

	f := file{Version: fileVersion, Users: make([]*record, 0, len(next))}
	for _, r := range next {
		f.Users = append(f.Users, r)
	}
	slices.SortFunc(f.Users, func(a, b *record) int { return strings.Compare(a.Name, b.Name) })
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(f); err != nil {
		return fmt.Errorf("encode %s: %w", fileName, err)
	}
	if err := atomic.WriteFile(s.path, buf.Bytes(), 0o600); err != nil {
		return fmt.Errorf("save %s: %w", fileName, err)
	}
	s.users = next
	return nil
}

// validName reports whether name is 1 to 64 characters from a-z, A-Z, 0-9, ".", "_" and "-".
// Upper case is accepted and stored lowercase; the check is ASCII-only so that no other
// character folds onto these.
func validName(name string) bool {
	if name == "" || len(name) > MaxNameLength {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}

func checkPassword(password string) error {
	switch {
	case len(password) > MaxPasswordBytes:
		return ErrPasswordLong
	case utf8.RuneCountInString(password) < MinPasswordRunes:
		return ErrPasswordShort
	}
	return nil
}

// asciiLower lowers A-Z only, so that characters such as the Kelvin sign do not fold to ASCII.
func asciiLower(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}, s)
}

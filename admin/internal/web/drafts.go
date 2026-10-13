package web

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// Pending drafts hold an upload while the administrator confirms a warning or a destination move,
// so the file never has to be selected again. They live in memory only and are bounded in count
// (and therefore in bytes: each page is at most 512 KB) and in time.
const (
	draftTTL            = 15 * time.Minute
	maxDrafts           = 8
	maxDraftsPerSession = 3
)

// formValues are the fields of the create and edit forms.
type formValues struct {
	Name         string
	Destinations string
	Source       string // "template" or "upload"
	AppName      string
	Message      string
	Lang         string
	Icon         string
}

type draft struct {
	id      string
	session string
	pageID  string // "" when the draft belongs to a new page
	form    formValues

	html     []byte // nil: keep the page's current HTML (edit only)
	fileName string

	warningsOK bool
	moveOK     bool

	expires time.Time
	seq     uint64
}

type draftStore struct {
	now func() time.Time

	mu  sync.Mutex
	m   map[string]*draft
	seq uint64
}

func newDraftStore(now func() time.Time) *draftStore {
	return &draftStore{now: now, m: make(map[string]*draft)}
}

// save stores d under a fresh random id, evicting the oldest drafts of its session and, when the
// store is full, the oldest overall.
func (s *draftStore) save(d *draft) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune()
	for s.count(d.session) >= maxDraftsPerSession {
		s.evictOldest(d.session)
	}
	for len(s.m) >= maxDrafts {
		s.evictOldest("")
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	s.seq++
	d.id = hex.EncodeToString(raw[:])
	d.expires = s.now().Add(draftTTL)
	d.seq = s.seq
	s.m[d.id] = d
	return d.id
}

// load returns the draft with this id if it belongs to the session and has not expired.
func (s *draftStore) load(id, session string) (*draft, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune()
	d, ok := s.m[id]
	if !ok || d.session != session {
		return nil, false
	}
	return d, true
}

func (s *draftStore) drop(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, id)
}

func (s *draftStore) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune()
	return len(s.m)
}

func (s *draftStore) prune() {
	now := s.now()
	for id, d := range s.m {
		if !now.Before(d.expires) {
			delete(s.m, id)
		}
	}
}

func (s *draftStore) count(session string) int {
	n := 0
	for _, d := range s.m {
		if d.session == session {
			n++
		}
	}
	return n
}

// evictOldest removes the oldest draft of session, or of anyone when session is empty.
func (s *draftStore) evictOldest(session string) {
	var victim *draft
	for _, d := range s.m {
		if session != "" && d.session != session {
			continue
		}
		if victim == nil || d.seq < victim.seq {
			victim = d
		}
	}
	if victim != nil {
		delete(s.m, victim.id)
	}
}

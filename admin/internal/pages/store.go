package pages

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jorgealonsodev/backsoon/admin/internal/publish/atomic"
)

// DefaultID is the id of the page that always exists, owns no targets and cannot be deleted.
const DefaultID = "default"

// DefaultName is the name of the default page.
const DefaultName = "Default page"

// Errors returned by the Store. Use errors.Is / errors.As.
var (
	ErrNotFound         = errors.New("page not found")
	ErrDefaultImmutable = errors.New("the default page cannot be deleted or assigned targets")
	ErrNoTargets        = errors.New("a page needs at least one target")
	ErrInvalidName      = errors.New("invalid page name")
)

// Site is a page: a name, the targets it is published for and its last modification time.
type Site struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Targets   []Target  `json:"targets"`
	UpdatedAt time.Time `json:"updated_at"`
}

// PageContent is a Site together with its original HTML.
type PageContent struct {
	Site Site
	HTML []byte
}

// Snapshot is a consistent view of every page, default first. It is what gets published.
type Snapshot struct {
	Pages []PageContent
}

// Conflict says which page currently owns a requested target.
type Conflict struct {
	Target    Target
	OwnerID   string
	OwnerName string
}

// ConflictError is returned when targets are owned by other pages and the caller did not confirm
// the move. Repeating the call with confirm=true moves them.
type ConflictError struct {
	Conflicts []Conflict
}

func (e *ConflictError) Error() string {
	parts := make([]string, len(e.Conflicts))
	for i, c := range e.Conflicts {
		parts[i] = fmt.Sprintf("%s (page %q)", c.Target.Value, c.OwnerName)
	}
	return "targets already assigned to another page: " + strings.Join(parts, ", ")
}

// PublishError reports that the change was saved but publishing it to the pages directory failed.
// The state is persisted; the next change or restart publishes everything again.
type PublishError struct{ Err error }

func (e *PublishError) Error() string { return "saved, but publishing failed: " + e.Err.Error() }
func (e *PublishError) Unwrap() error { return e.Err }

// Options configure Open.
type Options struct {
	// MetaDir holds sites.json and pages/<id>.html. Created if missing.
	MetaDir string
	// PagesDir is the published directory; only read at bootstrap, to keep a default.html that
	// backsoon-init already seeded.
	PagesDir string
	// TemplatePath is the maintenance template.
	TemplatePath string
	// Refresh is the refresh interval used when rendering the template (default 10).
	Refresh int
	// Publish, if set, is called under the store lock with a snapshot at startup and after every
	// change. Wire publish.Publisher(pagesDir) here.
	Publish func(Snapshot) error
	// Now overrides the clock (tests).
	Now func() time.Time
}

// Store keeps the pages in memory and persists them atomically. It is safe for concurrent use
// within one process.
type Store struct {
	opts  Options
	mu    sync.Mutex
	sites []Site
}

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// Open loads <meta>/sites.json, creating it (and the default page) on first run, and publishes
// everything once, which repairs any inconsistency left in the pages directory.
//
// The default page's HTML is <pages>/default.html when it exists, so what backsoon-init seeded is
// preserved; otherwise it is rendered from the template with the seed's values.
func Open(opts Options) (*Store, error) {
	if opts.Refresh == 0 {
		opts.Refresh = DefaultRefresh
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	s := &Store{opts: opts}
	if err := os.MkdirAll(s.pagesMetaDir(), 0o755); err != nil {
		return nil, fmt.Errorf("create meta directory: %w", err)
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.publish(); err != nil {
		return nil, err
	}
	return s, nil
}

// List returns all pages: the default page first, then the rest by name.
func (s *Store) List() []Site {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := cloneSites(s.sites)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if (a.ID == DefaultID) != (b.ID == DefaultID) {
			return a.ID == DefaultID
		}
		if an, bn := strings.ToLower(a.Name), strings.ToLower(b.Name); an != bn {
			return an < bn
		}
		return a.ID < b.ID
	})
	return out
}

// Get returns one page.
func (s *Store) Get(id string) (Site, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := indexOf(s.sites, id)
	if i < 0 {
		return Site{}, ErrNotFound
	}
	return cloneSite(s.sites[i]), nil
}

// HTML returns the original HTML of a page.
func (s *Store) HTML(id string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if indexOf(s.sites, id) < 0 {
		return nil, ErrNotFound
	}
	return s.readHTML(id)
}

// Snapshot returns every page with its HTML.
func (s *Store) Snapshot() (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot()
}

// Render renders the maintenance template with p. A zero Refresh uses the store's interval.
func (s *Store) Render(p TemplateParams) ([]byte, error) {
	if p.Refresh == 0 {
		p.Refresh = s.opts.Refresh
	}
	tpl, err := os.ReadFile(s.opts.TemplatePath)
	if err != nil {
		return nil, fmt.Errorf("read template: %w", err)
	}
	return RenderTemplate(tpl, p)
}

// Create adds a page. targets are raw user input (see ParseTarget). If a target belongs to another
// page, a *ConflictError is returned unless confirm is true, in which case it moves here.
// html must pass ValidateHTML's errors; its warnings are the caller's to confirm.
// With a *PublishError the page was saved and is returned.
func (s *Store) Create(name string, targets []string, html []byte, confirm bool) (Site, error) {
	name, err := cleanName(name)
	if err != nil {
		return Site{}, err
	}
	if _, err := ValidateHTML(html); err != nil {
		return Site{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	sites := cloneSites(s.sites)
	parsed, err := assign(sites, "", targets, confirm, now)
	if err != nil {
		return Site{}, err
	}
	site := Site{ID: s.newID(), Name: name, Targets: parsed, UpdatedAt: now}
	sites = append(sites, site)

	if err := s.writeHTML(site.ID, html); err != nil {
		return Site{}, err
	}
	if err := s.persist(sites); err != nil {
		os.Remove(s.htmlPath(site.ID))
		return Site{}, err
	}
	s.sites = sites
	return cloneSite(site), s.publish()
}

// Update changes a page. Nil/empty fields are left as they are. See Create for confirm and the
// error semantics. The default page can change name and HTML but cannot get targets.
type Update struct {
	Name    *string
	Targets *[]string
	HTML    []byte
}

// Update applies u to the page id.
func (s *Store) Update(id string, u Update, confirm bool) (Site, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := indexOf(s.sites, id)
	if i < 0 {
		return Site{}, ErrNotFound
	}
	now := s.now()
	sites := cloneSites(s.sites)

	if u.Name != nil {
		name, err := cleanName(*u.Name)
		if err != nil {
			return Site{}, err
		}
		sites[i].Name = name
	}
	if u.Targets != nil {
		if id == DefaultID {
			if len(*u.Targets) > 0 {
				return Site{}, ErrDefaultImmutable
			}
		} else {
			parsed, err := assign(sites, id, *u.Targets, confirm, now)
			if err != nil {
				return Site{}, err
			}
			sites[i].Targets = parsed
		}
	}
	if u.HTML != nil {
		if _, err := ValidateHTML(u.HTML); err != nil {
			return Site{}, err
		}
	}
	sites[i].UpdatedAt = now

	var oldHTML []byte
	if u.HTML != nil {
		oldHTML, _ = s.readHTML(id)
		if err := s.writeHTML(id, u.HTML); err != nil {
			return Site{}, err
		}
	}
	if err := s.persist(sites); err != nil {
		if u.HTML != nil && oldHTML != nil {
			s.writeHTML(id, oldHTML) // best effort: keep content and metadata in step
		}
		return Site{}, err
	}
	s.sites = sites
	return cloneSite(sites[i]), s.publish()
}

// Delete removes a page. Its targets are freed, so they fall back to the default page once
// published. The default page cannot be deleted.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == DefaultID {
		return ErrDefaultImmutable
	}
	i := indexOf(s.sites, id)
	if i < 0 {
		return ErrNotFound
	}
	sites := cloneSites(s.sites)
	sites = append(sites[:i], sites[i+1:]...)
	if err := s.persist(sites); err != nil {
		return err
	}
	s.sites = sites
	os.Remove(s.htmlPath(id))
	return s.publish()
}

// --- internals (callers hold s.mu) ---

func (s *Store) now() time.Time { return s.opts.Now().UTC().Truncate(time.Second) }

func (s *Store) sitesPath() string    { return filepath.Join(s.opts.MetaDir, "sites.json") }
func (s *Store) pagesMetaDir() string { return filepath.Join(s.opts.MetaDir, "pages") }
func (s *Store) htmlPath(id string) string {
	return filepath.Join(s.pagesMetaDir(), id+".html")
}

func (s *Store) readHTML(id string) ([]byte, error) { return os.ReadFile(s.htmlPath(id)) }
func (s *Store) writeHTML(id string, data []byte) error {
	return atomic.WriteFile(s.htmlPath(id), data, 0o644)
}

type sitesFile struct {
	Sites []Site `json:"sites"`
}

func (s *Store) persist(sites []Site) error {
	data, err := json.MarshalIndent(sitesFile{Sites: sites}, "", "  ")
	if err != nil {
		return err
	}
	return atomic.WriteFile(s.sitesPath(), append(data, '\n'), 0o644)
}

func (s *Store) snapshot() (Snapshot, error) {
	ordered := cloneSites(s.sites)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].ID == DefaultID && ordered[j].ID != DefaultID })
	snap := Snapshot{Pages: make([]PageContent, 0, len(ordered))}
	for _, site := range ordered {
		html, err := s.readHTML(site.ID)
		if err != nil {
			return Snapshot{}, fmt.Errorf("read html of page %s: %w", site.ID, err)
		}
		snap.Pages = append(snap.Pages, PageContent{Site: site, HTML: html})
	}
	return snap, nil
}

func (s *Store) publish() error {
	if s.opts.Publish == nil {
		return nil
	}
	snap, err := s.snapshot()
	if err != nil {
		return err
	}
	if err := s.opts.Publish(snap); err != nil {
		return &PublishError{Err: err}
	}
	return nil
}

// load reads sites.json or bootstraps the default page.
func (s *Store) load() error {
	raw, err := os.ReadFile(s.sitesPath())
	var sites []Site
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return fmt.Errorf("read sites.json: %w", err)
	default:
		var f sitesFile
		if err := json.Unmarshal(raw, &f); err != nil {
			return fmt.Errorf("sites.json is corrupt (fix or remove it): %w", err)
		}
		if sites, err = validateSites(f.Sites); err != nil {
			return fmt.Errorf("sites.json is invalid: %w", err)
		}
	}

	changed := false
	if indexOf(sites, DefaultID) < 0 {
		sites = append(sites, Site{ID: DefaultID, Name: DefaultName, Targets: []Target{}, UpdatedAt: s.now()})
		changed = true
	}
	if _, err := os.Stat(s.htmlPath(DefaultID)); errors.Is(err, os.ErrNotExist) {
		html, err := s.initialDefaultHTML()
		if err != nil {
			return err
		}
		if err := s.writeHTML(DefaultID, html); err != nil {
			return err
		}
	}
	for _, site := range sites {
		if _, err := os.Stat(s.htmlPath(site.ID)); err != nil {
			return fmt.Errorf("page %s has no stored HTML: %w", site.ID, err)
		}
	}
	if changed {
		if err := s.persist(sites); err != nil {
			return err
		}
	}
	s.sites = sites
	return nil
}

func (s *Store) initialDefaultHTML() ([]byte, error) {
	if seeded, err := os.ReadFile(filepath.Join(s.opts.PagesDir, "default.html")); err == nil && len(seeded) > 0 {
		return seeded, nil
	}
	return s.Render(DefaultParams(s.opts.Refresh))
}

func validateSites(in []Site) ([]Site, error) {
	seenID := map[string]bool{}
	seenTarget := map[Target]string{}
	out := make([]Site, 0, len(in))
	for _, site := range in {
		if !idRe.MatchString(site.ID) {
			return nil, fmt.Errorf("unsafe page id %q", site.ID)
		}
		if seenID[site.ID] {
			return nil, fmt.Errorf("duplicate page id %q", site.ID)
		}
		seenID[site.ID] = true
		if site.ID == DefaultID && len(site.Targets) > 0 {
			return nil, errors.New("the default page cannot own targets")
		}
		targets := make([]Target, 0, len(site.Targets))
		for _, t := range site.Targets {
			parsed, err := ParseTarget(t.Value)
			if err != nil || parsed != t {
				return nil, fmt.Errorf("page %s has an invalid target %q", site.ID, t.Value)
			}
			if owner, dup := seenTarget[parsed]; dup {
				return nil, fmt.Errorf("target %q is assigned to pages %s and %s", t.Value, owner, site.ID)
			}
			seenTarget[parsed] = site.ID
			targets = append(targets, parsed)
		}
		site.Targets = targets
		out = append(out, site)
	}
	return out, nil
}

// assign parses raw targets for page ownerID ("" for a new page), resolves ownership conflicts in
// sites (which must be a private copy) and returns the page's normalized targets.
func assign(sites []Site, ownerID string, raw []string, confirm bool, now time.Time) ([]Target, error) {
	var parsed []Target
	seen := map[Target]bool{}
	for _, r := range raw {
		t, err := ParseTarget(r)
		if err != nil {
			return nil, err
		}
		if !seen[t] {
			seen[t] = true
			parsed = append(parsed, t)
		}
	}
	if len(parsed) == 0 {
		return nil, ErrNoTargets
	}
	var conflicts []Conflict
	for _, t := range parsed {
		for _, site := range sites {
			if site.ID == ownerID {
				continue
			}
			for _, owned := range site.Targets {
				if owned == t {
					conflicts = append(conflicts, Conflict{Target: t, OwnerID: site.ID, OwnerName: site.Name})
				}
			}
		}
	}
	if len(conflicts) > 0 && !confirm {
		return nil, &ConflictError{Conflicts: conflicts}
	}
	for _, c := range conflicts {
		i := indexOf(sites, c.OwnerID)
		kept := sites[i].Targets[:0]
		for _, t := range sites[i].Targets {
			if t != c.Target {
				kept = append(kept, t)
			}
		}
		sites[i].Targets = kept
		sites[i].UpdatedAt = now
	}
	return parsed, nil
}

func cleanName(name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", fmt.Errorf("%w: the name is required", ErrInvalidName)
	case !utf8.ValidString(name) || utf8.RuneCountInString(name) > MaxNameRunes:
		return "", fmt.Errorf("%w: use valid text of at most %d characters", ErrInvalidName, MaxNameRunes)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("%w: the name contains control characters", ErrInvalidName)
		}
	}
	return name, nil
}

// newID returns a random 16-hex-digit id that is filename-safe and not in use.
func (s *Store) newID() string {
	for {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			panic("crypto/rand unavailable: " + err.Error())
		}
		id := hex.EncodeToString(b[:])
		if indexOf(s.sites, id) < 0 {
			return id
		}
	}
}

func indexOf(sites []Site, id string) int {
	for i, site := range sites {
		if site.ID == id {
			return i
		}
	}
	return -1
}

func cloneSite(s Site) Site {
	s.Targets = append(make([]Target, 0, len(s.Targets)), s.Targets...)
	return s
}

func cloneSites(in []Site) []Site {
	out := make([]Site, len(in))
	for i, s := range in {
		out[i] = cloneSite(s)
	}
	return out
}

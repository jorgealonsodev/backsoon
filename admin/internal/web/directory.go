package web

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jorgealonsodev/backsoon/admin/internal/auth"
	"github.com/jorgealonsodev/backsoon/admin/internal/pages"
)

func kindOf(loc locale, t pages.Target) (label string, isService bool) {
	if t.Type == pages.TargetService {
		return loc.T("kind.service"), true
	}
	return loc.T("kind.domain"), false
}

func destsOf(loc locale, targets []pages.Target, lit pages.Target, litSet bool) []destView {
	out := make([]destView, 0, len(targets))
	for _, t := range targets {
		kind, isService := kindOf(loc, t)
		out = append(out, destView{Value: t.Value, Kind: kind, IsService: isService, Hit: litSet && t == lit})
	}
	return out
}

// dateParts renders a modification time as an absolute UTC date.
func dateParts(t time.Time) (iso, text string) {
	t = t.UTC()
	return t.Format(time.RFC3339), t.Format("2006-01-02 15:04") + " UTC"
}

func noticeFor(loc locale, key string) *noticeView {
	warn, ok := notices[key]
	if !ok {
		return nil
	}
	return &noticeView{Text: loc.T("notice." + key), Warn: warn}
}

// reasonOf is the human part of a pages.ParseTarget error, in the language of loc.
func reasonOf(loc locale, err error) string {
	var te *pages.TargetError
	if !errors.As(err, &te) {
		return err.Error()
	}
	switch te.Problem {
	case pages.TargetEmpty:
		return loc.T("target.empty")
	case pages.TargetBadDomain:
		return loc.T("target.domain")
	case pages.TargetBadService:
		return loc.T("target.service")
	case pages.TargetBadPort:
		return loc.T("target.port")
	}
	return loc.T("target.portrange")
}

type lookupOutcome struct {
	view      lookupView
	pageID    string
	target    pages.Target
	targetSet bool
	toDefault bool
}

// resolveLookup answers "which page does this destination show?" in backsoon's own order:
// a page owning the domain, then a page owning the service:port, then the default page.
func resolveLookup(loc locale, sites []pages.Site, raw string) lookupOutcome {
	raw = strings.TrimSpace(raw)
	out := lookupOutcome{view: lookupView{Query: raw}}
	if raw == "" {
		return out
	}
	if strings.Contains(raw, "/") {
		out.view.Error = loc.T("lookup.err.path")
		return out
	}
	t, err := pages.ParseTarget(raw)
	if err != nil {
		out.view.Error = loc.T("lookup.err.invalid", raw, reasonOf(loc, err))
		return out
	}
	out.view.Query = t.Value
	_, isService := kindOf(loc, t)

	var defaultName string
	for _, site := range sites {
		if site.ID == pages.DefaultID {
			defaultName = site.Name
		}
		for _, owned := range site.Targets {
			if owned != t {
				continue
			}
			note := loc.T("lookup.note.exact")
			if isService {
				note = loc.T("lookup.note.service")
			}
			out.pageID, out.target, out.targetSet = site.ID, t, true
			out.view.Answer = &answerView{Dest: t.Value, IsService: isService, PageName: site.Name, Note: note}
			return out
		}
	}
	note := loc.T("lookup.note.none", t.Value)
	if isService {
		note = loc.T("lookup.note.none.svc", t.Value)
	}
	out.pageID, out.toDefault = pages.DefaultID, true
	out.view.Answer = &answerView{Dest: t.Value, IsService: isService, PageName: defaultName, Note: note}
	return out
}

func (s *Server) directory(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	q := r.URL.Query()
	loc := localeOf(r)
	sites := s.store.List() // default page first, then by name
	look := resolveLookup(loc, sites, q.Get("lookup"))
	hasAnswer := look.view.Answer != nil

	v := &directoryView{base: s.base(sess, loc.T("title.directory")), Lookup: look.view, Empty: len(sites) == 1}
	v.Notice = noticeFor(loc, q.Get("notice"))

	focusID := q.Get("focus")
	entries := make([]entryView, 0, len(sites))
	for _, site := range sites {
		iso, text := dateParts(site.UpdatedAt)
		e := entryView{
			ID: site.ID, Name: site.Name, UpdatedISO: iso, UpdatedText: text,
			IsDefault: site.ID == pages.DefaultID,
			Dests:     destsOf(loc, site.Targets, look.target, look.targetSet),
			Focus:     focusID != "" && site.ID == focusID,
		}
		e.NoDests = !e.IsDefault && len(site.Targets) == 0
		e.Hit = hasAnswer && look.pageID == site.ID
		if e.Focus {
			v.Focus = &focusView{ID: site.ID, Name: site.Name, Title: loc.T("dir.preview.title", site.Name)}
		}
		entries = append(entries, e)
	}
	// The default page closes the board as "everything else".
	if len(entries) > 1 && entries[0].IsDefault {
		entries = append(entries[1:], entries[0])
	}
	v.Entries = entries
	s.render(w, r, http.StatusOK, "directory", v)
}

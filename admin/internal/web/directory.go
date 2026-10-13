package web

import (
	"net/http"
	"strings"
	"time"

	"github.com/jorgealonsodev/backsoon/admin/internal/auth"
	"github.com/jorgealonsodev/backsoon/admin/internal/pages"
)

func kindOf(t pages.Target) (label string, isService bool) {
	if t.Type == pages.TargetService {
		return "service:port", true
	}
	return "domain", false
}

func destsOf(targets []pages.Target, lit pages.Target, litSet bool) []destView {
	out := make([]destView, 0, len(targets))
	for _, t := range targets {
		kind, isService := kindOf(t)
		out = append(out, destView{Value: t.Value, Kind: kind, IsService: isService, Hit: litSet && t == lit})
	}
	return out
}

// dateParts renders a modification time as an absolute UTC date.
func dateParts(t time.Time) (iso, text string) {
	t = t.UTC()
	return t.Format(time.RFC3339), t.Format("2006-01-02 15:04") + " UTC"
}

func noticeFor(key string) *noticeView {
	if n, ok := notices[key]; ok {
		return &n
	}
	return nil
}

// reasonOf extracts the human part of a pages.ParseTarget error.
func reasonOf(err error) string {
	msg := err.Error()
	if i := strings.LastIndex(msg, `": `); i >= 0 {
		return msg[i+3:]
	}
	if _, rest, ok := strings.Cut(msg, ": "); ok {
		return rest
	}
	return msg
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
func resolveLookup(sites []pages.Site, raw string) lookupOutcome {
	raw = strings.TrimSpace(raw)
	out := lookupOutcome{view: lookupView{Query: raw}}
	if raw == "" {
		return out
	}
	if strings.Contains(raw, "/") {
		out.view.Error = "Enter only the domain or the service:port, without http:// or a path."
		return out
	}
	t, err := pages.ParseTarget(raw)
	if err != nil {
		out.view.Error = "“" + raw + "” is not a valid destination: " + reasonOf(err)
		return out
	}
	out.view.Query = t.Value
	_, isService := kindOf(t)

	var defaultName string
	for _, site := range sites {
		if site.ID == pages.DefaultID {
			defaultName = site.Name
		}
		for _, owned := range site.Targets {
			if owned != t {
				continue
			}
			note := "Exact domain match: this page wins over every other rule."
			if isService {
				note = "Service:port match. This page is shown when the request's domain has no page of its own."
			}
			out.pageID, out.target, out.targetSet = site.ID, t, true
			out.view.Answer = &answerView{Dest: t.Value, IsService: isService, PageName: site.Name, Note: note}
			return out
		}
	}
	note := "No page is assigned to " + t.Value + ". If its NPM proxy host forwards to a service:port that has a page, visitors see that page; otherwise they see the default page."
	if isService {
		note = "No page is assigned to " + t.Value + ". Unless the request's domain has its own page, visitors see the default page."
	}
	out.pageID, out.toDefault = pages.DefaultID, true
	out.view.Answer = &answerView{Dest: t.Value, IsService: isService, PageName: defaultName, Note: note}
	return out
}

func (s *Server) directory(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	q := r.URL.Query()
	sites := s.store.List() // default page first, then by name
	look := resolveLookup(sites, q.Get("lookup"))
	hasAnswer := look.view.Answer != nil

	v := &directoryView{base: s.base(sess, "Pages"), Lookup: look.view, Empty: len(sites) == 1}
	v.Notice = noticeFor(q.Get("notice"))

	focusID := q.Get("focus")
	entries := make([]entryView, 0, len(sites))
	for _, site := range sites {
		iso, text := dateParts(site.UpdatedAt)
		e := entryView{
			ID: site.ID, Name: site.Name, UpdatedISO: iso, UpdatedText: text,
			IsDefault: site.ID == pages.DefaultID,
			Dests:     destsOf(site.Targets, look.target, look.targetSet),
			Focus:     focusID != "" && site.ID == focusID,
		}
		e.NoDests = !e.IsDefault && len(site.Targets) == 0
		e.Hit = hasAnswer && look.pageID == site.ID
		if e.Focus {
			v.Focus = &focusView{ID: site.ID, Name: site.Name, Title: "Preview of " + site.Name}
		}
		entries = append(entries, e)
	}
	// The default page closes the board as "everything else".
	if len(entries) > 1 && entries[0].IsDefault {
		entries = append(entries[1:], entries[0])
	}
	v.Entries = entries
	s.render(w, http.StatusOK, "directory", v)
}

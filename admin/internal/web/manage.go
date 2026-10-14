package web

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/jorgealonsodev/backsoon/admin/internal/auth"
	"github.com/jorgealonsodev/backsoon/admin/internal/pages"
)

const (
	maxDestinations = 100
	smallBodyMax    = 16 << 10
)

// fieldOrder is the order of the error summary; anchors are the ids of the controls, and the
// labels are catalogue keys ("field.<id>").
var fieldOrder = []string{"name", "destinations", "file", "app_name", "message", "lang", "icon"}

func defaultForm() formValues {
	return formValues{Source: "template", Lang: pages.DefaultLang, Message: pages.DefaultParams(0).Message}
}

func formOf(site pages.Site) formValues {
	f := defaultForm()
	f.Name = site.Name
	vals := make([]string, 0, len(site.Targets))
	for _, t := range site.Targets {
		vals = append(vals, t.Value)
	}
	f.Destinations = strings.Join(vals, "\n")
	return f
}

func (s *Server) newFormView(loc locale, sess auth.Session, form formValues) *formView {
	return &formView{
		base: s.base(sess, loc.T("title.new")), Action: "/pages", Form: form, Errs: map[string][]issue{},
	}
}

func (s *Server) editFormView(loc locale, sess auth.Session, site pages.Site, form formValues) *formView {
	iso, text := dateParts(site.UpdatedAt)
	isDefault := site.ID == pages.DefaultID
	return &formView{
		base: s.base(sess, loc.T("title.edit", site.Name)), Edit: true, IsDefault: isDefault,
		Action: "/pages/" + site.ID, PageID: site.ID, PageName: site.Name,
		UpdatedISO: iso, UpdatedText: text, NoDests: !isDefault && len(site.Targets) == 0,
		Form: form, Errs: map[string][]issue{}, PreviewSrc: "/pages/" + site.ID + "/preview",
		DeleteURL: "/pages/" + site.ID + "/delete",
	}
}

// summarize builds the error summary from the field issues.
func (v *formView) summarize(loc locale) {
	v.Summary = nil
	for _, field := range fieldOrder {
		if issues := v.Errs[field]; len(issues) > 0 {
			text := issues[0].Text
			if len(issues) > 1 {
				text = loc.T("form.problems", len(issues))
			}
			v.Summary = append(v.Summary, summaryItem{Anchor: field, Text: loc.T("field."+field) + ": " + text})
		}
	}
}

func (v *formView) keep(d *draft, id string) {
	v.DraftID = id
	if d.html != nil && d.fileName != "" {
		v.Kept = &keptFile{Name: d.fileName, Size: kbText(len(d.html))}
	}
}

func kbText(n int) string { return fmt.Sprintf("%d KB", (n+1023)/1024) }

func (s *Server) newPage(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	form := defaultForm()
	v := s.newFormView(localeOf(r), sess, form)
	if d, ok := s.drafts.load(r.URL.Query().Get("draft"), sess.ID); ok && d.pageID == "" {
		v.Form = d.form
		v.keep(d, d.id)
	}
	s.render(w, r, http.StatusOK, "form", v)
}

func (s *Server) editPage(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	site, err := s.store.Get(r.PathValue("id"))
	if err != nil {
		s.notFound(w, r, sess)
		return
	}
	v := s.editFormView(localeOf(r), sess, site, formOf(site))
	if d, ok := s.drafts.load(r.URL.Query().Get("draft"), sess.ID); ok && d.pageID == site.ID {
		v.Form = d.form
		v.keep(d, d.id)
	}
	s.render(w, r, http.StatusOK, "form", v)
}

func (s *Server) createPage(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	s.submit(w, r, sess, "")
}

func (s *Server) updatePage(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	s.submit(w, r, sess, r.PathValue("id"))
}

// splitDestinations accepts one destination per line or separated by commas.
func splitDestinations(text string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == '\r' || r == ',' }) {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

type fileUpload struct {
	name string
	data []byte
}

// readUpload returns the HTML file of the request, if one was chosen. A browser sends an empty,
// nameless file part when the field was left alone; that counts as no file.
func readUpload(r *http.Request) (*fileUpload, error) {
	f, hdr, err := r.FormFile("file")
	if err != nil {
		return nil, nil // no file part (or not a multipart request)
	}
	defer f.Close()
	if hdr.Filename == "" && hdr.Size == 0 {
		return nil, nil
	}
	data, err := io.ReadAll(io.LimitReader(f, pages.MaxHTMLBytes+1))
	if err != nil {
		return nil, err
	}
	return &fileUpload{name: hdr.Filename, data: data}, nil
}

func hasHTMLExt(name string) bool {
	n := strings.ToLower(name)
	return strings.HasSuffix(n, ".html") || strings.HasSuffix(n, ".htm")
}

// contentIssue maps a pages content error to its message and status.
func contentIssue(loc locale, err error) (text string, status int, ok bool) {
	switch {
	case errors.Is(err, pages.ErrTooLarge):
		return loc.T("val.toolarge"), http.StatusRequestEntityTooLarge, true
	case errors.Is(err, pages.ErrNotUTF8):
		return loc.T("val.notutf8"), http.StatusUnprocessableEntity, true
	case errors.Is(err, pages.ErrEmptyHTML):
		return loc.T("val.empty"), http.StatusUnprocessableEntity, true
	}
	return "", 0, false
}

// nameIssue is the message of a refused page name.
func nameIssue(loc locale, err error) string {
	var ne *pages.NameError
	if errors.As(err, &ne) {
		switch ne.Problem {
		case pages.NameRequired:
			return loc.T("name.required")
		case pages.NameInvalidText:
			return loc.T("name.text", ne.Max)
		case pages.NameControl:
			return loc.T("name.control")
		}
	}
	return err.Error()
}

// paramIssue maps a template validation error to the form field it belongs to and its message.
func paramIssue(loc locale, err error) (field, text string) {
	var pe *pages.ParamError
	if !errors.As(err, &pe) {
		return "app_name", err.Error()
	}
	switch pe.Problem {
	case pages.ParamBadLang:
		return "lang", loc.T("param.lang.bad")
	case pages.ParamBadRefresh: // not a form field; the panel never sends one
		return "app_name", loc.T("param.refresh.bad")
	case pages.ParamRequired:
		return pe.Field, loc.T("param." + pe.Field + ".required")
	case pages.ParamControl:
		return pe.Field, loc.T("param." + pe.Field + ".control")
	}
	return pe.Field, loc.T("param."+pe.Field+".text", pe.Max)
}

// warningText is the message of a content warning.
func warningText(loc locale, w pages.Warning) string {
	if w.Code == pages.WarningExternalResources {
		return loc.T("warn.external", strings.Join(w.Detail, ", "))
	}
	return loc.T("warn.script")
}

// submit handles POST /pages (id == "") and POST /pages/{id}: validation, the confirmation steps
// (content warnings, destination moves) and the store call.
func (s *Server) submit(w http.ResponseWriter, r *http.Request, sess auth.Session, id string) {
	loc := localeOf(r)
	edit := id != ""
	r.Body = http.MaxBytesReader(w, r.Body, s.uploadBodyMax)
	parseErr := r.ParseMultipartForm(1 << 20)
	if errors.Is(parseErr, http.ErrNotMultipart) {
		parseErr = nil
	}

	var site pages.Site
	if edit {
		var err error
		if site, err = s.store.Get(id); err != nil {
			if parseErr == nil && !s.validCSRF(sess, r) {
				s.csrfFailed(w, r, sess)
				return
			}
			s.notFound(w, r, sess)
			return
		}
	}
	formViewFor := func(form formValues) *formView {
		if edit {
			return s.editFormView(loc, sess, site, form)
		}
		return s.newFormView(loc, sess, form)
	}
	blank := defaultForm()
	if edit {
		blank = formOf(site)
	}

	if parseErr != nil {
		var tooBig *http.MaxBytesError
		v := formViewFor(blank)
		status := http.StatusBadRequest
		text := loc.T("request.unreadable")
		if errors.As(parseErr, &tooBig) {
			status, text = http.StatusRequestEntityTooLarge, loc.T("val.toolarge")
		}
		v.Errs["file"] = []issue{{Text: text}}
		v.summarize(loc)
		s.render(w, r, status, "form", v)
		return
	}
	if !s.validCSRF(sess, r) {
		s.csrfFailed(w, r, sess)
		return
	}

	form := formValues{
		Name: r.PostFormValue("name"), Destinations: r.PostFormValue("destinations"), Source: r.PostFormValue("source"),
		AppName: r.PostFormValue("app_name"), Message: r.PostFormValue("message"),
		Lang: r.PostFormValue("lang"), Icon: r.PostFormValue("icon"),
	}
	confirm := r.PostFormValue("confirm")
	stepConfirm := confirm == "warnings" || confirm == "move"

	var dr *draft
	missing := stepConfirm
	if did := r.PostFormValue("draft"); did != "" {
		if d, ok := s.drafts.load(did, sess.ID); ok && d.pageID == id {
			dr, missing = d, false
		} else {
			missing = true
		}
	}
	if missing {
		if stepConfirm {
			form = blank
		}
		v := formViewFor(form)
		v.Errs["file"] = []issue{{Text: loc.T("val.expired")}}
		v.summarize(loc)
		s.render(w, r, http.StatusGone, "form", v)
		return
	}

	var (
		html       []byte
		fileName   string
		warningsOK bool
		moveOK     bool
		status     = http.StatusUnprocessableEntity
		errs       = map[string][]issue{}
	)
	addIssue := func(field, subject, text string) {
		errs[field] = append(errs[field], issue{Subject: subject, Text: text})
	}

	if dr != nil && stepConfirm {
		form, html, fileName = dr.form, dr.html, dr.fileName
		warningsOK, moveOK = dr.warningsOK, dr.moveOK
		if confirm == "warnings" {
			warningsOK = true
		} else {
			moveOK = true
		}
	} else {
		if confirm == "true" { // one-shot confirmation of a complete submission (scripts)
			warningsOK, moveOK = true, true
		}
		file, err := readUpload(r)
		if err != nil {
			addIssue("file", "", loc.T("val.file.unread"))
		}
		source := form.Source
		if !edit {
			if source != "template" && source != "upload" {
				source = "template"
				if file != nil || (dr != nil && dr.html != nil) {
					source = "upload"
				}
			}
			form.Source = source
		}
		if file != nil && (edit || source == "upload") {
			switch {
			case !hasHTMLExt(file.name):
				addIssue("file", "", loc.T("val.file.ext"))
			default:
				if _, err := pages.Inspect(file.data); err != nil {
					text, st, ok := contentIssue(loc, err)
					if !ok {
						text, st = loc.T("val.empty"), http.StatusUnprocessableEntity
					}
					addIssue("file", "", text)
					status = st
				} else {
					html, fileName = file.data, file.name
				}
			}
		} else if dr != nil && dr.html != nil && (edit || source == "upload") {
			html, fileName = dr.html, dr.fileName
		}
		if !edit && source == "upload" && html == nil && len(errs["file"]) == 0 {
			addIssue("file", "", loc.T("val.file.choose"))
		}
		if !edit && source == "template" {
			out, err := s.store.Render(pages.TemplateParams{AppName: form.AppName, Message: form.Message, Lang: form.Lang, Icon: form.Icon})
			switch {
			case err == nil:
				html, fileName = out, ""
			case errors.Is(err, pages.ErrInvalidTemplateParams):
				field, msg := paramIssue(loc, err)
				addIssue(field, "", msg)
			default:
				s.internalError(w, r, sess, "render template", err)
				return
			}
		}
	}

	if strings.TrimSpace(form.Name) == "" {
		addIssue("name", "", loc.T("val.name.required"))
	}

	var targets []string
	entries := splitDestinations(form.Destinations)
	switch {
	case id == pages.DefaultID:
		if len(entries) > 0 {
			s.errorPage(w, r, sess, http.StatusConflict, loc.T("err.defdests.title"), loc.T("err.defdests.body"),
				"/pages/"+id, loc.T("back.page"))
			return
		}
	case len(entries) > maxDestinations:
		addIssue("destinations", "", loc.T("val.dest.toomany", maxDestinations))
	default:
		for _, e := range entries {
			t, err := pages.ParseTarget(e)
			if err != nil {
				addIssue("destinations", e, reasonOf(loc, err))
				continue
			}
			targets = append(targets, t.Value)
		}
		if len(entries) == 0 {
			switch {
			case !edit:
				addIssue("destinations", "", loc.T("val.dest.atleast"))
			case len(site.Targets) > 0:
				addIssue("destinations", "", loc.T("val.dest.atleast.del"))
			}
		}
	}

	stash := func(warn, move bool) string {
		if dr != nil {
			s.drafts.drop(dr.id)
		}
		return s.drafts.save(&draft{
			session: sess.ID, pageID: id, form: form, html: html, fileName: fileName, warningsOK: warn, moveOK: move,
		})
	}
	failForm := func() {
		v := formViewFor(form)
		v.Errs = errs
		v.summarize(loc)
		if html != nil && fileName != "" { // keep an upload so it need not be chosen again
			did := stash(false, false)
			if d, ok := s.drafts.load(did, sess.ID); ok {
				v.keep(d, did)
			}
		}
		s.render(w, r, status, "form", v)
	}
	if len(errs) > 0 {
		failForm()
		return
	}

	var warnings []string
	if html != nil {
		found, _ := pages.Inspect(html)
		for _, wn := range found {
			warnings = append(warnings, warningText(loc, wn))
		}
	}
	if len(warnings) > 0 && !warningsOK {
		did := stash(false, moveOK)
		s.render(w, r, http.StatusOK, "confirm", &confirmView{
			base: s.base(sess, loc.T("title.check")), Kind: "warnings", Action: formAction(id), DraftID: did,
			PageName: strings.TrimSpace(form.Name), Edit: edit, Warnings: warnings,
			Dests: destsFromValues(loc, targets), BackHref: backHref(id, did),
		})
		return
	}

	name := strings.TrimSpace(form.Name)
	var saved pages.Site
	var err error
	if edit {
		u := pages.Update{Name: &name, HTML: html}
		if id != pages.DefaultID && len(entries) > 0 {
			u.Targets = &targets
		}
		saved, err = s.store.Update(id, u, moveOK)
	} else {
		saved, err = s.store.Create(name, targets, html, moveOK)
	}

	var pubErr *pages.PublishError
	var conflict *pages.ConflictError
	switch {
	case err == nil:
	case errors.As(err, &pubErr):
		s.log.Printf("publish failed id=%s: %v", saved.ID, pubErr.Err)
	case errors.As(err, &conflict):
		did := stash(true, false)
		s.render(w, r, http.StatusConflict, "confirm", &confirmView{
			base: s.base(sess, loc.T("title.move")), Kind: "move", Action: formAction(id), DraftID: did,
			PageName: name, Edit: edit, Groups: s.moveGroups(loc, conflict, id), BackHref: backHref(id, did),
		})
		return
	case errors.Is(err, pages.ErrNotFound):
		s.notFound(w, r, sess)
		return
	case errors.Is(err, pages.ErrDefaultImmutable):
		s.errorPage(w, r, sess, http.StatusConflict, loc.T("err.defdests.title"), loc.T("err.defdests.short"), "/pages/"+id, loc.T("back.page"))
		return
	case errors.Is(err, pages.ErrInvalidName):
		addIssue("name", "", nameIssue(loc, err))
		failForm()
		return
	case errors.Is(err, pages.ErrNoTargets):
		addIssue("destinations", "", loc.T("val.dest.atleast.del"))
		failForm()
		return
	default:
		if text, st, ok := contentIssue(loc, err); ok {
			addIssue("file", "", text)
			status = st
			failForm()
			return
		}
		s.internalError(w, r, sess, "save page", err)
		return
	}

	if dr != nil {
		s.drafts.drop(dr.id)
	}
	dests := make([]string, 0, len(saved.Targets))
	for _, t := range saved.Targets {
		dests = append(dests, t.Value)
	}
	verb, notice := "created", "created"
	if edit {
		verb, notice = "updated", "saved"
	}
	s.log.Printf("page %s id=%s name=%q destinations=%v", verb, saved.ID, saved.Name, dests)
	if pubErr != nil {
		notice = "publish-failed"
	}
	http.Redirect(w, r, "/?notice="+notice+"&focus="+url.QueryEscape(saved.ID)+"#page-"+url.QueryEscape(saved.ID), http.StatusSeeOther)
}

func formAction(id string) string {
	if id == "" {
		return "/pages"
	}
	return "/pages/" + id
}

func backHref(id, draftID string) string {
	if id == "" {
		return "/pages/new?draft=" + draftID
	}
	return "/pages/" + id + "?draft=" + draftID
}

func destsFromValues(loc locale, values []string) []destView {
	out := make([]destView, 0, len(values))
	for _, v := range values {
		if t, err := pages.ParseTarget(v); err == nil {
			kind, isService := kindOf(loc, t)
			out = append(out, destView{Value: t.Value, Kind: kind, IsService: isService})
		}
	}
	return out
}

// moveGroups groups the conflicting destinations by their current owner and flags owners that
// would be left with nothing.
func (s *Server) moveGroups(loc locale, c *pages.ConflictError, newOwnerID string) []moveGroup {
	var groups []moveGroup
	index := map[string]int{}
	moved := map[string]map[pages.Target]bool{}
	for _, conf := range c.Conflicts {
		i, ok := index[conf.OwnerID]
		if !ok {
			i = len(groups)
			index[conf.OwnerID] = i
			groups = append(groups, moveGroup{OwnerName: conf.OwnerName, OwnerID: conf.OwnerID})
			moved[conf.OwnerID] = map[pages.Target]bool{}
		}
		kind, isService := kindOf(loc, conf.Target)
		groups[i].Dests = append(groups[i].Dests, destView{Value: conf.Target.Value, Kind: kind, IsService: isService})
		moved[conf.OwnerID][conf.Target] = true
	}
	for i, g := range groups {
		owner, err := s.store.Get(g.OwnerID)
		if err != nil || g.OwnerID == pages.DefaultID {
			continue
		}
		left := 0
		for _, t := range owner.Targets {
			if !moved[g.OwnerID][t] {
				left++
			}
		}
		groups[i].LeftEmpty = left == 0
	}
	return groups
}

func (s *Server) deletePage(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	id := r.PathValue("id")
	r.Body = http.MaxBytesReader(w, r.Body, smallBodyMax)
	if err := r.ParseForm(); err != nil || !s.validCSRF(sess, r) {
		s.csrfFailed(w, r, sess)
		return
	}
	site, err := s.store.Get(id)
	if err != nil {
		s.notFound(w, r, sess)
		return
	}
	if id == pages.DefaultID {
		loc := localeOf(r)
		s.errorPage(w, r, sess, http.StatusConflict, loc.T("err.defdelete.title"), loc.T("err.defdelete.body"),
			"/pages/"+id, loc.T("back.page"))
		return
	}
	dests := make([]string, 0, len(site.Targets))
	for _, t := range site.Targets {
		dests = append(dests, t.Value)
	}
	if r.PostFormValue("confirm") != "true" {
		defaultName := pages.DefaultName
		if d, err := s.store.Get(pages.DefaultID); err == nil {
			defaultName = d.Name
		}
		loc := localeOf(r)
		s.render(w, r, http.StatusOK, "confirm", &confirmView{
			base: s.base(sess, loc.T("title.delete", site.Name)), Kind: "delete", Action: "/pages/" + id + "/delete",
			PageName: site.Name, Dests: destsOf(loc, site.Targets, pages.Target{}, false),
			BackHref: "/pages/" + id, DefaultName: defaultName,
		})
		return
	}
	notice := "deleted"
	err = s.store.Delete(id)
	var pubErr *pages.PublishError
	switch {
	case err == nil:
	case errors.As(err, &pubErr):
		s.log.Printf("publish failed id=%s: %v", id, pubErr.Err)
		notice = "publish-failed"
	case errors.Is(err, pages.ErrNotFound):
		s.notFound(w, r, sess)
		return
	default:
		s.internalError(w, r, sess, "delete page", err)
		return
	}
	s.log.Printf("page deleted id=%s name=%q destinations=%v", id, site.Name, dests)
	http.Redirect(w, r, "/?notice="+notice, http.StatusSeeOther)
}

// preview serves a page's HTML for the sandboxed iframe (RF-18). The policy lets the document
// run nothing and load nothing, whatever the uploaded HTML contains.
func (s *Server) preview(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	html, err := s.store.HTML(r.PathValue("id"))
	if err != nil {
		s.notFound(w, r, sess)
		return
	}
	h := w.Header()
	h.Set("Content-Security-Policy", previewCSP)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Frame-Options", "SAMEORIGIN")
	h.Set("Content-Type", "text/html; charset=utf-8")
	w.Write(html)
}

package pages

import (
	"errors"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxHTMLBytes is the largest accepted page (RF-15): 512 KB.
const MaxHTMLBytes = 512 * 1024

// Content validation errors.
var (
	ErrEmptyHTML = errors.New("the page is empty")
	ErrNotUTF8   = errors.New("the page is not valid UTF-8")
	ErrTooLarge  = errors.New("the page is larger than 512 KB")
)

// Warning texts returned by ValidateHTML.
const (
	WarnExternalResources = "The page references external resources (%s); backsoon pages should be self-contained"
	WarnScript            = "The page contains script-capable markup (%s); backsoon blocks it, so it will not run for visitors"
)

var (
	scriptRe      = regexp.MustCompile(`(?i)<script\b`)
	scriptSrcRe   = regexp.MustCompile(`(?i)<script\b[^>]*\bsrc\b`)
	linkRe        = regexp.MustCompile(`(?i)<link[\s/>]`)
	importRe      = regexp.MustCompile(`(?i)@import\b`)
	urlHTTPRe     = regexp.MustCompile(`(?i)url\(\s*["']?\s*https?:`)
	externalKinds = []struct {
		re   *regexp.Regexp
		name string
	}{
		{scriptSrcRe, "<script src>"}, {linkRe, "<link>"}, {importRe, "@import"}, {urlHTTPRe, "url(http…)"},
	}

	// Script-capable markup. Advisory only: the CSP on every maintenance response is what keeps
	// it from running, so these favour catching variants over precision.
	handlerRe = regexp.MustCompile(`(?i)<[a-z][^>]*[\s/"']on[a-z]+\s*=`)
	// javascript followed by a colon, raw or as an entity (&#58; &#x3a; &colon;), anywhere: URLs
	// in attributes, meta refresh and CSS url().
	jsURLRe       = regexp.MustCompile(`(?i)javascript\s*(?::|&#0*58;?|&#x0*3a;?|&colon;)`)
	embedRe       = regexp.MustCompile(`(?i)<(?:iframe|frame|object|embed|applet|base)\b`)
	scriptCapable = []struct {
		re   *regexp.Regexp
		name string
	}{
		{scriptRe, "<script>"}, {handlerRe, "inline event handler"}, {jsURLRe, "javascript: URL"},
		{embedRe, "<iframe>, <object>, <embed> or <base>"},
	}
)

// WarningCode is the stable code of a content warning.
type WarningCode string

// The content warnings.
const (
	WarningExternalResources WarningCode = "external-resources"
	WarningScript            WarningCode = "script"
)

// Warning is a non-blocking finding in a page. Detail lists the kinds found (references for
// WarningExternalResources, script-capable markup for WarningScript); String is the English text.
type Warning struct {
	Code   WarningCode
	Detail []string
}

func (w Warning) String() string {
	if w.Code == WarningExternalResources {
		return fmt.Sprintf(WarnExternalResources, strings.Join(w.Detail, ", "))
	}
	return fmt.Sprintf(WarnScript, strings.Join(w.Detail, ", "))
}

// Inspect is ValidateHTML with structured warnings, for callers that show them in another
// language.
func Inspect(data []byte) (warnings []Warning, err error) {
	switch {
	case len(data) == 0:
		return nil, ErrEmptyHTML
	case len(data) > MaxHTMLBytes:
		return nil, ErrTooLarge
	case !utf8.Valid(data):
		return nil, ErrNotUTF8
	}
	var found []string
	for _, k := range externalKinds {
		if k.re.Match(data) {
			found = append(found, k.name)
		}
	}
	if len(found) > 0 {
		warnings = append(warnings, Warning{Code: WarningExternalResources, Detail: found})
	}
	var scripts []string
	for _, k := range scriptCapable {
		if k.re.Match(data) {
			scripts = append(scripts, k.name)
		}
	}
	if len(scripts) > 0 {
		warnings = append(warnings, Warning{Code: WarningScript, Detail: scripts})
	}
	return warnings, nil
}

// ValidateHTML rejects pages that are empty, not UTF-8 or over MaxHTMLBytes, and returns
// non-blocking warnings (RF-17): external resources (RF-11) and script-capable markup.
func ValidateHTML(data []byte) (warnings []string, err error) {
	found, err := Inspect(data)
	for _, w := range found {
		warnings = append(warnings, w.String())
	}
	return warnings, err
}

// Template limits.
const (
	MaxNameRunes    = 100
	MaxMessageRunes = 500
	MaxIconRunes    = 16
	DefaultLang     = "es"
	DefaultRefresh  = 10
)

// ErrInvalidTemplateParams is wrapped by every RenderTemplate validation failure.
var ErrInvalidTemplateParams = errors.New("invalid template values")

// ParamProblem is the stable code of why a template value was refused.
type ParamProblem string

// The reasons a template value is refused.
const (
	ParamRequired    ParamProblem = "required"
	ParamInvalidText ParamProblem = "invalid-text" // not valid text, or longer than Max
	ParamControl     ParamProblem = "control"
	ParamBadLang     ParamProblem = "bad-lang"
	ParamBadRefresh  ParamProblem = "bad-refresh"
)

// ParamError is the RenderTemplate validation error: Field is the form field ("app_name",
// "message", "icon", "lang" or "refresh"), Problem a code and Max the limit for ParamInvalidText;
// Error is the English text. It wraps ErrInvalidTemplateParams.
type ParamError struct {
	Field   string
	Problem ParamProblem
	Max     int
}

func (e *ParamError) Unwrap() error { return ErrInvalidTemplateParams }

func (e *ParamError) Error() string {
	what := map[string]string{"app_name": "application name", "message": "message", "icon": "icon"}[e.Field]
	var detail string
	switch e.Problem {
	case ParamRequired:
		detail = fmt.Sprintf("the %s is required", what)
	case ParamInvalidText:
		detail = fmt.Sprintf("the %s must be valid text of at most %d characters", what, e.Max)
	case ParamControl:
		detail = fmt.Sprintf("the %s contains control characters", what)
	case ParamBadLang:
		detail = `language must be a tag such as "es" or "pt-BR"`
	default:
		detail = "refresh must be between 1 and 99999 seconds"
	}
	return ErrInvalidTemplateParams.Error() + ": " + detail
}

var langRe = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8}){0,3}$`)

// TemplateParams are the values a user fills in to create a page from the template (RF-15).
type TemplateParams struct {
	AppName string // required, shown as the application name
	Message string // required, the explanatory line
	Lang    string // BCP 47-like tag such as "es" or "pt-BR"; default "es"
	Icon    string // optional emoji or short text
	Refresh int    // seconds, 1-99999; 0 means DefaultRefresh
}

// DefaultParams are the values of the generic page backsoon-init seeds as default.html.
func DefaultParams(refresh int) TemplateParams {
	return TemplateParams{
		AppName: "Este servicio",
		Message: "Estamos realizando tareas de mantenimiento. Esta página volverá a mostrar el servicio en cuanto esté disponible.",
		Lang:    DefaultLang,
		Icon:    "🔧",
		Refresh: refresh,
	}
}

// RenderTemplate fills the placeholders {{APP_NAME}}, {{MESSAGE}}, {{REFRESH}}, {{LANG}} and
// {{ICON}} of tpl. Every user-provided value is HTML-escaped, and values are inserted in a single
// pass, so a value containing "{{LANG}}" is not expanded.
func RenderTemplate(tpl []byte, p TemplateParams) ([]byte, error) {
	p.AppName = strings.TrimSpace(p.AppName)
	p.Message = strings.TrimSpace(p.Message)
	p.Icon = strings.TrimSpace(p.Icon)
	p.Lang = strings.TrimSpace(p.Lang)
	if p.Lang == "" {
		p.Lang = DefaultLang
	}
	if p.Refresh == 0 {
		p.Refresh = DefaultRefresh
	}
	if err := checkText("app_name", p.AppName, MaxNameRunes, true, false); err != nil {
		return nil, err
	}
	if err := checkText("message", p.Message, MaxMessageRunes, true, true); err != nil {
		return nil, err
	}
	if err := checkText("icon", p.Icon, MaxIconRunes, false, false); err != nil {
		return nil, err
	}
	if !langRe.MatchString(p.Lang) {
		return nil, &ParamError{Field: "lang", Problem: ParamBadLang}
	}
	if p.Refresh < 1 || p.Refresh > 99999 {
		return nil, &ParamError{Field: "refresh", Problem: ParamBadRefresh}
	}
	r := strings.NewReplacer(
		"{{APP_NAME}}", html.EscapeString(p.AppName),
		"{{MESSAGE}}", html.EscapeString(p.Message),
		"{{REFRESH}}", strconv.Itoa(p.Refresh),
		"{{LANG}}", html.EscapeString(p.Lang),
		"{{ICON}}", html.EscapeString(p.Icon),
	)
	return []byte(r.Replace(string(tpl))), nil
}

func checkText(field, v string, maxRunes int, required, multiline bool) error {
	if v == "" {
		if required {
			return &ParamError{Field: field, Problem: ParamRequired}
		}
		return nil
	}
	if !utf8.ValidString(v) || utf8.RuneCountInString(v) > maxRunes {
		return &ParamError{Field: field, Problem: ParamInvalidText, Max: maxRunes}
	}
	for _, r := range v {
		if unicode.IsControl(r) && !(multiline && (r == '\n' || r == '\r' || r == '\t')) {
			return &ParamError{Field: field, Problem: ParamControl}
		}
	}
	return nil
}

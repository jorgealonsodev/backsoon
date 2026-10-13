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
	WarnScript            = "The page contains <script>; scripts will run for visitors"
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
)

// ValidateHTML rejects pages that are empty, not UTF-8 or over MaxHTMLBytes, and returns
// non-blocking warnings (RF-17): external resources (RF-11) and any <script>.
func ValidateHTML(data []byte) (warnings []string, err error) {
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
		warnings = append(warnings, fmt.Sprintf(WarnExternalResources, strings.Join(found, ", ")))
	}
	if scriptRe.Match(data) {
		warnings = append(warnings, WarnScript)
	}
	return warnings, nil
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
	if err := checkText("application name", p.AppName, MaxNameRunes, true, false); err != nil {
		return nil, err
	}
	if err := checkText("message", p.Message, MaxMessageRunes, true, true); err != nil {
		return nil, err
	}
	if err := checkText("icon", p.Icon, MaxIconRunes, false, false); err != nil {
		return nil, err
	}
	if !langRe.MatchString(p.Lang) {
		return nil, fmt.Errorf("%w: language must be a tag such as \"es\" or \"pt-BR\"", ErrInvalidTemplateParams)
	}
	if p.Refresh < 1 || p.Refresh > 99999 {
		return nil, fmt.Errorf("%w: refresh must be between 1 and 99999 seconds", ErrInvalidTemplateParams)
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

func checkText(what, v string, maxRunes int, required, multiline bool) error {
	if v == "" {
		if required {
			return fmt.Errorf("%w: the %s is required", ErrInvalidTemplateParams, what)
		}
		return nil
	}
	if !utf8.ValidString(v) || utf8.RuneCountInString(v) > maxRunes {
		return fmt.Errorf("%w: the %s must be valid text of at most %d characters", ErrInvalidTemplateParams, what, maxRunes)
	}
	for _, r := range v {
		if unicode.IsControl(r) && !(multiline && (r == '\n' || r == '\r' || r == '\t')) {
			return fmt.Errorf("%w: the %s contains control characters", ErrInvalidTemplateParams, what)
		}
	}
	return nil
}

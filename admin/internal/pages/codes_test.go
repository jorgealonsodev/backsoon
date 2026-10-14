package pages

import (
	"errors"
	"strings"
	"testing"
)

// The web layer translates validation problems from their code and parameters, so each
// problem must carry a stable code while its English text stays what it always was.
func TestTargetErrorsCarryCodes(t *testing.T) {
	for _, tc := range []struct {
		raw     string
		problem TargetProblem
		text    string
	}{
		{"", TargetEmpty, "invalid target: empty value"},
		{"a b", TargetBadDomain, `invalid target "a b": a domain may contain only letters, digits, dots and hyphens (max 250 characters, starting with a letter or digit)`},
		{"-x:80", TargetBadService, `invalid target "-x:80": the service name may contain only letters, digits, dots and hyphens (max 240 characters, starting with a letter or digit)`},
		{"app:abc", TargetBadPort, `invalid target "app:abc": the port must be a number between 1 and 65535`},
		{"app:99999", TargetPortRange, `invalid target "app:99999": the port must be between 1 and 65535`},
		{"app:0", TargetPortRange, `invalid target "app:0": the port must be between 1 and 65535`},
	} {
		_, err := ParseTarget(tc.raw)
		var te *TargetError
		if !errors.As(err, &te) || !errors.Is(err, ErrInvalidTarget) {
			t.Fatalf("ParseTarget(%q) = %v, want a *TargetError wrapping ErrInvalidTarget", tc.raw, err)
		}
		if te.Problem != tc.problem {
			t.Errorf("ParseTarget(%q) problem = %q, want %q", tc.raw, te.Problem, tc.problem)
		}
		if err.Error() != tc.text {
			t.Errorf("ParseTarget(%q) text = %q, want %q", tc.raw, err.Error(), tc.text)
		}
	}
}

func TestTemplateParamErrorsCarryCodes(t *testing.T) {
	tpl := []byte("{{APP_NAME}}{{MESSAGE}}{{REFRESH}}{{LANG}}{{ICON}}")
	ok := TemplateParams{AppName: "Acme", Message: "Hello", Lang: "es", Icon: "x"}
	for name, tc := range map[string]struct {
		mod     func(*TemplateParams)
		field   string
		problem ParamProblem
		max     int
		text    string
	}{
		"app required":  {func(p *TemplateParams) { p.AppName = "" }, "app_name", ParamRequired, 0, "invalid template values: the application name is required"},
		"app long":      {func(p *TemplateParams) { p.AppName = strings.Repeat("x", 101) }, "app_name", ParamInvalidText, MaxNameRunes, "invalid template values: the application name must be valid text of at most 100 characters"},
		"app control":   {func(p *TemplateParams) { p.AppName = "a\x01" }, "app_name", ParamControl, 0, "invalid template values: the application name contains control characters"},
		"message empty": {func(p *TemplateParams) { p.Message = "" }, "message", ParamRequired, 0, "invalid template values: the message is required"},
		"message long":  {func(p *TemplateParams) { p.Message = strings.Repeat("x", 501) }, "message", ParamInvalidText, MaxMessageRunes, "invalid template values: the message must be valid text of at most 500 characters"},
		"icon long":     {func(p *TemplateParams) { p.Icon = strings.Repeat("x", 17) }, "icon", ParamInvalidText, MaxIconRunes, "invalid template values: the icon must be valid text of at most 16 characters"},
		"icon control":  {func(p *TemplateParams) { p.Icon = "\x01" }, "icon", ParamControl, 0, "invalid template values: the icon contains control characters"},
		"bad lang":      {func(p *TemplateParams) { p.Lang = "not a tag" }, "lang", ParamBadLang, 0, `invalid template values: language must be a tag such as "es" or "pt-BR"`},
		"bad refresh":   {func(p *TemplateParams) { p.Refresh = 100000 }, "refresh", ParamBadRefresh, 0, "invalid template values: refresh must be between 1 and 99999 seconds"},
	} {
		p := ok
		tc.mod(&p)
		_, err := RenderTemplate(tpl, p)
		var pe *ParamError
		if !errors.As(err, &pe) || !errors.Is(err, ErrInvalidTemplateParams) {
			t.Fatalf("%s: err = %v, want a *ParamError wrapping ErrInvalidTemplateParams", name, err)
		}
		if pe.Field != tc.field || pe.Problem != tc.problem || pe.Max != tc.max {
			t.Errorf("%s: got %+v, want field %q problem %q max %d", name, pe, tc.field, tc.problem, tc.max)
		}
		if err.Error() != tc.text {
			t.Errorf("%s: text = %q, want %q", name, err.Error(), tc.text)
		}
	}
}

func TestNameErrorsCarryCodes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		problem NameProblem
		text    string
	}{
		{"  ", NameRequired, "invalid page name: the name is required"},
		{strings.Repeat("n", 101), NameInvalidText, "invalid page name: use valid text of at most 100 characters"},
		{"a\x01b", NameControl, "invalid page name: the name contains control characters"},
	} {
		_, err := cleanName(tc.name)
		var ne *NameError
		if !errors.As(err, &ne) || !errors.Is(err, ErrInvalidName) {
			t.Fatalf("cleanName(%q) = %v", tc.name, err)
		}
		if ne.Problem != tc.problem || (tc.problem == NameInvalidText && ne.Max != MaxNameRunes) {
			t.Errorf("cleanName(%q) = %+v", tc.name, ne)
		}
		if err.Error() != tc.text {
			t.Errorf("cleanName(%q) text = %q, want %q", tc.name, err.Error(), tc.text)
		}
	}
}

func TestInspectReturnsStructuredWarnings(t *testing.T) {
	ws, err := Inspect([]byte(`<link href="https://x/a.css"><script src="a.js"></script>`))
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 2 || ws[0].Code != WarningExternalResources || ws[1].Code != WarningScript {
		t.Fatalf("warnings = %+v", ws)
	}
	if got := strings.Join(ws[0].Detail, "|"); got != "<script src>|<link>" {
		t.Errorf("detail = %q", got)
	}
	plain, _ := ValidateHTML([]byte(`<link href="https://x/a.css"><script src="a.js"></script>`))
	if len(plain) != 2 || plain[0] != ws[0].String() || plain[1] != ws[1].String() {
		t.Errorf("ValidateHTML text %q differs from the structured warnings %q", plain, ws)
	}
	if _, err := Inspect(nil); !errors.Is(err, ErrEmptyHTML) {
		t.Errorf("Inspect(nil) err = %v", err)
	}
}

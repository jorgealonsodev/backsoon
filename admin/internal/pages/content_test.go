package pages

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestValidateHTMLRejects(t *testing.T) {
	if _, err := ValidateHTML([]byte("<p>hi</p>\xff")); !errors.Is(err, ErrNotUTF8) {
		t.Errorf("non UTF-8: %v", err)
	}
	if _, err := ValidateHTML(nil); !errors.Is(err, ErrEmptyHTML) {
		t.Errorf("empty: %v", err)
	}
	big := []byte(strings.Repeat("a", MaxHTMLBytes+1))
	if _, err := ValidateHTML(big); !errors.Is(err, ErrTooLarge) {
		t.Errorf("too large: %v", err)
	}
	if _, err := ValidateHTML(big[:MaxHTMLBytes]); err != nil {
		t.Errorf("exactly 512 KB must pass: %v", err)
	}
	if MaxHTMLBytes != 512*1024 {
		t.Error("limit must be 512*1024")
	}
}

func TestValidateHTMLWarnings(t *testing.T) {
	cases := []struct {
		name, html string
		warns      int
		contains   string
	}{
		{"clean", "<!doctype html><title>x</title><p>hi</p><style>a{color:red}</style>", 0, ""},
		{"link", `<LINK rel="stylesheet" href="https://cdn.example/x.css">`, 1, "external"},
		{"script src", `<script src="https://x/y.js"></script>`, 2, "external"},
		{"script src spaced", "<ScRiPt\n type=\"module\" SRC='a.js'></script>", 2, "external"},
		{"inline script", `<script>alert(1)</script>`, 1, "<script>"},
		{"import", `<style>@import url(foo.css);</style>`, 1, "external"},
		{"url http", `<div style="background:url(http://x/y.png)">`, 1, "external"},
		{"url https quoted", `<div style="background:URL( 'https://x/y.png' )">`, 1, "external"},
		{"data uri is fine", `<img src="data:image/png;base64,AAAA"><div style="background:url(data:image/png;base64,AA)">`, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, err := ValidateHTML([]byte(tc.html))
			if err != nil {
				t.Fatal(err)
			}
			if len(w) != tc.warns {
				t.Fatalf("got %d warnings %q, want %d", len(w), w, tc.warns)
			}
			if tc.contains != "" && !strings.Contains(strings.Join(w, "|"), tc.contains) {
				t.Fatalf("warnings %q do not mention %q", w, tc.contains)
			}
		})
	}
}

const templatePath = "../../../templates/maintenance.html"

func readTemplate(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRenderTemplate(t *testing.T) {
	out, err := RenderTemplate(readTemplate(t), TemplateParams{
		AppName: "My <App> & \"Co\"", Message: "<script>alert(1)</script> back soon", Lang: "pt-BR", Icon: "🔧", Refresh: 15,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, p := range []string{"{{APP_NAME}}", "{{MESSAGE}}", "{{REFRESH}}", "{{LANG}}", "{{ICON}}"} {
		if strings.Contains(s, p) {
			t.Errorf("placeholder %s left in output", p)
		}
	}
	if strings.Contains(s, "<script") || strings.Contains(s, "<App>") {
		t.Error("user values must be HTML-escaped")
	}
	for _, want := range []string{
		`<html lang="pt-BR">`, `content="15"`, "My &lt;App&gt; &amp; &#34;Co&#34;", "&lt;script&gt;alert(1)&lt;/script&gt; back soon", "🔧", "cada 15 segundos",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q", want)
		}
	}
	if _, err := ValidateHTML(out); err != nil {
		t.Errorf("rendered page must pass validation: %v", err)
	}
	if w, _ := ValidateHTML(out); len(w) != 0 {
		t.Errorf("rendered template must have no warnings (CA-14): %v", w)
	}
}

func TestRenderTemplateNoDoubleSubstitution(t *testing.T) {
	out, err := RenderTemplate(readTemplate(t), TemplateParams{AppName: "{{MESSAGE}}", Message: "{{LANG}}", Lang: "es"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "{{MESSAGE}}") || !strings.Contains(string(out), "{{LANG}}") {
		t.Error("substituted values must not be re-expanded")
	}
}

func TestRenderTemplateValidation(t *testing.T) {
	tpl := readTemplate(t)
	bad := map[string]TemplateParams{
		"no name":      {Message: "m"},
		"no message":   {AppName: "a"},
		"long name":    {AppName: strings.Repeat("a", MaxNameRunes+1), Message: "m"},
		"long message": {AppName: "a", Message: strings.Repeat("m", MaxMessageRunes+1)},
		"bad lang":     {AppName: "a", Message: "m", Lang: "es\"><script>"},
		"lang spaces":  {AppName: "a", Message: "m", Lang: "e s"},
		"long icon":    {AppName: "a", Message: "m", Icon: strings.Repeat("x", 40)},
		"refresh 0":    {AppName: "a", Message: "m", Refresh: -1},
		"refresh big":  {AppName: "a", Message: "m", Refresh: 100000},
		"control char": {AppName: "a\x00b", Message: "m"},
	}
	for name, p := range bad {
		if _, err := RenderTemplate(tpl, p); err == nil {
			t.Errorf("%s: expected error", name)
		} else if !errors.Is(err, ErrInvalidTemplateParams) {
			t.Errorf("%s: error must wrap ErrInvalidTemplateParams: %v", name, err)
		}
	}
	// Defaults: lang es, refresh 10.
	out, err := RenderTemplate(tpl, TemplateParams{AppName: "a", Message: "m"})
	if err != nil || !strings.Contains(string(out), `lang="es"`) || !strings.Contains(string(out), `content="10"`) {
		t.Errorf("defaults not applied: %v", err)
	}
}

func TestDefaultParamsMatchSeed(t *testing.T) {
	out, err := RenderTemplate(readTemplate(t), DefaultParams(10))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "Este servicio") || !strings.Contains(string(out), `lang="es"`) {
		t.Error("default page must be generic and Spanish like the seed")
	}
}

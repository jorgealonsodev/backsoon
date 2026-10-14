package web

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var verbRe = regexp.MustCompile(`%[-+# 0-9.]*[a-zA-Z]`)

// Both languages carry exactly the same keys, with the same number of format verbs.
func TestCatalogueIsComplete(t *testing.T) {
	en, es := catalog[langEN], catalog[langES]
	for key, msg := range en {
		other, ok := es[key]
		switch {
		case !ok:
			t.Errorf("key %q has no Spanish text", key)
		case strings.TrimSpace(other) == "" || strings.TrimSpace(msg) == "":
			t.Errorf("key %q is empty", key)
		case len(verbRe.FindAllString(msg, -1)) != len(verbRe.FindAllString(other, -1)):
			t.Errorf("key %q: format verbs differ: %q vs %q", key, msg, other)
		}
	}
	for key := range es {
		if _, ok := en[key]; !ok {
			t.Errorf("key %q has no English text", key)
		}
	}
}

// Every key the templates and the handlers ask for exists (the literal ones; dynamic keys are
// checked below and by the Spanish rendering tests, which fail on ⟦key⟧).
func TestCatalogueKeysUsedExist(t *testing.T) {
	templateKey := regexp.MustCompile(`\{\{t "([^"]+)"`)
	goKey := regexp.MustCompile(`\.T\("([^"]+)"`)
	for glob, re := range map[string]*regexp.Regexp{"templates/*.html": templateKey, "*.go": goKey} {
		files, err := filepath.Glob(glob)
		if err != nil || len(files) == 0 {
			t.Fatalf("glob %s: %v %v", glob, files, err)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range re.FindAllStringSubmatch(string(src), -1) {
				if strings.HasSuffix(m[1], ".") { // a prefix of a dynamic key
					continue
				}
				if _, ok := catalog[langEN][m[1]]; !ok {
					t.Errorf("%s uses unknown key %q", f, m[1])
				}
			}
		}
	}
	for key := range notices {
		if _, ok := catalog[langEN]["notice."+key]; !ok {
			t.Errorf("notice %q has no text", key)
		}
	}
	for _, field := range fieldOrder {
		if _, ok := catalog[langEN]["field."+field]; !ok {
			t.Errorf("field %q has no label", field)
		}
	}
	for _, field := range []string{"app_name", "message"} {
		for _, p := range []string{"required", "text", "control"} {
			if _, ok := catalog[langEN]["param."+field+"."+p]; !ok {
				t.Errorf("missing param.%s.%s", field, p)
			}
		}
	}
	for _, p := range []string{"text", "control"} {
		if _, ok := catalog[langEN]["param.icon."+p]; !ok {
			t.Errorf("missing param.icon.%s", p)
		}
	}
}

func TestMissingKeyIsVisible(t *testing.T) {
	if got := locale(langES).T("no.such.key"); got != "⟦no.such.key⟧" {
		t.Errorf("T = %q", got)
	}
}

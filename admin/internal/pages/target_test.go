package pages

import (
	"errors"
	"strings"
	"testing"
)

// CA-20: APP.Test is normalized, app:99999 and "a b" are rejected.
func TestParseTarget(t *testing.T) {
	ok := map[string]Target{
		"APP.Test":               {TargetHost, "app.test"},
		"  nueva.test ":          {TargetHost, "nueva.test"},
		"192.168.1.10":           {TargetHost, "192.168.1.10"},
		"app:8080":               {TargetService, "app:8080"},
		"App-1.lan:65535":        {TargetService, "app-1.lan:65535"},
		"192.168.1.10:8081":      {TargetService, "192.168.1.10:8081"},
		"app:1":                  {TargetService, "app:1"},
		"app:0080":               {TargetService, "app:80"},
		strings.Repeat("a", 250): {TargetHost, strings.Repeat("a", 250)},
	}
	for in, want := range ok {
		got, err := ParseTarget(in)
		if err != nil || got != want {
			t.Errorf("ParseTarget(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	bad := []string{
		"", "   ", "app:99999", "app:65536", "app:0", "app:", ":80", "a b", "app test:80",
		"-app.test", ".app", "app_x", "app/x", "app:8a", "app:80:90", "app:123456", "ünï.test",
		"a\x00b", "http://app.test", "app.test/", "*.test",
		strings.Repeat("a", 251), strings.Repeat("a", 241) + ":80",
	}
	for _, in := range bad {
		_, err := ParseTarget(in)
		if err == nil {
			t.Errorf("ParseTarget(%q) must fail", in)
			continue
		}
		if !errors.Is(err, ErrInvalidTarget) {
			t.Errorf("ParseTarget(%q) error must wrap ErrInvalidTarget: %v", in, err)
		}
	}
	_, err := ParseTarget("app:99999")
	if err == nil || !strings.Contains(err.Error(), "port") {
		t.Errorf("port error must be explicit: %v", err)
	}
}

func TestTargetFileName(t *testing.T) {
	if got := (Target{TargetHost, "app.test"}).FileName(); got != "host/app.test.html" {
		t.Error(got)
	}
	if got := (Target{TargetService, "app:8080"}).FileName(); got != "target/app_8080.html" {
		t.Error(got)
	}
}

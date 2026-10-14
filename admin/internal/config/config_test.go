package config

import (
	"net/netip"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func mustHash(t *testing.T, pw string) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return string(h)
}

var secret = strings.Repeat("s", 32)

func TestLoadWithHash(t *testing.T) {
	h := mustHash(t, "pw")
	cfg, err := Load(env(map[string]string{
		EnvUser: "admin", EnvPasswordHash: h, EnvSessionSecret: secret,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.User != "admin" || string(cfg.PasswordHash) != h || cfg.PlaintextPassword {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if cfg.Refresh != 10 || cfg.Listen != ":8080" || cfg.PagesDir != "/pages" ||
		cfg.MetaDir != "/meta" || cfg.TemplatePath != "/usr/share/backsoon/maintenance.html" {
		t.Fatalf("defaults wrong: %+v", cfg)
	}
}

func TestHashWinsOverPlaintext(t *testing.T) {
	h := mustHash(t, "from-hash")
	cfg, err := Load(env(map[string]string{
		EnvUser: "a", EnvPasswordHash: h, EnvPassword: "plain", EnvSessionSecret: secret,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PlaintextPassword {
		t.Fatal("plaintext must not be reported when the hash wins")
	}
	if bcrypt.CompareHashAndPassword(cfg.PasswordHash, []byte("from-hash")) != nil {
		t.Fatal("hash was not kept")
	}
}

func TestPlaintextIsHashedAndReported(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		EnvUser: "a", EnvPassword: "hunter2hunter2", EnvSessionSecret: secret,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.PlaintextPassword {
		t.Fatal("plaintext use must be reported")
	}
	if bcrypt.CompareHashAndPassword(cfg.PasswordHash, []byte("hunter2hunter2")) != nil {
		t.Fatal("generated hash does not match the plaintext")
	}
}

// CA-17: the panel must refuse to start without credentials or secret.
func TestLoadRejectsMissingRequired(t *testing.T) {
	h := mustHash(t, "pw")
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"no user", map[string]string{EnvPasswordHash: h, EnvSessionSecret: secret}, EnvUser},
		{"blank user", map[string]string{EnvUser: "  ", EnvPasswordHash: h, EnvSessionSecret: secret}, EnvUser},
		{"no password", map[string]string{EnvUser: "a", EnvSessionSecret: secret}, EnvPasswordHash},
		{"no secret", map[string]string{EnvUser: "a", EnvPasswordHash: h}, EnvSessionSecret},
		{"short secret", map[string]string{EnvUser: "a", EnvPasswordHash: h, EnvSessionSecret: "short-secret-value"}, EnvSessionSecret},
		{"bad hash", map[string]string{EnvUser: "a", EnvPasswordHash: "not-a-bcrypt-hash", EnvSessionSecret: secret}, EnvPasswordHash},
		{"password too long", map[string]string{EnvUser: "a", EnvPassword: strings.Repeat("x", 80), EnvSessionSecret: secret}, EnvPassword},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(env(tc.env))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not name %s", err, tc.want)
			}
			for _, v := range tc.env {
				if len(v) > 8 && strings.Contains(err.Error(), v) {
					t.Fatalf("error leaks a configured value: %q", err)
				}
			}
		})
	}
}

func TestLoadReportsEverythingMissing(t *testing.T) {
	_, err := Load(env(nil))
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, name := range []string{EnvUser, EnvPasswordHash, EnvPassword, EnvSessionSecret} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error does not mention %s: %v", name, err)
		}
	}
}

// Same rule as nginx/docker-entrypoint.d/05-backsoon-validate.sh.
func TestRefreshRule(t *testing.T) {
	h := mustHash(t, "pw")
	base := map[string]string{EnvUser: "a", EnvPasswordHash: h, EnvSessionSecret: secret}
	valid := map[string]int{"1": 1, "10": 10, "99999": 99999, "": 10}
	for in, want := range valid {
		e := map[string]string{EnvRefresh: in}
		for k, v := range base {
			e[k] = v
		}
		cfg, err := Load(env(e))
		if err != nil || cfg.Refresh != want {
			t.Errorf("refresh %q: got %d, %v; want %d", in, cfg.Refresh, err, want)
		}
	}
	for _, in := range []string{"0", "007", "100000", "-1", "1.5", "abc", "1 0", "+5"} {
		e := map[string]string{EnvRefresh: in}
		for k, v := range base {
			e[k] = v
		}
		if _, err := Load(env(e)); err == nil || !strings.Contains(err.Error(), EnvRefresh) {
			t.Errorf("refresh %q should be rejected naming %s, got %v", in, EnvRefresh, err)
		}
	}
}

func TestPathOverrides(t *testing.T) {
	h := mustHash(t, "pw")
	cfg, err := Load(env(map[string]string{
		EnvUser: "a", EnvPasswordHash: h, EnvSessionSecret: secret,
		EnvListen: ":9000", EnvPagesDir: "/p", EnvMetaDir: "/m", EnvTemplate: "/t.html",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":9000" || cfg.PagesDir != "/p" || cfg.MetaDir != "/m" || cfg.TemplatePath != "/t.html" {
		t.Fatalf("overrides ignored: %+v", cfg)
	}
}

func TestTrustedProxies(t *testing.T) {
	h := mustHash(t, "pw")
	base := func(v string) map[string]string {
		return map[string]string{EnvUser: "a", EnvPasswordHash: h, EnvSessionSecret: secret, EnvTrustedProxies: v}
	}
	cfg, err := Load(env(base("")))
	if err != nil || cfg.TrustedProxies != nil {
		t.Fatalf("unset: want nil (trust X-Real-IP from any peer), got %v %v", cfg.TrustedProxies, err)
	}
	cfg, err = Load(env(base(" 172.18.0.1/16, 10.0.0.5\t2001:DB8::/32,,::ffff:192.0.2.7\r\n")))
	if err != nil {
		t.Fatal(err)
	}
	want := []netip.Prefix{
		netip.MustParsePrefix("172.18.0.0/16"), netip.MustParsePrefix("10.0.0.5/32"),
		netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("192.0.2.7/32"),
	}
	if len(cfg.TrustedProxies) != len(want) {
		t.Fatalf("TrustedProxies = %v, want %v", cfg.TrustedProxies, want)
	}
	for i := range want {
		if cfg.TrustedProxies[i] != want[i] {
			t.Fatalf("TrustedProxies = %v, want %v", cfg.TrustedProxies, want)
		}
	}
	for _, bad := range []string{"nope", "10.0.0.0/33", "10.0.0.0/8, 300.1.1.1", "fe80::1%eth0", "172.18.0.0/", ","} {
		_, err := Load(env(base(bad)))
		if err == nil || !strings.Contains(err.Error(), EnvTrustedProxies) {
			t.Errorf("%q should be rejected naming %s, got %v", bad, EnvTrustedProxies, err)
		}
	}
}

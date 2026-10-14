// Package config loads and validates the panel settings from environment variables.
//
// Load never returns a value that would leave the panel reachable without a login: a missing
// user, password or session secret is an error, and errors never contain configured values.
package config

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/crypto/bcrypt"
)

// Environment variable names.
const (
	// EnvUser is the administrator user name (required).
	EnvUser = "BACKSOON_ADMIN_USER"
	// EnvPasswordHash is the bcrypt hash of the administrator password (recommended).
	EnvPasswordHash = "BACKSOON_ADMIN_PASSWORD_HASH"
	// EnvPassword is the plaintext password alternative; the hash wins when both are set.
	EnvPassword = "BACKSOON_ADMIN_PASSWORD"
	// EnvSessionSecret signs session cookies (required, at least MinSecretBytes bytes).
	EnvSessionSecret = "BACKSOON_SESSION_SECRET"
	// EnvRefresh is the maintenance refresh interval in seconds (default 10), shared with backsoon.
	EnvRefresh = "BACKSOON_REFRESH"
	// EnvListen is the listen address of the panel (default ":8080").
	EnvListen = "BACKSOON_ADMIN_LISTEN"
	// EnvPagesDir is the published pages directory read by backsoon (default "/pages").
	EnvPagesDir = "BACKSOON_PAGES_DIR"
	// EnvMetaDir holds sites.json and the original HTML of each page (default "/meta").
	EnvMetaDir = "BACKSOON_META_DIR"
	// EnvTemplate is the maintenance page template (default "/usr/share/backsoon/maintenance.html").
	EnvTemplate = "BACKSOON_TEMPLATE"
	// EnvTrustedProxies lists the IP addresses or CIDR ranges allowed to set X-Real-IP, the client
	// address of the login rate limit (optional; unset trusts X-Real-IP from any peer).
	EnvTrustedProxies = "BACKSOON_TRUSTED_PROXIES"
)

// MinSecretBytes is the minimum length of the session secret.
const MinSecretBytes = 32

// Defaults for the optional settings.
const (
	DefaultRefresh      = 10
	DefaultListen       = ":8080"
	DefaultPagesDir     = "/pages"
	DefaultMetaDir      = "/meta"
	DefaultTemplatePath = "/usr/share/backsoon/maintenance.html"
)

// Config is the validated panel configuration.
type Config struct {
	// User is the single administrator's user name.
	User string
	// PasswordHash is a valid bcrypt hash of the administrator password. When the password came
	// from EnvPassword it was generated at load time; the plaintext is not retained.
	PasswordHash []byte
	// PlaintextPassword reports that the password was configured in plaintext (EnvPassword, no hash
	// present), so main can log a warning at startup.
	PlaintextPassword bool
	// PasswordFingerprint identifies the configured owner password without revealing it: a keyed
	// digest of the plaintext password or of the hash, whichever was configured. It is stable
	// across restarts (a plaintext password is re-hashed with a new salt at every start, so the
	// hash is not) and changes when the password is changed. The owner's sessions are tied to it.
	PasswordFingerprint []byte
	// SessionSecret is the key used to sign sessions.
	SessionSecret []byte
	// Refresh is the maintenance refresh interval in seconds (1-99999).
	Refresh int
	// Listen, PagesDir, MetaDir and TemplatePath are the addresses and paths described by the
	// EnvListen, EnvPagesDir, EnvMetaDir and EnvTemplate variables.
	Listen       string
	PagesDir     string
	MetaDir      string
	TemplatePath string
	// TrustedProxies are the peers whose X-Real-IP header the login rate limit believes. Nil means
	// any peer (EnvTrustedProxies unset); a peer outside a non-nil list is keyed on its own address.
	TrustedProxies []netip.Prefix
}

// Load reads the configuration through getenv (usually os.Getenv). All problems found are
// reported together, each naming the offending variable.
func Load(getenv func(string) string) (Config, error) {
	var errs []error
	cfg := Config{
		Listen:       withDefault(getenv(EnvListen), DefaultListen),
		PagesDir:     withDefault(getenv(EnvPagesDir), DefaultPagesDir),
		MetaDir:      withDefault(getenv(EnvMetaDir), DefaultMetaDir),
		TemplatePath: withDefault(getenv(EnvTemplate), DefaultTemplatePath),
	}

	cfg.User = strings.TrimSpace(getenv(EnvUser))
	if cfg.User == "" {
		errs = append(errs, fmt.Errorf("%s is required", EnvUser))
	}

	secret := getenv(EnvSessionSecret)
	switch {
	case secret == "":
		errs = append(errs, fmt.Errorf("%s is required (at least %d bytes of random data)", EnvSessionSecret, MinSecretBytes))
	case len(secret) < MinSecretBytes:
		errs = append(errs, fmt.Errorf("%s is too short: it must be at least %d bytes", EnvSessionSecret, MinSecretBytes))
	default:
		cfg.SessionSecret = []byte(secret)
	}

	if err := loadPassword(&cfg, getenv); err != nil {
		errs = append(errs, err)
	}

	refresh, err := ParseRefresh(getenv(EnvRefresh))
	if err != nil {
		errs = append(errs, err)
	}
	cfg.Refresh = refresh

	trusted, err := ParseTrustedProxies(getenv(EnvTrustedProxies))
	if err != nil {
		errs = append(errs, err)
	}
	cfg.TrustedProxies = trusted

	if len(errs) > 0 {
		return Config{}, errors.Join(errs...)
	}
	return cfg, nil
}

func loadPassword(cfg *Config, getenv func(string) string) error {
	if hash := strings.TrimSpace(getenv(EnvPasswordHash)); hash != "" {
		if _, err := bcrypt.Cost([]byte(hash)); err != nil {
			return fmt.Errorf("%s is not a valid bcrypt hash", EnvPasswordHash)
		}
		cfg.PasswordHash = []byte(hash)
		cfg.PasswordFingerprint = fingerprint(cfg.SessionSecret, "hash", hash)
		return nil
	}
	password := getenv(EnvPassword)
	if password == "" {
		return fmt.Errorf("%s or %s is required", EnvPasswordHash, EnvPassword)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		// bcrypt rejects passwords over 72 bytes; the error text never includes the password.
		return fmt.Errorf("%s cannot be hashed (bcrypt accepts at most 72 bytes): use %s instead", EnvPassword, EnvPasswordHash)
	}
	cfg.PasswordHash = hash
	cfg.PlaintextPassword = true
	cfg.PasswordFingerprint = fingerprint(cfg.SessionSecret, "plaintext", password)
	return nil
}

// fingerprint is HMAC-SHA256 keyed with the session secret over the kind of credential and its
// value, so the plaintext and hash forms never collide.
func fingerprint(secret []byte, kind, value string) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("owner-password"))
	mac.Write([]byte{0})
	mac.Write([]byte(kind))
	mac.Write([]byte{0})
	mac.Write([]byte(value))
	return mac.Sum(nil)
}

// ParseRefresh applies the BACKSOON_REFRESH rule shared with
// nginx/docker-entrypoint.d/05-backsoon-validate.sh: a plain integer from 1 to 99999 with no
// sign and no leading zero. An empty value yields DefaultRefresh.
func ParseRefresh(value string) (int, error) {
	if value == "" {
		return DefaultRefresh, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || len(value) > 5 || value[0] < '1' || value[0] > '9' ||
		strings.Trim(value, "0123456789") != "" || n < 1 || n > 99999 {
		return DefaultRefresh, fmt.Errorf("%s must be an integer between 1 and 99999", EnvRefresh)
	}
	return n, nil
}

// ParseTrustedProxies reads a list of IP addresses and CIDR ranges separated by commas or white
// space.
// A single address becomes a one-address range; host bits of a range are cleared. A blank value
// yields nil. Errors name the position of the bad entry, not its text.
func ParseTrustedProxies(value string) ([]netip.Prefix, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	entries := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
	if len(entries) == 0 {
		return nil, fmt.Errorf("%s must list IP addresses or CIDR ranges, for example 172.18.0.0/16", EnvTrustedProxies)
	}
	prefixes := make([]netip.Prefix, 0, len(entries))
	for i, entry := range entries {
		prefix, ok := parseTrustedEntry(entry)
		if !ok {
			return nil, fmt.Errorf("%s: entry %d is not an IP address or CIDR range (for example 172.18.0.0/16)", EnvTrustedProxies, i+1)
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes, nil
}

func parseTrustedEntry(entry string) (netip.Prefix, bool) {
	if strings.Contains(entry, "/") {
		prefix, err := netip.ParsePrefix(entry)
		if err != nil {
			return netip.Prefix{}, false
		}
		if prefix.Addr().Is4In6() && prefix.Bits() >= 96 {
			prefix = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96)
		}
		return prefix.Masked(), true
	}
	addr, err := netip.ParseAddr(entry)
	if err != nil || addr.Zone() != "" {
		return netip.Prefix{}, false
	}
	addr = addr.Unmap()
	return netip.PrefixFrom(addr, addr.BitLen()), true
}

func withDefault(value, def string) string {
	if value == "" {
		return def
	}
	return value
}

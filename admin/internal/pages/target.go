// Package pages is the panel's page model: sites with their targets, content validation, template
// rendering and the persistent Store backed by <meta>/sites.json.
package pages

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// TargetType says how backsoon matches a request to a page.
type TargetType string

const (
	// TargetHost matches the request host (file host/<domain>.html).
	TargetHost TargetType = "host"
	// TargetService matches the destination NPM forwards, service:port (file target/<service>_<port>.html).
	TargetService TargetType = "target"
)

// Target is a domain or a service:port a page is published for.
type Target struct {
	Type  TargetType `json:"type"`
	Value string     `json:"value"`
}

// ErrInvalidTarget is wrapped by every ParseTarget failure.
var ErrInvalidTarget = errors.New("invalid target")

// These mirror the map blocks of nginx/default.conf.template, so any target the panel accepts is
// one nginx can actually serve.
var (
	hostRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,249}$`)
	serviceRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,239}$`)
	portRe    = regexp.MustCompile(`^[0-9]{1,5}$`)
)

// ParseTarget trims and lowercases raw, and parses it as a domain ("app.test") or a service:port
// ("app:8080", port 1-65535; leading zeros of the port are dropped).
func ParseTarget(raw string) (Target, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return Target{}, fmt.Errorf("%w: empty value", ErrInvalidTarget)
	}
	name, port, hasPort := strings.Cut(v, ":")
	if !hasPort {
		if !hostRe.MatchString(v) {
			return Target{}, fmt.Errorf("%w %q: a domain may contain only letters, digits, dots and hyphens (max 250 characters, starting with a letter or digit)", ErrInvalidTarget, raw)
		}
		return Target{TargetHost, v}, nil
	}
	if !serviceRe.MatchString(name) {
		return Target{}, fmt.Errorf("%w %q: the service name may contain only letters, digits, dots and hyphens (max 240 characters, starting with a letter or digit)", ErrInvalidTarget, raw)
	}
	if !portRe.MatchString(port) {
		return Target{}, fmt.Errorf("%w %q: the port must be a number between 1 and 65535", ErrInvalidTarget, raw)
	}
	n, _ := strconv.Atoi(port)
	if n < 1 || n > 65535 {
		return Target{}, fmt.Errorf("%w %q: the port must be between 1 and 65535", ErrInvalidTarget, raw)
	}
	return Target{TargetService, name + ":" + strconv.Itoa(n)}, nil
}

// FileName is the path, relative to the pages directory, backsoon reads for this target.
func (t Target) FileName() string {
	if t.Type == TargetService {
		return "target/" + strings.Replace(t.Value, ":", "_", 1) + ".html"
	}
	return "host/" + t.Value + ".html"
}

// String returns the value as the user typed it in the panel.
func (t Target) String() string { return t.Value }

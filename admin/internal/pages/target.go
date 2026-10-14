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

// TargetProblem is the stable code of why ParseTarget refused a value.
type TargetProblem string

// The reasons a target is refused.
const (
	TargetEmpty      TargetProblem = "empty"
	TargetBadDomain  TargetProblem = "bad-domain"
	TargetBadService TargetProblem = "bad-service"
	TargetBadPort    TargetProblem = "bad-port"
	TargetPortRange  TargetProblem = "port-range"
)

// TargetError is the error ParseTarget returns: a code for the web layer to translate, and the
// English text in Error. It wraps ErrInvalidTarget.
type TargetError struct {
	Problem TargetProblem
	Raw     string
}

func (e *TargetError) Unwrap() error { return ErrInvalidTarget }

func (e *TargetError) Error() string {
	switch e.Problem {
	case TargetEmpty:
		return ErrInvalidTarget.Error() + ": empty value"
	case TargetBadDomain:
		return fmt.Sprintf("%v %q: a domain may contain only letters, digits, dots and hyphens (max 250 characters, starting with a letter or digit)", ErrInvalidTarget, e.Raw)
	case TargetBadService:
		return fmt.Sprintf("%v %q: the service name may contain only letters, digits, dots and hyphens (max 240 characters, starting with a letter or digit)", ErrInvalidTarget, e.Raw)
	case TargetBadPort:
		return fmt.Sprintf("%v %q: the port must be a number between 1 and 65535", ErrInvalidTarget, e.Raw)
	}
	return fmt.Sprintf("%v %q: the port must be between 1 and 65535", ErrInvalidTarget, e.Raw)
}

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
		return Target{}, &TargetError{Problem: TargetEmpty, Raw: raw}
	}
	name, port, hasPort := strings.Cut(v, ":")
	if !hasPort {
		if !hostRe.MatchString(v) {
			return Target{}, &TargetError{Problem: TargetBadDomain, Raw: raw}
		}
		return Target{TargetHost, v}, nil
	}
	if !serviceRe.MatchString(name) {
		return Target{}, &TargetError{Problem: TargetBadService, Raw: raw}
	}
	if !portRe.MatchString(port) {
		return Target{}, &TargetError{Problem: TargetBadPort, Raw: raw}
	}
	n, _ := strconv.Atoi(port)
	if n < 1 || n > 65535 {
		return Target{}, &TargetError{Problem: TargetPortRange, Raw: raw}
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

package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

const secret = "0123456789abcdef0123456789abcdef-secret"

func validEnv(t *testing.T) map[string]string {
	dir := t.TempDir()
	return map[string]string{
		"BACKSOON_ADMIN_USER":     "admin",
		"BACKSOON_ADMIN_PASSWORD": "plaintext-pass-1",
		"BACKSOON_SESSION_SECRET": secret,
		"BACKSOON_ADMIN_LISTEN":   "127.0.0.1:0",
		"BACKSOON_PAGES_DIR":      filepath.Join(dir, "pages"),
		"BACKSOON_META_DIR":       filepath.Join(dir, "meta"),
		"BACKSOON_TEMPLATE":       "../../../templates/maintenance.html",
	}
}

// CA-17 (second half): no credentials, no start.
func TestRunRefusesToStartWithoutValidConfiguration(t *testing.T) {
	cases := map[string]struct {
		unset, set string
		value      string
		want       string
	}{
		"no user":         {unset: "BACKSOON_ADMIN_USER", want: "BACKSOON_ADMIN_USER"},
		"no password":     {unset: "BACKSOON_ADMIN_PASSWORD", want: "BACKSOON_ADMIN_PASSWORD_HASH or BACKSOON_ADMIN_PASSWORD"},
		"no secret":       {unset: "BACKSOON_SESSION_SECRET", want: "BACKSOON_SESSION_SECRET"},
		"short secret":    {set: "BACKSOON_SESSION_SECRET", value: "short", want: "too short"},
		"invalid refresh": {set: "BACKSOON_REFRESH", value: "0", want: "BACKSOON_REFRESH"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			m := validEnv(t)
			if c.unset != "" {
				delete(m, c.unset)
			}
			if c.set != "" {
				m[c.set] = c.value
			}
			var stdout, stderr syncBuffer
			code := run(context.Background(), nil, env(m), &stdout, &stderr)
			if code == 0 {
				t.Fatal("exit code must not be 0")
			}
			if !strings.Contains(stderr.String(), c.want) {
				t.Fatalf("stderr = %q, want it to mention %q", stderr.String(), c.want)
			}
			if strings.Contains(stdout.String(), "listening") {
				t.Fatal("the panel must not start")
			}
			if strings.Contains(stderr.String(), "plaintext-pass-1") || strings.Contains(stderr.String(), secret) {
				t.Fatal("errors must not echo configured values")
			}
		})
	}
}

var listeningRe = regexp.MustCompile(`listening on (127\.0\.0\.1:\d+)`)

func TestRunServesAndShutsDownGracefully(t *testing.T) {
	m := validEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stdout, stderr syncBuffer
	done := make(chan int, 1)
	go func() { done <- run(ctx, nil, env(m), &stdout, &stderr) }()

	var addr string
	deadline := time.Now().Add(10 * time.Second)
	for addr == "" && time.Now().Before(deadline) {
		if m := listeningRe.FindStringSubmatch(stdout.String()); m != nil {
			addr = m[1]
		}
		time.Sleep(20 * time.Millisecond)
	}
	if addr == "" {
		t.Fatalf("the panel did not start: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	base := "http://" + addr

	resp, err := http.Get(base + "/healthz")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("healthz: %v %v", resp, err)
	}
	resp.Body.Close()

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequest("GET", base+"/", nil)
	req.Header.Set("Accept", "text/html")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	resp, err = client.Do(req)
	if err != nil || resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/login" {
		t.Fatalf("GET / without a session: %v %v", resp, err)
	}
	resp.Body.Close()

	logs := stdout.String()
	if !strings.Contains(logs, "BACKSOON_ADMIN_PASSWORD") || !strings.Contains(strings.ToLower(logs), "warning") {
		t.Errorf("a plaintext password must log a warning at start: %q", logs)
	}
	if strings.Contains(logs, "plaintext-pass-1") {
		t.Error("the warning must not contain the password")
	}
	if !strings.Contains(logs, "X-Real-IP trusted from any peer") {
		t.Errorf("without BACKSOON_TRUSTED_PROXIES the start log must say X-Real-IP is trusted from any peer: %q", logs)
	}

	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit code = %d, stderr=%q", code, stderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the panel did not shut down")
	}
}

func TestRunDoesNotWarnWithABcryptHash(t *testing.T) {
	m := validEnv(t)
	delete(m, "BACKSOON_ADMIN_PASSWORD")
	m["BACKSOON_ADMIN_PASSWORD_HASH"] = "$2a$04$BLUvv.M8CYSXPOUhNnGnk.bCm8tcMWIKaAvjt6LvGvmhbIAS5BgfK" // not a real credential
	ctx, cancel := context.WithCancel(context.Background())
	var stdout, stderr syncBuffer
	done := make(chan int, 1)
	go func() { done <- run(ctx, nil, env(m), &stdout, &stderr) }()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(stdout.String(), "listening") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	if strings.Contains(strings.ToLower(stdout.String()), "warning") {
		t.Errorf("unexpected warning: %q", stdout.String())
	}
}

func TestRunLogsTheTrustedProxies(t *testing.T) {
	m := validEnv(t)
	m["BACKSOON_TRUSTED_PROXIES"] = "172.18.0.0/16, 10.0.0.5"
	ctx, cancel := context.WithCancel(context.Background())
	var stdout, stderr syncBuffer
	done := make(chan int, 1)
	go func() { done <- run(ctx, nil, env(m), &stdout, &stderr) }()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(stdout.String(), "listening") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	if !strings.Contains(stdout.String(), "X-Real-IP trusted from 172.18.0.0/16, 10.0.0.5/32") {
		t.Errorf("the start log must name the trusted proxies: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

// The first line of every start reminds that NPM must reload to apply the fallback.
func TestRunFirstLogLineRemindsToReloadNPM(t *testing.T) {
	m := validEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	var stdout, stderr syncBuffer
	done := make(chan int, 1)
	go func() { done <- run(ctx, nil, env(m), &stdout, &stderr) }()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(stdout.String(), "listening") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	first, _, _ := strings.Cut(stdout.String(), "\n")
	if !strings.Contains(first, "reminder: after installing or updating backsoon, reload Nginx Proxy Manager so it applies server_proxy.conf") {
		t.Errorf("the first log line must remind to reload NPM: %q", first)
	}
}

func TestRunRejectsInvalidTrustedProxies(t *testing.T) {
	m := validEnv(t)
	m["BACKSOON_TRUSTED_PROXIES"] = "172.18.0.0/16, not-a-range"
	var stdout, stderr syncBuffer
	if code := run(context.Background(), nil, env(m), &stdout, &stderr); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "BACKSOON_TRUSTED_PROXIES") {
		t.Errorf("stderr must name the variable: %q", stderr.String())
	}
}

func TestRunReportsAnUnusableListenAddress(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	m := validEnv(t)
	m["BACKSOON_ADMIN_LISTEN"] = ln.Addr().String()
	var stdout, stderr syncBuffer
	if code := run(context.Background(), nil, env(m), &stdout, &stderr); code == 0 {
		t.Fatal("exit code must not be 0")
	}
	if stderr.String() == "" {
		t.Fatal("the failure must be explained on stderr")
	}
}

func healthEnv(srv *httptest.Server) func(string) string {
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	return env(map[string]string{"BACKSOON_ADMIN_LISTEN": ":" + port}) // no credentials on purpose
}

func TestHealthcheckSucceedsOnlyOn200(t *testing.T) {
	status := http.StatusOK
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.WriteHeader(status)
	}))
	defer srv.Close()

	var stdout, stderr syncBuffer
	if code := run(context.Background(), []string{"-healthcheck"}, healthEnv(srv), &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, stderr=%q", code, stderr.String())
	}
	if path != "/healthz" {
		t.Fatalf("requested %q", path)
	}
	for _, s := range []int{http.StatusServiceUnavailable, http.StatusSeeOther, http.StatusUnauthorized} {
		status = s
		if code := run(context.Background(), []string{"-healthcheck"}, healthEnv(srv), io.Discard, io.Discard); code == 0 {
			t.Errorf("status %d must fail the healthcheck", s)
		}
	}
}

func TestHealthcheckFailsWhenNothingListensAndWhenSlow(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	closed := env(map[string]string{"BACKSOON_ADMIN_LISTEN": ":" + strconv.Itoa(port)})
	if code := run(context.Background(), []string{"-healthcheck"}, closed, io.Discard, io.Discard); code == 0 {
		t.Fatal("nothing listens: must fail")
	}

	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	defer slow.Close()
	defer close(release)
	start := time.Now()
	if code := run(context.Background(), []string{"-healthcheck"}, healthEnv(slow), io.Discard, io.Discard); code == 0 {
		t.Fatal("a hanging server must fail")
	}
	if d := time.Since(start); d > 4*time.Second {
		t.Fatalf("healthcheck took %s, the timeout is 2 s", d)
	}
}

func TestHealthcheckUsesTheDefaultPort(t *testing.T) {
	if got := healthURL(env(nil)); got != "http://127.0.0.1:8080/healthz" {
		t.Fatalf("url = %q", got)
	}
	if got := healthURL(env(map[string]string{"BACKSOON_ADMIN_LISTEN": "0.0.0.0:9000"})); got != "http://127.0.0.1:9000/healthz" {
		t.Fatalf("url = %q", got)
	}
}

// S2: like sites.json, a corrupt users.json stops the panel with a message instead of starting
// with users it cannot trust.
func TestRunRefusesToStartWithACorruptUserFile(t *testing.T) {
	m := validEnv(t)
	if err := os.MkdirAll(m["BACKSOON_META_DIR"], 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m["BACKSOON_META_DIR"], "users.json"), []byte(`{"version":1,"users":[{"name":"Al ice"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr syncBuffer
	if code := run(context.Background(), nil, env(m), &stdout, &stderr); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "users.json") {
		t.Fatalf("stderr = %q, want it to name users.json", stderr.String())
	}
	if strings.Contains(stdout.String(), "listening") {
		t.Fatal("the panel must not start")
	}
}

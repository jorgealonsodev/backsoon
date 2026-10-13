package auth

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *fakeClock { return &fakeClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)} }
func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

var testSecret = []byte(strings.Repeat("k", 32))

func TestCredentials(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("correct horse"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	c := NewCredentials("admin", hash)
	cases := []struct {
		user, pass string
		want       bool
	}{
		{"admin", "correct horse", true},
		{"admin", "wrong", false},
		{"Admin", "correct horse", false},
		{"", "correct horse", false},
		{"admin", "", false},
		{"admin2", "correct horse", false},
	}
	for _, tc := range cases {
		if got := c.Check(tc.user, tc.pass); got != tc.want {
			t.Errorf("Check(%q,%q) = %v, want %v", tc.user, tc.pass, got, tc.want)
		}
	}
}

func TestSessionRoundTrip(t *testing.T) {
	clk := newClock()
	m := NewManager(testSecret, clk.Now)
	s, cookie, err := m.Issue()
	if err != nil {
		t.Fatal(err)
	}
	if cookie.Name != SessionCookieName || !cookie.HttpOnly || !cookie.Secure ||
		cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" {
		t.Fatalf("cookie attributes wrong: %+v", cookie)
	}
	if cookie.MaxAge != int((12*time.Hour).Seconds()) || !cookie.Expires.Equal(clk.Now().Add(12*time.Hour)) {
		t.Fatalf("cookie lifetime wrong: maxage=%d expires=%v", cookie.MaxAge, cookie.Expires)
	}
	got, err := m.Verify(cookie.Value)
	if err != nil || got.ID != s.ID || !got.Expires.Equal(s.Expires) {
		t.Fatalf("verify failed: %+v %v", got, err)
	}

	r, _ := http.NewRequest("GET", "/", nil)
	r.AddCookie(cookie)
	if got, err := m.FromRequest(r); err != nil || got.ID != s.ID {
		t.Fatalf("FromRequest: %+v %v", got, err)
	}
	if _, err := m.FromRequest(mustReq(t)); err == nil {
		t.Fatal("request without cookie must fail")
	}
}

func mustReq(t *testing.T) *http.Request {
	t.Helper()
	r, err := http.NewRequest("GET", "/", nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSessionRejectsTamperedExpiredMalformed(t *testing.T) {
	clk := newClock()
	m := NewManager(testSecret, clk.Now)
	_, cookie, _ := m.Issue()
	v := cookie.Value
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		t.Fatalf("unexpected value shape %q", v)
	}
	// Extend the expiry without re-signing.
	forgedExp := parts[0] + ".99999999999." + parts[2]
	// Flip a signature character.
	sig := []byte(parts[2])
	if sig[0] == 'A' {
		sig[0] = 'B'
	} else {
		sig[0] = 'A'
	}
	badSig := parts[0] + "." + parts[1] + "." + string(sig)
	other := NewManager([]byte(strings.Repeat("z", 32)), clk.Now)

	for name, val := range map[string]string{
		"forged expiry": forgedExp, "bad signature": badSig, "empty": "", "garbage": "abc",
		"two parts": parts[0] + "." + parts[1], "four parts": v + ".x", "non numeric exp": parts[0] + ".x." + parts[2],
	} {
		if _, err := m.Verify(val); err == nil {
			t.Errorf("%s: expected rejection", name)
		}
	}
	if _, err := other.Verify(v); err == nil {
		t.Error("a different secret must not verify the cookie")
	}

	clk.Advance(12*time.Hour - time.Second)
	if _, err := m.Verify(v); err != nil {
		t.Errorf("still valid just before expiry: %v", err)
	}
	clk.Advance(time.Second)
	if _, err := m.Verify(v); err == nil {
		t.Error("expired session must be rejected at exactly 12h")
	}
}

func TestSessionsAreUnique(t *testing.T) {
	m := NewManager(testSecret, nil)
	a, _, _ := m.Issue()
	b, _, _ := m.Issue()
	if a.ID == b.ID {
		t.Fatal("session ids must be random")
	}
}

func TestExpiredCookie(t *testing.T) {
	m := NewManager(testSecret, nil)
	c := m.ExpiredCookie()
	if c.Name != SessionCookieName || c.Value != "" || c.MaxAge >= 0 || !c.HttpOnly || !c.Secure ||
		c.SameSite != http.SameSiteStrictMode || c.Path != "/" || !c.Expires.Before(time.Now()) {
		t.Fatalf("logout cookie wrong: %+v", c)
	}
}

func TestCSRF(t *testing.T) {
	m := NewManager(testSecret, nil)
	a, _, _ := m.Issue()
	b, _, _ := m.Issue()
	tok := m.CSRFToken(a)
	if tok == "" || tok != m.CSRFToken(a) {
		t.Fatal("token must be stable per session")
	}
	if tok == m.CSRFToken(b) {
		t.Fatal("token must differ per session")
	}
	if !m.VerifyCSRF(a, tok) {
		t.Fatal("valid token rejected")
	}
	for _, bad := range []string{"", "x", tok + "x", m.CSRFToken(b)} {
		if m.VerifyCSRF(a, bad) {
			t.Errorf("token %q must be rejected", bad)
		}
	}
	if tok == strings.Split(mustCookieValue(t, m, a), ".")[2] {
		t.Fatal("csrf token must not equal the session signature")
	}
}

func mustCookieValue(t *testing.T, m *Manager, s Session) string {
	t.Helper()
	return m.Cookie(s).Value
}

// CA-24: 6 failed logins in a row; the sixth is rejected even with the right password.
func TestLimiterSixthAttemptRejected(t *testing.T) {
	clk := newClock()
	l := NewLimiter(5, 15*time.Minute, clk.Now)
	for i := 1; i <= 5; i++ {
		if ok, _ := l.Attempt("1.2.3.4"); !ok {
			t.Fatalf("attempt %d must be allowed", i)
		}
		clk.Advance(time.Second)
	}
	ok, retry := l.Attempt("1.2.3.4")
	if ok {
		t.Fatal("sixth attempt must be rejected")
	}
	if retry <= 0 || retry > 15*time.Minute {
		t.Fatalf("retry-after out of range: %v", retry)
	}
	// Rejected attempts do not extend the block.
	clk.Advance(14*time.Minute + 54*time.Second)
	if ok, _ := l.Attempt("1.2.3.4"); ok {
		t.Fatal("still blocked 1s before the first failure leaves the window")
	}
	clk.Advance(time.Second)
	if ok, _ := l.Attempt("1.2.3.4"); !ok {
		t.Fatal("must be allowed once 15 minutes passed since the first failure")
	}
}

func TestLimiterBlocksUntilWindowPasses(t *testing.T) {
	clk := newClock()
	l := NewLimiter(5, 15*time.Minute, clk.Now)
	for range 5 {
		l.Attempt("ip")
	}
	clk.Advance(15*time.Minute - time.Nanosecond)
	if ok, _ := l.Attempt("ip"); ok {
		t.Fatal("still inside the window")
	}
	clk.Advance(time.Nanosecond)
	if ok, _ := l.Attempt("ip"); !ok {
		t.Fatal("window has passed")
	}
}

func TestLimiterIsPerIP(t *testing.T) {
	l := NewLimiter(5, 15*time.Minute, newClock().Now)
	for range 5 {
		l.Attempt("a")
	}
	if ok, _ := l.Attempt("a"); ok {
		t.Fatal("a must be blocked")
	}
	if ok, _ := l.Attempt("b"); !ok {
		t.Fatal("b must be unaffected")
	}
}

// A success clears the counter, but can never admit a sixth attempt: an attempt is
// only admitted while fewer than five failures are on record.
func TestLimiterSuccessResetsButNeverAdmitsSixth(t *testing.T) {
	clk := newClock()
	l := NewLimiter(5, 15*time.Minute, clk.Now)
	for range 4 {
		l.Attempt("ip")
	}
	if ok, _ := l.Attempt("ip"); !ok { // 5th attempt, correct password
		t.Fatal("fifth attempt must be allowed")
	}
	l.Success("ip")
	for i := 1; i <= 5; i++ {
		if ok, _ := l.Attempt("ip"); !ok {
			t.Fatalf("after success attempt %d must be allowed", i)
		}
	}
	if ok, _ := l.Attempt("ip"); ok {
		t.Fatal("sixth failure after reset must be rejected")
	}
}

func TestLimiterConcurrentAttemptsNeverExceedLimit(t *testing.T) {
	l := NewLimiter(5, 15*time.Minute, newClock().Now)
	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _ := l.Attempt("ip"); ok {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if allowed != 5 {
		t.Fatalf("allowed %d concurrent attempts, want 5", allowed)
	}
}

func TestLimiterBoundedMemory(t *testing.T) {
	clk := newClock()
	l := NewLimiter(5, 15*time.Minute, clk.Now)
	for i := range 3000 {
		l.Attempt("ip-" + strconv.Itoa(i))
	}
	clk.Advance(16 * time.Minute)
	l.Attempt("fresh")
	if n := l.size(); n > 1 {
		t.Fatalf("expired entries must be pruned, have %d", n)
	}
	for i := range 3 * maxTracked {
		l.Attempt("flood-" + strconv.Itoa(i))
	}
	if n := l.size(); n > maxTracked {
		t.Fatalf("limiter grew past its cap: %d", n)
	}
}

package auth

import (
	"encoding/base64"
	"errors"
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

// fakeDir is a Directory with settable users: name -> stamp. "root" is the owner.
type fakeDir struct {
	mu     sync.Mutex
	stamps map[string]string
}

func newDir() *fakeDir {
	return &fakeDir{stamps: map[string]string{"root": "s-root", "alice": "s-alice-1", "bob": "s-bob-1"}}
}

func (d *fakeDir) Resolve(user string) (string, bool, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	stamp, ok := d.stamps[user]
	return stamp, user == "root", ok
}

func (d *fakeDir) set(user, stamp string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if stamp == "" {
		delete(d.stamps, user)
		return
	}
	d.stamps[user] = stamp
}

var (
	ownerID = Identity{User: "root", Stamp: "s-root", Owner: true}
	aliceID = Identity{User: "alice", Stamp: "s-alice-1"}
	bobID   = Identity{User: "bob", Stamp: "s-bob-1"}
)

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
	m := NewManager(testSecret, clk.Now, newDir())
	s, cookie, err := m.Issue(ownerID)
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
	if err != nil || got.ID != s.ID || !got.Expires.Equal(s.Expires) || got.User != "root" || !got.Owner {
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
	m := NewManager(testSecret, clk.Now, newDir())
	_, cookie, _ := m.Issue(ownerID)
	v := cookie.Value
	parts := strings.Split(v, ".")
	if len(parts) != 5 {
		t.Fatalf("unexpected value shape %q", v)
	}
	// Extend the expiry without re-signing.
	forgedExp := strings.Join([]string{parts[0], "99999999999", parts[2], parts[3], parts[4]}, ".")
	// Flip a signature character.
	sig := []byte(parts[4])
	if sig[0] == 'A' {
		sig[0] = 'B'
	} else {
		sig[0] = 'A'
	}
	badSig := strings.Join(parts[:4], ".") + "." + string(sig)
	// Another user's name or stamp under the same signature.
	forgedUser := strings.Join([]string{parts[0], parts[1], base64.RawURLEncoding.EncodeToString([]byte("alice")), parts[3], parts[4]}, ".")
	forgedStamp := strings.Join([]string{parts[0], parts[1], parts[2], "s-alice-1", parts[4]}, ".")
	other := NewManager([]byte(strings.Repeat("z", 32)), clk.Now, newDir())

	for name, val := range map[string]string{
		"forged expiry": forgedExp, "bad signature": badSig, "empty": "", "garbage": "abc",
		"forged user": forgedUser, "forged stamp": forgedStamp,
		"two parts": parts[0] + "." + parts[1], "six parts": v + ".x", "non numeric exp": strings.Join([]string{parts[0], "x", parts[2], parts[3], parts[4]}, "."),
		"user not base64": strings.Join([]string{parts[0], parts[1], "!!", parts[3], parts[4]}, "."),
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
	m := NewManager(testSecret, nil, newDir())
	a, _, _ := m.Issue(ownerID)
	b, _, _ := m.Issue(ownerID)
	if a.ID == b.ID {
		t.Fatal("session ids must be random")
	}
}

func TestRevokedSessionIsRejectedUntilItWouldExpire(t *testing.T) {
	clk := newClock()
	m := NewManager(testSecret, clk.Now, newDir())
	a, cookieA, _ := m.Issue(ownerID)
	_, cookieB, _ := m.Issue(ownerID)

	m.Revoke(a)
	if _, err := m.Verify(cookieA.Value); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("revoked session verified: %v", err)
	}
	r := mustReq(t)
	r.AddCookie(cookieA)
	if _, err := m.FromRequest(r); err == nil {
		t.Fatal("FromRequest accepted a revoked session")
	}
	if _, err := m.Verify(cookieB.Value); err != nil {
		t.Fatalf("revoking one session must not affect another: %v", err)
	}
	if m.revokedCount() != 1 {
		t.Fatalf("revoked entries = %d, want 1", m.revokedCount())
	}

	// The entry is only needed while the signature would still be accepted.
	clk.Advance(SessionLifetime)
	m.Revoke(Session{ID: strings.Repeat("0", 32), Expires: clk.Now().Add(time.Hour)})
	if m.revokedCount() != 1 {
		t.Fatalf("expired revocations must be pruned: %d entries, want 1", m.revokedCount())
	}
	// Revoking an already expired session stores nothing.
	m.Revoke(a)
	if m.revokedCount() != 1 {
		t.Fatalf("revoking an expired session stored it: %d entries", m.revokedCount())
	}
}

func TestExpiredCookie(t *testing.T) {
	m := NewManager(testSecret, nil, newDir())
	c := m.ExpiredCookie()
	if c.Name != SessionCookieName || c.Value != "" || c.MaxAge >= 0 || !c.HttpOnly || !c.Secure ||
		c.SameSite != http.SameSiteStrictMode || c.Path != "/" || !c.Expires.Before(time.Now()) {
		t.Fatalf("logout cookie wrong: %+v", c)
	}
}

func TestCSRF(t *testing.T) {
	m := NewManager(testSecret, nil, newDir())
	a, _, _ := m.Issue(ownerID)
	b, _, _ := m.Issue(ownerID)
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
	if tok == strings.Split(mustCookieValue(t, m, a), ".")[4] {
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

func TestSessionCarriesTheUserAndTheOwnerFlag(t *testing.T) {
	m := NewManager(testSecret, nil, newDir())
	for _, tc := range []struct {
		id    Identity
		owner bool
	}{{ownerID, true}, {aliceID, false}} {
		s, cookie, err := m.Issue(tc.id)
		if err != nil {
			t.Fatal(err)
		}
		if s.User != tc.id.User || s.Stamp != tc.id.Stamp || s.Owner != tc.owner {
			t.Fatalf("issued session = %+v for %+v", s, tc.id)
		}
		got, err := m.Verify(cookie.Value)
		if err != nil || got.User != tc.id.User || got.Owner != tc.owner {
			t.Fatalf("verified session = %+v, %v", got, err)
		}
	}
}

func TestSessionOfAnUnknownOrChangedUserIsRejected(t *testing.T) {
	dir := newDir()
	m := NewManager(testSecret, nil, dir)
	_, aliceCookie, _ := m.Issue(aliceID)
	_, bobCookie, _ := m.Issue(bobID)
	_, ownerCookie, _ := m.Issue(ownerID)

	// A password set changes alice's stamp: only her sessions end.
	dir.set("alice", "s-alice-2")
	if _, err := m.Verify(aliceCookie.Value); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("session with an outdated stamp verified: %v", err)
	}
	if _, err := m.Verify(bobCookie.Value); err != nil {
		t.Fatalf("another user's session must survive: %v", err)
	}
	// A session issued with the new stamp works.
	if _, c, _ := m.Issue(Identity{User: "alice", Stamp: "s-alice-2"}); c == nil {
		t.Fatal("no cookie")
	} else if _, err := m.Verify(c.Value); err != nil {
		t.Fatalf("session with the current stamp rejected: %v", err)
	}

	// Deleting bob ends his sessions, also if the name is created again with a new stamp.
	dir.set("bob", "")
	if _, err := m.Verify(bobCookie.Value); err == nil {
		t.Fatal("session of a deleted user verified")
	}
	dir.set("bob", "s-bob-2")
	if _, err := m.Verify(bobCookie.Value); err == nil {
		t.Fatal("session of a recreated user verified")
	}

	// A new owner password changes the owner's stamp.
	dir.set("root", "s-root-new")
	if _, err := m.Verify(ownerCookie.Value); err == nil {
		t.Fatal("owner session verified after the owner's stamp changed")
	}
}

func TestSessionsNeedADirectory(t *testing.T) {
	m := NewManager(testSecret, nil, nil)
	_, cookie, err := NewManager(testSecret, nil, newDir()).Issue(ownerID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Verify(cookie.Value); err == nil {
		t.Fatal("a manager without a directory accepted a session")
	}
}

// S5: cookies issued before users existed ("<id>.<exp>.<sig>") stop working.
func TestOldFormatCookieIsRejected(t *testing.T) {
	clk := newClock()
	m := NewManager(testSecret, clk.Now, newDir())
	id := strings.Repeat("a", 32)
	exp := strconv.FormatInt(clk.Now().Add(time.Hour).Unix(), 10)
	old := id + "." + exp + "." + m.sign("session", id+"."+exp)
	if _, err := m.Verify(old); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("old-format cookie verified: %v", err)
	}
}

func TestIssueRejectsAnIdentityWithoutUser(t *testing.T) {
	m := NewManager(testSecret, nil, newDir())
	if _, _, err := m.Issue(Identity{Stamp: "x"}); err == nil {
		t.Fatal("Issue accepted an identity without a user")
	}
}

type fakeUsers struct {
	stamps map[string]string
	pass   map[string]string
	calls  int
}

func (f *fakeUsers) Stamp(name string) (string, bool) { s, ok := f.stamps[name]; return s, ok }

func (f *fakeUsers) Authenticate(name, password string) (string, string, bool) {
	f.calls++
	name = strings.ToLower(name)
	want, ok := f.pass[name]
	return name, f.stamps[name], ok && want == password
}

func newAccounts(t *testing.T, ownerPassword, fingerprint string, users UserStore) *Accounts {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(ownerPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return NewAccounts("Boss", hash, []byte(fingerprint), testSecret, users)
}

func TestAccountsAuthenticateTheOwnerAndStoredUsers(t *testing.T) {
	users := &fakeUsers{stamps: map[string]string{"alice": "s1"}, pass: map[string]string{"alice": "alice password"}}
	a := newAccounts(t, "owner password", "fp", users)

	id, ok := a.Authenticate("Boss", "owner password")
	if !ok || id.User != "Boss" || !id.Owner || id.Stamp == "" {
		t.Fatalf("owner = %+v, %v", id, ok)
	}
	if users.calls != 0 {
		t.Fatal("the owner's sign-in must not consult the store")
	}
	id, ok = a.Authenticate("ALICE", "alice password")
	if !ok || id.User != "alice" || id.Owner || id.Stamp != "s1" {
		t.Fatalf("stored user = %+v, %v", id, ok)
	}
	for _, tc := range [][2]string{
		{"Boss", "alice password"}, {"boss", "owner password"}, {"alice", "owner password"},
		{"alice", ""}, {"carol", "alice password"}, {"", "owner password"},
	} {
		if _, ok := a.Authenticate(tc[0], tc[1]); ok {
			t.Errorf("Authenticate(%q, %q) accepted", tc[0], tc[1])
		}
	}
}

func TestAccountsStillCompareForAnUnknownUser(t *testing.T) {
	users := &fakeUsers{stamps: map[string]string{}, pass: map[string]string{}}
	a := newAccounts(t, "owner password", "fp", users)
	a.Authenticate("nobody", "whatever")
	if users.calls != 1 {
		t.Fatalf("store consulted %d times for an unknown user, want 1 (it owns the dummy comparison)", users.calls)
	}
}

func TestAccountsResolve(t *testing.T) {
	users := &fakeUsers{stamps: map[string]string{"alice": "s1", "Boss": "shadow"}, pass: map[string]string{}}
	a := newAccounts(t, "owner password", "fp", users)

	if stamp, owner, ok := a.Resolve("alice"); !ok || owner || stamp != "s1" {
		t.Fatalf("alice = %q %v %v", stamp, owner, ok)
	}
	stamp, owner, ok := a.Resolve("Boss")
	if !ok || !owner || stamp == "" || stamp == "shadow" {
		t.Fatalf("the owner must win over a stored entry with the same name: %q %v %v", stamp, owner, ok)
	}
	if _, _, ok := a.Resolve("carol"); ok {
		t.Fatal("unknown user resolved")
	}
	if _, _, ok := NewAccounts("Boss", []byte("x"), []byte("fp"), testSecret, nil).Resolve("alice"); ok {
		t.Fatal("without a store only the owner exists")
	}
}

// Changing the owner's password in the stack variables must end its sessions, and nothing else
// may: the stamp follows the password fingerprint, not the (re-salted) hash.
func TestOwnerStampFollowsThePasswordFingerprint(t *testing.T) {
	stamp := func(password, fingerprint string) string {
		s, _, _ := newAccounts(t, password, fingerprint, nil).Resolve("Boss")
		return s
	}
	a, b := stamp("one", "fp-1"), stamp("one-but-rehashed", "fp-1")
	if a == "" || a != b {
		t.Fatalf("same fingerprint, different hash salts: %q vs %q", a, b)
	}
	if c := stamp("one", "fp-2"); c == a {
		t.Fatal("a different fingerprint must change the owner's stamp")
	}
	other, _, _ := NewAccounts("Boss", []byte("x"), []byte("fp-1"), []byte(strings.Repeat("z", 32)), nil).Resolve("Boss")
	if other == a {
		t.Fatal("the stamp must depend on the session secret")
	}
	if strings.Contains(a, "fp-1") {
		t.Fatal("the stamp leaks the fingerprint")
	}
}

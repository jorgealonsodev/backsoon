package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const alicePassword = "alice has a long password"

// loginAs signs in as user and returns the session with its CSRF token.
func (hn *harness) loginAs(user, password string) session {
	hn.t.Helper()
	w := hn.postLogin(user, password, "")
	if w.Code != http.StatusSeeOther {
		hn.t.Fatalf("login as %q = %d: %s", user, w.Code, w.Body.String())
	}
	var s session
	for _, c := range w.Result().Cookies() {
		if c.Name == "backsoon_session" {
			s.cookie = c
		}
	}
	if s.cookie == nil {
		hn.t.Fatal("login set no session cookie")
	}
	page := hn.get("/pages/new", s)
	s.csrf = hiddenValue(csrfRe, page.Body.String())
	if s.csrf == "" {
		hn.t.Fatalf("no csrf token in /pages/new: %d", page.Code)
	}
	return s
}

func (hn *harness) addUser(name, password string) {
	hn.t.Helper()
	if _, err := hn.users.Create(name, password); err != nil {
		hn.t.Fatalf("create user %q: %v", name, err)
	}
}

func wantSignedIn(t *testing.T, hn *harness, s session) {
	t.Helper()
	wantStatus(t, hn.get("/", s), 200)
}

func wantSignedOut(t *testing.T, hn *harness, s session) {
	t.Helper()
	w := hn.get("/", s)
	wantStatus(t, w, http.StatusSeeOther)
	if loc := w.Header().Get("Location"); loc != "/login" {
		t.Fatalf("Location = %q, want /login", loc)
	}
}

func TestStoredUserSignsInAndManagesPages(t *testing.T) {
	hn := newHarness(t)
	hn.addUser("alice", alicePassword)

	s := hn.loginAs("alice", alicePassword)
	w := hn.get("/", s)
	wantStatus(t, w, 200)
	mustContain(t, w.Body.String(), `<span class="who">alice</span>`)
	mustNotContain(t, w.Body.String(), `<span class="who">`+testUser+`</span>`)
	mustContain(t, hn.logs.String(), `login ok user="alice"`)

	// The same page flows as the owner: open, edit form, delete.
	page := hn.createPage("Alice's page", "alice.example.com")
	wantStatus(t, hn.get("/pages/"+page.ID, s), 200)
	wantStatus(t, hn.get("/pages/new", s), 200)
	wantStatus(t, hn.postForm("/pages/"+page.ID+"/delete", s, url.Values{"confirm": {"true"}}), http.StatusSeeOther)
}

func TestOwnerNameInTheNavbarAndLogIsTheConfiguredOne(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	mustContain(t, hn.get("/", s).Body.String(), `<span class="who">`+testUser+`</span>`)
	mustContain(t, hn.logs.String(), `login ok user="`+testUser+`"`)
}

func TestStoredUserNameIsCaseInsensitiveAtSignIn(t *testing.T) {
	hn := newHarness(t)
	hn.addUser("Alice", alicePassword)
	s := hn.loginAs("ALICE", alicePassword)
	mustContain(t, hn.get("/", s).Body.String(), `<span class="who">alice</span>`)
}

// S5: one error message and status for an unknown user, a wrong password and a wrong password of a
// stored user.
func TestSignInFailuresAreIndistinguishable(t *testing.T) {
	hn := newHarness(t)
	hn.addUser("alice", alicePassword)
	var want string
	for i, tc := range [][2]string{
		{"alice", "not the password"}, {"nobody", alicePassword}, {testUser, "not the password"}, {"alice", ""}, {"", ""},
	} {
		w := hn.postLogin(tc[0], tc[1], "")
		wantStatus(t, w, http.StatusUnauthorized)
		mustContain(t, w.Body.String(), "Wrong user or password")
		for _, c := range w.Result().Cookies() {
			if c.Name == "backsoon_session" {
				t.Fatalf("%v: failed login set a session", tc)
			}
		}
		// The page differs only by the echoed user name and the one-time form token.
		body := strings.NewReplacer(tc[0], "", `value=""`, "").Replace(w.Body.String())
		if i == 0 {
			want = errorSentence(body)
		} else if got := errorSentence(body); got != want {
			t.Errorf("%v: error text %q differs from %q", tc, got, want)
		}
	}
	mustNotContain(t, hn.logs.String(), alicePassword, "not the password")
}

func errorSentence(body string) string {
	i := strings.Index(body, "Wrong user or password")
	if i < 0 {
		return ""
	}
	return body[i : i+len("Wrong user or password")]
}

func TestStoredUserSignInIsRateLimited(t *testing.T) {
	hn := newHarness(t)
	hn.addUser("alice", alicePassword)
	for range 5 {
		wantStatus(t, hn.postLogin("alice", "wrong password!", "203.0.113.9"), http.StatusUnauthorized)
	}
	// The sixth attempt is refused even with the right password.
	wantStatus(t, hn.postLogin("alice", alicePassword, "203.0.113.9"), http.StatusTooManyRequests)
}

func TestSessionEndsWhenTheUserIsDeleted(t *testing.T) {
	hn := newHarness(t)
	hn.addUser("alice", alicePassword)
	hn.addUser("bob", "bob has a long password")
	a1, a2 := hn.loginAs("alice", alicePassword), hn.loginAs("alice", alicePassword)
	b := hn.loginAs("bob", "bob has a long password")
	owner := hn.login()

	if err := hn.users.Delete("alice"); err != nil {
		t.Fatal(err)
	}
	wantSignedOut(t, hn, a1)
	wantSignedOut(t, hn, a2)
	wantSignedIn(t, hn, b)
	wantSignedIn(t, hn, owner)

	// Creating the name again does not bring the old sessions back.
	hn.addUser("alice", alicePassword)
	wantSignedOut(t, hn, a1)
	wantStatus(t, hn.postLogin("alice", alicePassword, ""), http.StatusSeeOther)
}

func TestSessionEndsWhenThePasswordIsSet(t *testing.T) {
	hn := newHarness(t)
	hn.addUser("alice", alicePassword)
	hn.addUser("bob", "bob has a long password")
	a := hn.loginAs("alice", alicePassword)
	b := hn.loginAs("bob", "bob has a long password")

	if err := hn.users.SetPassword("alice", "alice has another password"); err != nil {
		t.Fatal(err)
	}
	wantSignedOut(t, hn, a)
	wantSignedIn(t, hn, b)
	hn.loginAs("alice", "alice has another password")
	wantStatus(t, hn.postLogin("alice", alicePassword, ""), http.StatusUnauthorized)
}

func TestRevocationsSurviveARestart(t *testing.T) {
	hn := newHarness(t)
	hn.addUser("alice", alicePassword)
	hn.addUser("bob", "bob has a long password")
	hn.addUser("carol", "carol has a long password")
	a := hn.loginAs("alice", alicePassword)
	b := hn.loginAs("bob", "bob has a long password")
	c := hn.loginAs("carol", "carol has a long password")
	owner := hn.login()

	hn.users.Delete("alice")
	hn.users.SetPassword("bob", "bob has another password")

	hn.restart()
	wantSignedOut(t, hn, a)
	wantSignedOut(t, hn, b)
	wantSignedIn(t, hn, c)
	wantSignedIn(t, hn, owner)
	hn.loginAs("bob", "bob has another password")
}

func TestOwnerSessionEndsWhenTheConfiguredHashChanges(t *testing.T) {
	hn := newHarness(t)
	owner := hn.login()
	wantSignedIn(t, hn, owner)

	// Same stack, same secret, a new BACKSOON_ADMIN_PASSWORD_HASH.
	newHash, err := bcrypt.GenerateFromPassword([]byte("a different owner password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	hn.h = hn.newHandler(newHash)
	wantSignedOut(t, hn, owner)

	// Restarting with the unchanged hash keeps the sessions.
	hn.hash = newHash
	fresh := hn.postLogin(testUser, "a different owner password", "")
	wantStatus(t, fresh, http.StatusSeeOther)
	var cookie *http.Cookie
	for _, c := range fresh.Result().Cookies() {
		if c.Name == "backsoon_session" {
			cookie = c
		}
	}
	hn.restart()
	wantSignedIn(t, hn, session{cookie: cookie})
}

// S5: a session cookie from before users existed, even validly signed, is not accepted.
func TestOldFormatSessionCookieIsRejected(t *testing.T) {
	hn := newHarness(t)
	id := strings.Repeat("ab", 16)
	exp := strconv.FormatInt(hn.clock.Now().Add(time.Hour).Unix(), 10)
	mac := hmac.New(sha256.New, testSecret)
	mac.Write([]byte("session"))
	mac.Write([]byte{0})
	mac.Write([]byte(id + "." + exp))
	old := id + "." + exp + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	wantSignedOut(t, hn, session{cookie: &http.Cookie{Name: "backsoon_session", Value: old}})
	wantStatus(t, hn.serve(fetchReq(httptest.NewRequest(http.MethodGet, "/", nil))), http.StatusUnauthorized)
}

func TestLogoutStillRevokesTheSession(t *testing.T) {
	hn := newHarness(t)
	hn.addUser("alice", alicePassword)
	s := hn.loginAs("alice", alicePassword)
	other := hn.loginAs("alice", alicePassword)
	w := hn.postForm("/logout", s, url.Values{})
	wantStatus(t, w, http.StatusSeeOther)
	wantSignedOut(t, hn, s)
	wantSignedIn(t, hn, other)
}

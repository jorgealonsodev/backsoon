package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	bobPassword   = "bob has a long password"
	carolPassword = "carol has a long password"
	newPassword   = "a brand new password"
)

// --- helpers -------------------------------------------------------------------------------

func createForm(name, password, confirm string) url.Values {
	return url.Values{"name": {name}, "password": {password}, "confirm_password": {confirm}}
}

func passwordForm(user, password, confirm string) url.Values {
	return url.Values{"user": {user}, "password": {password}, "confirm_password": {confirm}, "confirm": {"true"}}
}

func ownPasswordForm(current, password, confirm string) url.Values {
	return url.Values{"current": {current}, "password": {password}, "confirm_password": {confirm}}
}

func sessionCookie(w *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == "backsoon_session" {
			return c
		}
	}
	return nil
}

func wantRedirect(t *testing.T, w *httptest.ResponseRecorder, location string) {
	t.Helper()
	wantStatus(t, w, http.StatusSeeOther)
	if got := w.Header().Get("Location"); got != location {
		t.Fatalf("Location = %q, want %q", got, location)
	}
}

func userNames(hn *harness) []string {
	var out []string
	for _, u := range hn.users.List() {
		out = append(out, u.Name)
	}
	return out
}

func wantUsers(t *testing.T, hn *harness, want ...string) {
	t.Helper()
	got := userNames(hn)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("users = %v, want %v", got, want)
	}
}

// --- S3: the owner manages users ------------------------------------------------------------

func TestOwnerSeesTheUserListAndTheOwnerNote(t *testing.T) {
	hn := newHarness(t)
	o := hn.login()

	w := hn.get("/users", o)
	wantStatus(t, w, 200)
	mustContain(t, w.Body.String(), `data-view="users"`, "No added users yet", `action="/users"`,
		`name="name"`, `name="password"`, `name="confirm_password"`, `name="csrf"`, testUser)
	mustNotContain(t, w.Body.String(), `/users/delete`)

	hn.addUser("bob", bobPassword)
	hn.addUser("alice", alicePassword)
	w = hn.get("/users", o)
	wantStatus(t, w, 200)
	body := w.Body.String()
	mustContain(t, body, "alice", "bob", "2026-10-13 21:04 UTC", `action="/users/delete"`, `href="/users/password?user=alice"`)
	mustNotContain(t, body, "No added users yet", "$2a$", "$2y$", bobPassword, alicePassword)
	if strings.Index(body, ">alice<") > strings.Index(body, ">bob<") {
		t.Errorf("users are not listed by name")
	}
}

func TestNavbarShowsUsersOnlyToTheOwner(t *testing.T) {
	hn := newHarness(t)
	hn.addUser("alice", alicePassword)
	o, e := hn.login(), hn.loginAs("alice", alicePassword)

	for _, p := range []string{"/", "/pages/new", "/account"} {
		ob, eb := hn.get(p, o).Body.String(), hn.get(p, e).Body.String()
		mustContain(t, ob, `href="/users"`, `<span class="who">`+testUser+`</span>`, `href="/account"`)
		mustContain(t, eb, `<span class="who">alice</span>`, `href="/account"`)
		mustNotContain(t, eb, `href="/users"`)
	}
}

func TestOwnerCreatesAUser(t *testing.T) {
	hn := newHarness(t)
	o := hn.login()

	w := hn.postForm("/users", o, createForm("Bob", bobPassword, bobPassword))
	wantRedirect(t, w, "/users?notice=user-created")
	wantUsers(t, hn, "bob")
	notice := hn.get("/users?notice=user-created", o).Body.String()
	mustContain(t, notice, "User created.", "bob")
	mustContain(t, hn.logs.String(), `user created by="`+testUser+`" user="bob"`)

	// The new user signs in and manages pages like the owner.
	b := hn.loginAs("bob", bobPassword)
	page := hn.createPage("Bob's page", "bob.example.com")
	wantStatus(t, hn.get("/pages/"+page.ID, b), 200)
	wantStatus(t, hn.postForm("/pages/"+page.ID+"/delete", b, url.Values{"confirm": {"true"}}), http.StatusSeeOther)

	// users.json: mode 0600, hashes only.
	path := filepath.Join(hn.metaDir, "users.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("users.json mode = %v, want 0600", info.Mode().Perm())
	}
	raw := readFile(t, path)
	if strings.Contains(raw, bobPassword) || !strings.Contains(raw, "$2a$") {
		t.Errorf("users.json holds a plaintext password or no bcrypt hash: %s", raw)
	}
}

func TestCreateUserRefusesWhatTheStoreRefuses(t *testing.T) {
	hn := newHarness(t)
	hn.addUser("alice", alicePassword)
	o := hn.login()

	for _, tc := range []struct {
		name, pw, confirm string
		status            int
		want              string
	}{
		{"bad name", bobPassword, bobPassword, 422, "Use 1 to 64 characters"},
		{"", bobPassword, bobPassword, 422, "Use 1 to 64 characters"},
		{strings.Repeat("a", 65), bobPassword, bobPassword, 422, "Use 1 to 64 characters"},
		{"ünï", bobPassword, bobPassword, 422, "Use 1 to 64 characters"},
		{testUser, bobPassword, bobPassword, 422, "That is the owner&#39;s user name."},
		{strings.ToUpper(testUser), bobPassword, bobPassword, 422, "That is the owner&#39;s user name."},
		{"alice", bobPassword, bobPassword, 409, "A user with that name already exists."},
		{"ALICE", bobPassword, bobPassword, 409, "A user with that name already exists."},
		{"bob", "short", "short", 422, "The password needs at least 10 characters."},
		{"bob", strings.Repeat("x", 73), strings.Repeat("x", 73), 422, "The password can be at most 72 bytes."},
		{"bob", bobPassword, bobPassword + "!", 422, "The two passwords are different."},
		{"bob", bobPassword, "", 422, "The two passwords are different."},
	} {
		w := hn.postForm("/users", o, createForm(tc.name, tc.pw, tc.confirm))
		wantStatus(t, w, tc.status)
		mustContain(t, w.Body.String(), tc.want, `data-view="users"`)
		// Neither password is echoed back; the name is, escaped.
		mustNotContain(t, w.Body.String(), bobPassword, tc.pw+"\"")
		wantUsers(t, hn, "alice")
	}

	// The name the owner typed stays in the form.
	w := hn.postForm("/users", o, createForm("Bob", "short", "short"))
	mustContain(t, w.Body.String(), `value="Bob"`)
}

func TestCreateUserWithDotNames(t *testing.T) {
	hn := newHarness(t)
	o := hn.login()
	for _, name := range []string{".", ".."} {
		wantRedirect(t, hn.postForm("/users", o, createForm(name, bobPassword, bobPassword)), "/users?notice=user-created")
	}
	wantUsers(t, hn, ".", "..")
	// They can be managed: the name travels in the form, not in the path.
	for _, name := range []string{".", ".."} {
		wantStatus(t, hn.get("/users/password?user="+url.QueryEscape(name), o), 200)
		wantRedirect(t, hn.postForm("/users/password", o, passwordForm(name, newPassword, newPassword)), "/users?notice=user-password")
		wantRedirect(t, hn.postForm("/users/delete", o, url.Values{"user": {name}, "confirm": {"true"}}), "/users?notice=user-deleted")
	}
	wantUsers(t, hn)
}

func TestOwnerSetsAUserPasswordBehindAConfirmation(t *testing.T) {
	hn := newHarness(t)
	hn.addUser("alice", alicePassword)
	hn.addUser("bob", bobPassword)
	o := hn.login()
	a1, a2 := hn.loginAs("alice", alicePassword), hn.loginAs("alice", alicePassword)
	b := hn.loginAs("bob", bobPassword)

	form := hn.get("/users/password?user=alice", o)
	wantStatus(t, form, 200)
	mustContain(t, form.Body.String(), `data-view="user-password"`, `action="/users/password"`, `name="user" value="alice"`,
		`name="confirm" value="true"`, `name="password"`, `name="confirm_password"`, "Set a new password for")

	// Without the confirmation field nothing changes: the form is shown again.
	w := hn.postForm("/users/password", o, url.Values{"user": {"alice"}, "password": {newPassword}, "confirm_password": {newPassword}})
	wantStatus(t, w, 200)
	mustContain(t, w.Body.String(), `data-view="user-password"`)
	wantSignedIn(t, hn, a1)
	wantStatus(t, hn.postLogin("alice", alicePassword, ""), http.StatusSeeOther)

	w = hn.postForm("/users/password", o, passwordForm("Alice", newPassword, newPassword))
	wantRedirect(t, w, "/users?notice=user-password")
	mustContain(t, hn.logs.String(), `user password set by="`+testUser+`" user="alice"`)
	wantSignedOut(t, hn, a1)
	wantSignedOut(t, hn, a2)
	wantSignedIn(t, hn, b)
	wantSignedIn(t, hn, o)
	hn.loginAs("alice", newPassword)
	wantStatus(t, hn.postLogin("alice", alicePassword, ""), http.StatusUnauthorized)
}

func TestSetPasswordValidation(t *testing.T) {
	hn := newHarness(t)
	hn.addUser("alice", alicePassword)
	o := hn.login()
	a := hn.loginAs("alice", alicePassword)

	for _, tc := range []struct {
		pw, confirm string
		want        string
	}{
		{"short", "short", "The password needs at least 10 characters."},
		{strings.Repeat("x", 73), strings.Repeat("x", 73), "The password can be at most 72 bytes."},
		{newPassword, newPassword + "x", "The two passwords are different."},
	} {
		w := hn.postForm("/users/password", o, passwordForm("alice", tc.pw, tc.confirm))
		wantStatus(t, w, 422)
		mustContain(t, w.Body.String(), tc.want, `name="user" value="alice"`)
		mustNotContain(t, w.Body.String(), newPassword)
		wantSignedIn(t, hn, a)
	}
	wantStatus(t, hn.postForm("/users/password", o, passwordForm("nobody", newPassword, newPassword)), 404)
	wantStatus(t, hn.get("/users/password?user=nobody", o), 404)
	wantStatus(t, hn.get("/users/password", o), 404)
}

func TestOwnerDeletesAUserBehindAConfirmation(t *testing.T) {
	hn := newHarness(t)
	hn.addUser("alice", alicePassword)
	hn.addUser("bob", bobPassword)
	o := hn.login()
	a := hn.loginAs("alice", alicePassword)
	b := hn.loginAs("bob", bobPassword)

	w := hn.postForm("/users/delete", o, url.Values{"user": {"alice"}})
	wantStatus(t, w, 200)
	mustContain(t, w.Body.String(), `data-view="user-delete"`, `action="/users/delete"`, `name="confirm" value="true"`,
		`name="user" value="alice"`, "Delete “alice”?")
	wantUsers(t, hn, "alice", "bob")
	wantSignedIn(t, hn, a)

	wantRedirect(t, hn.postForm("/users/delete", o, url.Values{"user": {"Alice"}, "confirm": {"true"}}), "/users?notice=user-deleted")
	wantUsers(t, hn, "bob")
	mustContain(t, hn.logs.String(), `user deleted by="`+testUser+`" user="alice"`)
	wantSignedOut(t, hn, a)
	wantSignedIn(t, hn, b)
	wantSignedIn(t, hn, o)
	wantStatus(t, hn.postLogin("alice", alicePassword, ""), http.StatusUnauthorized)

	wantStatus(t, hn.postForm("/users/delete", o, url.Values{"user": {"alice"}, "confirm": {"true"}}), 404)
	wantStatus(t, hn.postForm("/users/delete", o, url.Values{"confirm": {"true"}}), 404)
}

func TestTheOwnerCannotBeManagedFromThePanel(t *testing.T) {
	hn := newHarness(t)
	o := hn.login()
	for _, name := range []string{testUser, strings.ToUpper(testUser)} {
		w := hn.postForm("/users/delete", o, url.Values{"user": {name}, "confirm": {"true"}})
		wantStatus(t, w, 403)
		mustContain(t, w.Body.String(), "can&#39;t be changed here")
		wantStatus(t, hn.postForm("/users/password", o, passwordForm(name, newPassword, newPassword)), 403)
		wantStatus(t, hn.get("/users/password?user="+name, o), 403)
	}
	wantSignedIn(t, hn, o)
	wantStatus(t, hn.postLogin(testUser, testPassword, ""), http.StatusSeeOther)
}

// --- S6: authorization and CSRF -------------------------------------------------------------

func TestEditorGetsForbiddenOnEveryUserRoute(t *testing.T) {
	hn := newHarness(t)
	hn.addUser("alice", alicePassword)
	hn.addUser("bob", bobPassword)
	e := hn.loginAs("alice", alicePassword)
	b := hn.loginAs("bob", bobPassword)
	o := hn.login()

	reqs := []struct {
		method, path string
		form         url.Values
	}{
		{"GET", "/users", nil},
		{"GET", "/users/password?user=bob", nil},
		{"POST", "/users", createForm("carol", carolPassword, carolPassword)},
		{"POST", "/users/password", passwordForm("bob", newPassword, newPassword)},
		{"POST", "/users/delete", url.Values{"user": {"bob"}}},
		{"POST", "/users/delete", url.Values{"user": {"bob"}, "confirm": {"true"}}},
		{"POST", "/users/delete", url.Values{"user": {"alice"}, "confirm": {"true"}}}, // themselves
		{"POST", "/users/delete", url.Values{"user": {"bob"}, "confirm": {"true"}, "csrf": {"-"}}},
	}
	for _, rq := range reqs {
		var w *httptest.ResponseRecorder
		if rq.method == "GET" {
			w = hn.get(rq.path, e)
		} else {
			w = hn.postForm(rq.path, e, rq.form)
		}
		wantStatus(t, w, http.StatusForbidden)
		mustContain(t, w.Body.String(), "Only the owner can manage users.")
		mustNotContain(t, w.Body.String(), `data-view="users"`, `class="name"`, "Delete user", "Add user")
	}
	wantUsers(t, hn, "alice", "bob")
	wantSignedIn(t, hn, e)
	wantSignedIn(t, hn, b)
	mustContain(t, hn.logs.String(), `forbidden user="alice" POST /users/password`)
	wantStatus(t, hn.postLogin("bob", bobPassword, ""), http.StatusSeeOther)
	wantSignedIn(t, hn, o)
}

func TestUserRoutesNeedASession(t *testing.T) {
	hn := newHarness(t)
	hn.addUser("alice", alicePassword)
	for _, tc := range []struct{ method, path string }{
		{"GET", "/users"}, {"POST", "/users"}, {"GET", "/users/password?user=alice"}, {"POST", "/users/password"},
		{"POST", "/users/delete"}, {"GET", "/account"}, {"POST", "/account/password"},
	} {
		nav := navigate(httptest.NewRequest(tc.method, tc.path, strings.NewReader("")))
		w := hn.serve(nav)
		wantRedirect(t, w, "/login")
		f := hn.serve(fetchReq(httptest.NewRequest(tc.method, tc.path, strings.NewReader(""))))
		wantStatus(t, f, http.StatusUnauthorized)
	}
	wantUsers(t, hn, "alice")
}

func TestEveryNewPostNeedsTheCSRFToken(t *testing.T) {
	hn := newHarness(t)
	hn.addUser("alice", alicePassword)
	hn.addUser("bob", bobPassword)
	o := hn.login()
	a := hn.loginAs("alice", alicePassword)

	for _, tc := range []struct {
		who  session
		path string
		form url.Values
	}{
		{o, "/users", createForm("carol", carolPassword, carolPassword)},
		{o, "/users/password", passwordForm("bob", newPassword, newPassword)},
		{o, "/users/delete", url.Values{"user": {"bob"}, "confirm": {"true"}}},
		{a, "/account/password", ownPasswordForm(alicePassword, newPassword, newPassword)},
	} {
		for name, token := range map[string]string{"missing": "-", "wrong": "not-the-token", "other session": hn.loginAs("bob", bobPassword).csrf} {
			form := url.Values{}
			for k, v := range tc.form {
				form[k] = v
			}
			form.Set("csrf", token)
			w := hn.postForm(tc.path, tc.who, form)
			if w.Code != http.StatusForbidden {
				t.Errorf("POST %s with a %s token = %d, want 403", tc.path, name, w.Code)
			}
			mustContain(t, w.Body.String(), "This form has expired")
		}
	}
	wantUsers(t, hn, "alice", "bob")
	wantSignedIn(t, hn, a)
	wantStatus(t, hn.postLogin("alice", alicePassword, ""), http.StatusSeeOther)
	wantStatus(t, hn.postLogin("bob", bobPassword, ""), http.StatusSeeOther)
}

// --- S4: the editor's own password ---------------------------------------------------------

func TestEditorChangesTheirOwnPassword(t *testing.T) {
	hn := newHarness(t)
	hn.addUser("alice", alicePassword)
	hn.addUser("bob", bobPassword)
	cur, other := hn.loginAs("alice", alicePassword), hn.loginAs("alice", alicePassword)
	b := hn.loginAs("bob", bobPassword)

	form := hn.get("/account", cur)
	wantStatus(t, form, 200)
	mustContain(t, form.Body.String(), `data-view="account"`, `action="/account/password"`, `name="current"`,
		`name="password"`, `name="confirm_password"`, `autocomplete="current-password"`, `autocomplete="new-password"`)
	mustNotContain(t, form.Body.String(), "BACKSOON_ADMIN_PASSWORD")

	w := hn.postForm("/account/password", cur, ownPasswordForm(alicePassword, newPassword, newPassword))
	wantRedirect(t, w, "/account?notice=password-changed")
	mustContain(t, hn.logs.String(), `password changed user="alice"`)

	fresh := sessionCookie(w)
	if fresh == nil || fresh.Value == cur.cookie.Value {
		t.Fatalf("no new session cookie: %v", fresh)
	}
	// The browser keeps working through the new cookie; every other session of the user ends.
	next := session{cookie: fresh}
	wantSignedIn(t, hn, next)
	notice := hn.get("/account?notice=password-changed", next)
	wantStatus(t, notice, 200)
	mustContain(t, notice.Body.String(), "Password changed.")
	wantSignedOut(t, hn, cur)
	wantSignedOut(t, hn, other)
	wantSignedIn(t, hn, b)
	hn.loginAs("alice", newPassword)
	wantStatus(t, hn.postLogin("alice", alicePassword, ""), http.StatusUnauthorized)

	// The new session carries its own CSRF token and can be revoked on its own.
	page := hn.get("/pages/new", next)
	next.csrf = hiddenValue(csrfRe, page.Body.String())
	wantStatus(t, hn.postForm("/logout", next, url.Values{}), http.StatusSeeOther)
	wantSignedOut(t, hn, next)
}

func TestOwnPasswordValidation(t *testing.T) {
	hn := newHarness(t)
	hn.addUser("alice", alicePassword)
	a := hn.loginAs("alice", alicePassword)

	for _, tc := range []struct {
		current, pw, confirm, want string
	}{
		{"not my password", newPassword, newPassword, "The current password is wrong."},
		{"", newPassword, newPassword, "The current password is wrong."},
		{alicePassword, newPassword, newPassword + "x", "The two passwords are different."},
		{alicePassword, "short", "short", "The password needs at least 10 characters."},
		{alicePassword, strings.Repeat("x", 73), strings.Repeat("x", 73), "The password can be at most 72 bytes."},
	} {
		w := hn.postForm("/account/password", a, ownPasswordForm(tc.current, tc.pw, tc.confirm))
		wantStatus(t, w, 422)
		mustContain(t, w.Body.String(), tc.want, `data-view="account"`)
		mustNotContain(t, w.Body.String(), alicePassword, newPassword, "not my password")
		if sessionCookie(w) != nil {
			t.Errorf("a refused change issued a session cookie")
		}
		wantSignedIn(t, hn, a)
	}
	mustContain(t, hn.logs.String(), `password change failed user="alice"`)
	wantStatus(t, hn.postLogin("alice", alicePassword, ""), http.StatusSeeOther)
}

func TestOwnPasswordCheckIsRateLimited(t *testing.T) {
	hn := newHarness(t)
	hn.addUser("alice", alicePassword)
	a := hn.loginAs("alice", alicePassword)
	for range 5 {
		wantStatus(t, hn.postForm("/account/password", a, ownPasswordForm("wrong password!", newPassword, newPassword)), 422)
	}
	w := hn.postForm("/account/password", a, ownPasswordForm(alicePassword, newPassword, newPassword))
	wantStatus(t, w, http.StatusTooManyRequests)
	if w.Header().Get("Retry-After") == "" {
		t.Errorf("no Retry-After")
	}
	wantStatus(t, hn.postLogin("alice", alicePassword, ""), http.StatusSeeOther)
}

func TestOwnerPasswordIsNotChangedInThePanel(t *testing.T) {
	hn := newHarness(t)
	o := hn.login()

	w := hn.get("/account", o)
	wantStatus(t, w, 200)
	mustContain(t, w.Body.String(), `data-view="account"`, "BACKSOON_ADMIN_PASSWORD_HASH", "BACKSOON_ADMIN_PASSWORD")
	mustNotContain(t, w.Body.String(), `name="current"`, `action="/account/password"`)

	w = hn.postForm("/account/password", o, ownPasswordForm(testPassword, newPassword, newPassword))
	wantStatus(t, w, 403)
	wantSignedIn(t, hn, o)
	wantStatus(t, hn.postLogin(testUser, testPassword, ""), http.StatusSeeOther)
	wantStatus(t, hn.postLogin(testUser, newPassword, ""), http.StatusUnauthorized)
}

// --- S7: logs ---------------------------------------------------------------------------------

func TestPageChangesNameTheActingUser(t *testing.T) {
	hn := newHarness(t)
	hn.addUser("alice", alicePassword)
	e, o := hn.loginAs("alice", alicePassword), hn.login()

	w := hn.postMultipart("/pages", e, templateFields("Acme", "acme.test"), nil)
	wantStatus(t, w, http.StatusSeeOther)
	id := strings.TrimPrefix(strings.SplitN(w.Header().Get("Location"), "focus=", 2)[1], "")
	id = strings.SplitN(id, "#", 2)[0]
	mustContainRaw(t, hn.logs.String(), `page created by="alice" id=`+id)

	w = hn.postMultipart("/pages/"+id, o, map[string]string{"name": "Acme 2", "destinations": "acme.test"}, nil)
	wantStatus(t, w, http.StatusSeeOther)
	mustContainRaw(t, hn.logs.String(), `page updated by="`+testUser+`" id=`+id)

	wantStatus(t, hn.postForm("/pages/"+id+"/delete", e, url.Values{"confirm": {"true"}}), http.StatusSeeOther)
	mustContainRaw(t, hn.logs.String(), `page deleted by="alice" id=`+id)
}

func TestLogsNeverHoldSecrets(t *testing.T) {
	hn := newHarness(t)
	o := hn.login()
	hn.postForm("/users", o, createForm("bob", bobPassword, bobPassword))
	hn.postForm("/users", o, createForm("bob", bobPassword, bobPassword))
	hn.postForm("/users", o, createForm("carol", carolPassword, "mismatch mismatch"))
	b := hn.loginAs("bob", bobPassword)
	hn.postForm("/account/password", b, ownPasswordForm("wrong current pw", newPassword, newPassword))
	w := hn.postForm("/account/password", b, ownPasswordForm(bobPassword, newPassword, newPassword))
	fresh := sessionCookie(w)
	f := session{cookie: fresh}
	f.csrf = hiddenValue(csrfRe, hn.get("/pages/new", f).Body.String())
	hn.postForm("/users", f, createForm("dave", "dave has a long password", "dave has a long password"))
	hn.postForm("/users/password", o, passwordForm("bob", "owner chose this one", "owner chose this one"))
	hn.postForm("/users/delete", o, url.Values{"user": {"bob"}, "confirm": {"true"}})

	logs := hn.logs.String()
	mustContain(t, logs, "user created", "user password set", "user deleted", "password changed", "forbidden")
	secrets := []string{bobPassword, carolPassword, newPassword, "wrong current pw", "owner chose this one", "mismatch mismatch",
		"dave has a long password", testPassword, "$2a$", "$2y$", o.cookie.Value, b.cookie.Value, fresh.Value, o.csrf, b.csrf, f.csrf}
	mustNotContain(t, logs, secrets...)
}

// --- S9: persistence ------------------------------------------------------------------------

func TestPanelChangesSurviveARestart(t *testing.T) {
	hn := newHarness(t)
	o := hn.login()
	wantRedirect(t, hn.postForm("/users", o, createForm("alice", alicePassword, alicePassword)), "/users?notice=user-created")
	wantRedirect(t, hn.postForm("/users", o, createForm("bob", bobPassword, bobPassword)), "/users?notice=user-created")
	wantRedirect(t, hn.postForm("/users", o, createForm("carol", carolPassword, carolPassword)), "/users?notice=user-created")
	a, b, c := hn.loginAs("alice", alicePassword), hn.loginAs("bob", bobPassword), hn.loginAs("carol", carolPassword)

	hn.postForm("/users/delete", o, url.Values{"user": {"alice"}, "confirm": {"true"}})
	hn.postForm("/users/password", o, passwordForm("bob", newPassword, newPassword))

	hn.restart()
	wantUsers(t, hn, "bob", "carol")
	wantSignedOut(t, hn, a)
	wantSignedOut(t, hn, b)
	wantSignedIn(t, hn, c)
	wantSignedIn(t, hn, o)
	hn.loginAs("bob", newPassword)
	wantStatus(t, hn.postLogin("alice", alicePassword, ""), http.StatusUnauthorized)
}

// --- S8: both languages ---------------------------------------------------------------------

func TestUserViewsAreFullyTranslated(t *testing.T) {
	hn := newHarness(t)
	hn.addUser("alice", alicePassword)
	o, e := hn.login(), hn.loginAs("alice", alicePassword)

	for name, w := range map[string]*httptest.ResponseRecorder{
		"users":         hn.getL("/users", o, "es"),
		"users notice":  hn.getL("/users?notice=user-created", o, "es"),
		"password":      hn.getL("/users/password?user=alice", o, "es"),
		"delete":        hn.postFormL("/users/delete", o, url.Values{"user": {"alice"}}, "es"),
		"create error":  hn.postFormL("/users", o, createForm("x y", "a", "b"), "es"),
		"exists":        hn.postFormL("/users", o, createForm("alice", alicePassword, alicePassword), "es"),
		"password err":  hn.postFormL("/users/password", o, passwordForm("alice", "a", "b"), "es"),
		"account owner": hn.getL("/account", o, "es"),
		"account":       hn.getL("/account?notice=password-changed", e, "es"),
		"account error": hn.postFormL("/account/password", e, ownPasswordForm("x", "a", "b"), "es"),
		"forbidden":     hn.getL("/users", e, "es"),
		"owner fixed":   hn.postFormL("/users/delete", o, url.Values{"user": {testUser}, "confirm": {"true"}}, "es"),
	} {
		body := w.Body.String()
		if strings.Contains(body, "⟦") {
			t.Errorf("%s: missing catalogue key\n%s", name, excerpt(body))
		}
		if pageLang(body) != "es" {
			t.Errorf("%s: lang = %q", name, pageLang(body))
		}
	}
	mustContain(t, hn.getL("/users", o, "es").Body.String(), "Usuarios", "Añadir usuario")
	mustContain(t, hn.getL("/account", e, "es").Body.String(), "Mi contraseña", "Contraseña actual")
	mustContain(t, hn.getL("/users", e, "es").Body.String(), "Solo el propietario puede gestionar usuarios.")
	mustContain(t, hn.getL("/users", o, "en").Body.String(), "Users", "Add user")
}

func TestLanguageLinksOnUserPagesReturnToThem(t *testing.T) {
	hn := newHarness(t)
	hn.addUser("alice", alicePassword)
	o, e := hn.login(), hn.loginAs("alice", alicePassword)
	for _, tc := range []struct {
		w    *httptest.ResponseRecorder
		next string
	}{
		{hn.get("/users", o), "%2Fusers"},
		{hn.postForm("/users", o, createForm("x y", "a", "b")), "%2Fusers"},
		{hn.postForm("/users/password", o, passwordForm("alice", "a", "b")), "%2Fusers"},
		{hn.postForm("/users/delete", o, url.Values{"user": {"alice"}}), "%2Fusers"},
		{hn.get("/account", e), "%2Faccount"},
		{hn.postForm("/account/password", e, ownPasswordForm("x", "a", "b")), "%2Faccount"},
	} {
		mustContain(t, tc.w.Body.String(), "&amp;next="+tc.next+`"`)
	}
}

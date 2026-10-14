package users_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/jorgealonsodev/backsoon/admin/internal/users"
)

const (
	owner    = "Boss"
	password = "correct horse battery"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newClock() *clock { return &clock{t: time.Date(2026, 10, 14, 9, 30, 0, 0, time.UTC)} }

// open opens a store in dir with the cheapest bcrypt cost, except where a test is about the cost.
func open(t *testing.T, dir string, clk *clock) *users.Store {
	t.Helper()
	s, err := users.Open(users.Options{MetaDir: dir, Owner: owner, Now: clk.Now, Cost: bcrypt.MinCost})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func usersFile(dir string) string { return filepath.Join(dir, "users.json") }

func TestOpenWithoutFileMeansNoUsers(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir, newClock())
	if got := s.List(); len(got) != 0 {
		t.Fatalf("List = %v, want none", got)
	}
	if _, err := os.Stat(usersFile(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("opening must not create users.json: %v", err)
	}
	if _, ok := s.Stamp("alice"); ok {
		t.Fatal("unknown user has a stamp")
	}
}

func TestCreateListAndLookup(t *testing.T) {
	clk := newClock()
	s := open(t, t.TempDir(), clk)

	u, err := s.Create("Alice", password)
	if err != nil {
		t.Fatal(err)
	}
	if u.Name != "alice" || !u.Created.Equal(clk.Now()) || !u.PasswordChanged.Equal(clk.Now()) {
		t.Fatalf("created user = %+v", u)
	}
	clk.Advance(time.Hour)
	if _, err := s.Create("bob.2_x-y", password); err != nil {
		t.Fatal(err)
	}

	list := s.List()
	if len(list) != 2 || list[0].Name != "alice" || list[1].Name != "bob.2_x-y" {
		t.Fatalf("List = %+v", list)
	}
	for _, name := range []string{"alice", "ALICE", "Alice"} {
		got, ok := s.Lookup(name)
		if !ok || got != u {
			t.Errorf("Lookup(%q) = %+v, %v", name, got, ok)
		}
	}
	if _, ok := s.Lookup("carol"); ok {
		t.Error("Lookup found a user that does not exist")
	}
}

func TestFileIsPrivateBcryptAndHoldsNoPlaintext(t *testing.T) {
	dir := t.TempDir()
	// Default cost on purpose: S2 asks for bcrypt cost 12.
	s, err := users.Open(users.Options{MetaDir: dir, Owner: owner})
	if err != nil {
		t.Fatal(err)
	}
	const secret = "a very long initial password"
	if _, err := s.Create("alice", secret); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(usersFile(dir))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("users.json mode = %v, want 0600", info.Mode().Perm())
	}
	raw, err := os.ReadFile(usersFile(dir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatal("users.json holds the plaintext password")
	}
	if !strings.Contains(string(raw), `"name": "alice"`) && !strings.Contains(string(raw), `"name":"alice"`) {
		t.Fatalf("users.json does not name the user: %s", raw)
	}
	for _, line := range strings.Split(string(raw), `"`) {
		if strings.HasPrefix(line, "$2") {
			cost, err := bcrypt.Cost([]byte(line))
			if err != nil || cost != 12 {
				t.Fatalf("stored hash cost = %d, %v; want 12", cost, err)
			}
			return
		}
	}
	t.Fatalf("users.json has no bcrypt hash: %s", raw)
}

func TestFileStaysPrivateAfterEveryChange(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir, newClock())
	mode := func() os.FileMode {
		t.Helper()
		info, err := os.Stat(usersFile(dir))
		if err != nil {
			t.Fatal(err)
		}
		return info.Mode().Perm()
	}
	s.Create("alice", password)
	s.Create("bob", password)
	if err := s.SetPassword("alice", "another password"); err != nil {
		t.Fatal(err)
	}
	if mode() != 0o600 {
		t.Fatalf("mode after set password = %v", mode())
	}
	if err := s.Delete("bob"); err != nil {
		t.Fatal(err)
	}
	if mode() != 0o600 {
		t.Fatalf("mode after delete = %v", mode())
	}
}

func TestNameValidation(t *testing.T) {
	s := open(t, t.TempDir(), newClock())
	valid := []string{"a", "B", "alice", "a.b_c-d", "0", strings.Repeat("a", 64), "Bob-9"}
	invalid := []string{
		"", strings.Repeat("a", 65), "al ice", " alice", "alice ", "al\tice", "al/ice", "ali\x00ce",
		"álvaro", "ali@ce", "ali+ce", "名前", "a\nb", "\u212a", "alice\u0301",
	}
	for _, name := range valid {
		if _, err := s.Create(name, password); err != nil {
			t.Errorf("Create(%q) = %v, want ok", name, err)
		}
	}
	for _, name := range invalid {
		if _, err := s.Create(name, password); !errors.Is(err, users.ErrInvalidName) {
			t.Errorf("Create(%q) = %v, want ErrInvalidName", name, err)
		}
	}
	if got := len(s.List()); got != len(valid) {
		t.Fatalf("%d users stored, want %d (only the valid names)", got, len(valid))
	}
}

func TestPasswordValidation(t *testing.T) {
	s := open(t, t.TempDir(), newClock())
	cases := []struct {
		name, password string
		want           error
	}{
		{"nine characters", "123456789", users.ErrPasswordShort},
		{"empty", "", users.ErrPasswordShort},
		{"ten characters", "1234567890", nil},
		{"ten multibyte characters", strings.Repeat("ñ", 10), nil},
		{"nine multibyte characters", strings.Repeat("ñ", 9), users.ErrPasswordShort},
		{"72 bytes", strings.Repeat("a", 72), nil},
		{"73 bytes", strings.Repeat("a", 73), users.ErrPasswordLong},
		{"36 two-byte characters", strings.Repeat("ñ", 36), nil},
		{"37 two-byte characters", strings.Repeat("ñ", 37), users.ErrPasswordLong},
	}
	for i, tc := range cases {
		name := fmt.Sprintf("user%d", i)
		_, err := s.Create(name, tc.password)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: Create = %v, want %v", tc.name, err, tc.want)
		}
		if tc.want != nil {
			if _, ok := s.Lookup(name); ok {
				t.Errorf("%s: a user with a rejected password was stored", tc.name)
			}
		}
	}
	if err := s.SetPassword("user2", "123456789"); !errors.Is(err, users.ErrPasswordShort) {
		t.Errorf("SetPassword short = %v", err)
	}
	if err := s.SetPassword("user2", strings.Repeat("a", 73)); !errors.Is(err, users.ErrPasswordLong) {
		t.Errorf("SetPassword long = %v", err)
	}
}

func TestDuplicatesAndTheOwnerNameAreRejected(t *testing.T) {
	s := open(t, t.TempDir(), newClock())
	if _, err := s.Create("alice", password); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"alice", "Alice", "ALICE"} {
		if _, err := s.Create(name, password); !errors.Is(err, users.ErrExists) {
			t.Errorf("Create(%q) = %v, want ErrExists", name, err)
		}
	}
	for _, name := range []string{"boss", "Boss", "BOSS"} {
		if _, err := s.Create(name, password); !errors.Is(err, users.ErrOwnerName) {
			t.Errorf("Create(%q) = %v, want ErrOwnerName", name, err)
		}
	}
	if got := len(s.List()); got != 1 {
		t.Fatalf("%d users stored, want 1", got)
	}
}

func TestAuthenticate(t *testing.T) {
	s := open(t, t.TempDir(), newClock())
	s.Create("alice", password)
	stamp, _ := s.Stamp("alice")

	name, got, ok := s.Authenticate("Alice", password)
	if !ok || name != "alice" || got != stamp {
		t.Fatalf("Authenticate = %q %q %v; want alice %q true", name, got, ok, stamp)
	}
	for _, tc := range [][2]string{{"alice", "wrong password!"}, {"alice", ""}, {"carol", password}, {"", password}, {"boss", password}} {
		if _, _, ok := s.Authenticate(tc[0], tc[1]); ok {
			t.Errorf("Authenticate(%q, %q) accepted", tc[0], tc[1])
		}
	}
}

func TestSetPasswordChangesTheSessionStamp(t *testing.T) {
	clk := newClock()
	s := open(t, t.TempDir(), clk)
	created, _ := s.Create("alice", password)
	before, _ := s.Stamp("alice")

	clk.Advance(2 * time.Hour)
	if err := s.SetPassword("ALICE", "a brand new password"); err != nil {
		t.Fatal(err)
	}
	after, ok := s.Stamp("alice")
	if !ok || after == before || after == "" {
		t.Fatalf("stamp before %q after %q: must change on set password", before, after)
	}
	u, _ := s.Lookup("alice")
	if !u.Created.Equal(created.Created) || !u.PasswordChanged.Equal(clk.Now()) {
		t.Fatalf("after set password: %+v (created %v)", u, created.Created)
	}
	if _, _, ok := s.Authenticate("alice", password); ok {
		t.Error("the old password still works")
	}
	if _, _, ok := s.Authenticate("alice", "a brand new password"); !ok {
		t.Error("the new password does not work")
	}
	if err := s.SetPassword("carol", password); !errors.Is(err, users.ErrNotFound) {
		t.Errorf("SetPassword unknown = %v, want ErrNotFound", err)
	}
}

func TestDeleteRemovesTheUserAndARecreatedUserGetsANewStamp(t *testing.T) {
	s := open(t, t.TempDir(), newClock())
	s.Create("alice", password)
	s.Create("bob", password)
	old, _ := s.Stamp("alice")

	if err := s.Delete("Alice"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Stamp("alice"); ok {
		t.Fatal("a deleted user still has a stamp")
	}
	if _, _, ok := s.Authenticate("alice", password); ok {
		t.Fatal("a deleted user can authenticate")
	}
	if err := s.Delete("alice"); !errors.Is(err, users.ErrNotFound) {
		t.Fatalf("second Delete = %v, want ErrNotFound", err)
	}
	if got := s.List(); len(got) != 1 || got[0].Name != "bob" {
		t.Fatalf("List = %+v", got)
	}

	s.Create("alice", password)
	fresh, ok := s.Stamp("alice")
	if !ok || fresh == old {
		t.Fatalf("a recreated user reuses the stamp %q: old sessions would come back", old)
	}
}

func TestRestartKeepsUsersStampsAndRevocations(t *testing.T) {
	dir := t.TempDir()
	clk := newClock()
	s := open(t, dir, clk)
	s.Create("alice", password)
	s.Create("bob", password)
	s.Create("carol", password)
	s.SetPassword("bob", "bob has a new password")
	s.Delete("carol")
	aliceStamp, _ := s.Stamp("alice")
	bobStamp, _ := s.Stamp("bob")

	again := open(t, dir, clk)
	if got := again.List(); len(got) != 2 || got[0].Name != "alice" || got[1].Name != "bob" {
		t.Fatalf("after restart List = %+v", got)
	}
	if got, _ := again.Stamp("alice"); got != aliceStamp {
		t.Errorf("alice's stamp changed over a restart: %q -> %q", aliceStamp, got)
	}
	if got, _ := again.Stamp("bob"); got != bobStamp {
		t.Errorf("bob's stamp changed over a restart: %q -> %q", bobStamp, got)
	}
	if _, ok := again.Stamp("carol"); ok {
		t.Error("a deleted user came back after a restart")
	}
	if _, _, ok := again.Authenticate("bob", "bob has a new password"); !ok {
		t.Error("bob's new password does not survive a restart")
	}
	if _, _, ok := again.Authenticate("bob", password); ok {
		t.Error("bob's old password works after a restart")
	}
	u, _ := again.Lookup("alice")
	if !u.Created.Equal(clk.Now()) {
		t.Errorf("created time lost over a restart: %v", u.Created)
	}
}

func TestOpenRefusesACorruptOrInvalidFile(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	entry := func(name, hash, epoch string) string {
		return fmt.Sprintf(`{"name":%q,"hash":%q,"epoch":%q,"created":"2026-10-14T09:30:00Z","password_changed":"2026-10-14T09:30:00Z"}`,
			name, hash, epoch)
	}
	const epoch = "00112233445566778899aabbccddeeff"
	file := func(entries ...string) string {
		return `{"version":1,"users":[` + strings.Join(entries, ",") + `]}`
	}
	cases := map[string]string{
		"not json":          "{this is not json",
		"empty file":        "",
		"unknown version":   `{"version":2,"users":[]}`,
		"no version":        `{"users":[]}`,
		"trailing garbage":  file() + "x",
		"uppercase name":    file(entry("Alice", string(hash), epoch)),
		"invalid name":      file(entry("al ice", string(hash), epoch)),
		"empty name":        file(entry("", string(hash), epoch)),
		"duplicate name":    file(entry("alice", string(hash), epoch), entry("alice", string(hash), epoch)),
		"hash not bcrypt":   file(entry("alice", "plaintext-password", epoch)),
		"empty hash":        file(entry("alice", "", epoch)),
		"missing epoch":     file(entry("alice", string(hash), "")),
		"bad time":          strings.Replace(file(entry("alice", string(hash), epoch)), "2026-10-14T09:30:00Z", "yesterday", 1),
		"user list is null": `{"version":1,"users":null}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(usersFile(dir), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := users.Open(users.Options{MetaDir: dir, Owner: owner, Cost: bcrypt.MinCost})
			if err == nil {
				t.Fatal("Open accepted the file")
			}
			if !strings.Contains(err.Error(), "users.json") {
				t.Errorf("error does not name the file: %v", err)
			}
			if strings.Contains(err.Error(), string(hash)) || strings.Contains(err.Error(), "plaintext-password") {
				t.Errorf("error leaks a stored value: %v", err)
			}
		})
	}
}

func TestOpenAcceptsAValidFileAndKeepsAShadowedOwnerNameListed(t *testing.T) {
	hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	dir := t.TempDir()
	content := fmt.Sprintf(`{"version":1,"users":[{"name":"boss","hash":%q,"epoch":"00112233445566778899aabbccddeeff","created":"2026-10-14T09:30:00Z","password_changed":"2026-10-14T09:30:00Z"}]}`, hash)
	if err := os.WriteFile(usersFile(dir), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	s := open(t, dir, newClock())
	// The owner was renamed in the stack variables after this user existed: the entry stays so the
	// owner can still list and delete it.
	if _, ok := s.Lookup("boss"); !ok {
		t.Fatal("an entry equal to the owner's name must still be listed")
	}
	if err := s.Delete("boss"); err != nil {
		t.Fatal(err)
	}
}

func TestAFailedWriteChangesNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "meta")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	s := open(t, dir, newClock())
	if _, err := s.Create("alice", password); err != nil {
		t.Fatal(err)
	}
	stamp, _ := s.Stamp("alice")

	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create("bob", password); err == nil {
		t.Error("Create succeeded without being able to persist")
	}
	if err := s.SetPassword("alice", "another password"); err == nil {
		t.Error("SetPassword succeeded without being able to persist")
	}
	if err := s.Delete("alice"); err == nil {
		t.Error("Delete succeeded without being able to persist")
	}
	if got, _ := s.Stamp("alice"); got != stamp {
		t.Error("a failed write changed the stamp")
	}
	if _, _, ok := s.Authenticate("alice", password); !ok {
		t.Error("a failed SetPassword changed the password in memory")
	}
	if _, ok := s.Lookup("bob"); ok {
		t.Error("a failed Create left the user in memory")
	}
}

func TestConcurrentUse(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir, newClock())
	const n = 12
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := fmt.Sprintf("user%d", i)
			if _, err := s.Create(name, password); err != nil {
				t.Errorf("Create(%s): %v", name, err)
			}
			s.Authenticate(name, password)
			s.List()
			s.Stamp(name)
			if i%2 == 0 {
				if err := s.SetPassword(name, "second password"); err != nil {
					t.Errorf("SetPassword(%s): %v", name, err)
				}
			}
		}()
	}
	// Racing creations of one name: exactly one wins.
	wins := make(chan bool, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Create("same", password)
			wins <- err == nil
		}()
	}
	wg.Wait()
	close(wins)
	won := 0
	for w := range wins {
		if w {
			won++
		}
	}
	if won != 1 {
		t.Fatalf("%d concurrent creations of one name succeeded, want 1", won)
	}
	if got := len(s.List()); got != n+1 {
		t.Fatalf("%d users, want %d", got, n+1)
	}
	// What is on disk is what is in memory.
	if got := len(open(t, dir, newClock()).List()); got != n+1 {
		t.Fatalf("%d users after reopening, want %d", got, n+1)
	}
}

package users

import (
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// An unknown user costs the same single bcrypt comparison as a known one, so response time does
// not reveal which names exist (S5).
func TestAuthenticateComparesOnceForKnownAndUnknownUsers(t *testing.T) {
	var compared int
	orig := compareHash
	compareHash = func(hash, pw []byte) error {
		compared++
		return orig(hash, pw)
	}
	t.Cleanup(func() { compareHash = orig })

	s, err := Open(Options{MetaDir: t.TempDir(), Owner: "boss", Cost: bcrypt.MinCost})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create("alice", "correct horse battery"); err != nil {
		t.Fatal(err)
	}

	cases := []struct{ user, password string }{
		{"alice", "correct horse battery"}, // known, right
		{"alice", "wrong password!"},       // known, wrong
		{"carol", "correct horse battery"}, // unknown
		{"", ""},                           // empty
		{"not a valid name", "x"},          // invalid name
	}
	for _, tc := range cases {
		compared = 0
		s.Authenticate(tc.user, tc.password)
		if compared != 1 {
			t.Errorf("Authenticate(%q) ran %d bcrypt comparisons, want 1", tc.user, compared)
		}
	}
}

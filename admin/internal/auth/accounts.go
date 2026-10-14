package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// UserStore is the set of added users, as Accounts needs it (implemented by users.Store).
type UserStore interface {
	// Stamp returns the current session stamp of the user called name.
	Stamp(name string) (stamp string, ok bool)
	// Authenticate checks a sign-in and returns the canonical name and the current stamp. It must
	// make one password comparison also when the user does not exist.
	Authenticate(name, password string) (canonical, stamp string, ok bool)
}

// Accounts is everybody who may sign in: the owner, configured through the stack variables, and
// the users in the store. It is the Directory of the session Manager.
type Accounts struct {
	owner      *Credentials
	ownerName  string
	ownerStamp string
	users      UserStore
}

// NewAccounts builds the accounts of the owner called ownerName with the given bcrypt hash.
// fingerprint identifies the configured owner password (config.Config.PasswordFingerprint; the
// hash itself when empty) and, with secret, derives the owner's session stamp: change the
// password in the stack variables and the owner's sessions end. users may be nil.
func NewAccounts(ownerName string, ownerHash, fingerprint, secret []byte, users UserStore) *Accounts {
	if len(fingerprint) == 0 {
		fingerprint = ownerHash
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("owner-stamp"))
	mac.Write([]byte{0})
	mac.Write(fingerprint)
	return &Accounts{
		owner:      NewCredentials(ownerName, ownerHash),
		ownerName:  ownerName,
		ownerStamp: hex.EncodeToString(mac.Sum(nil)[:16]),
		users:      users,
	}
}

// Authenticate checks a sign-in. The owner's name is exact; stored users' names are case-
// insensitive. Whoever the name belongs to, exactly one bcrypt comparison is made, so the time
// taken does not reveal whether a user exists.
func (a *Accounts) Authenticate(user, password string) (Identity, bool) {
	if a.owner.MatchesUser(user) {
		if !a.owner.Check(user, password) {
			return Identity{}, false
		}
		return Identity{User: a.ownerName, Stamp: a.ownerStamp, Owner: true}, true
	}
	if a.users == nil {
		// Keep the cost of a failed sign-in the same without a store.
		a.owner.Check(user, password)
		return Identity{}, false
	}
	name, stamp, ok := a.users.Authenticate(user, password)
	if !ok {
		return Identity{}, false
	}
	return Identity{User: name, Stamp: stamp}, true
}

// Resolve implements Directory. The owner wins over a stored entry with the same name.
func (a *Accounts) Resolve(user string) (stamp string, owner, ok bool) {
	if user == a.ownerName {
		return a.ownerStamp, true, true
	}
	if a.users == nil {
		return "", false, false
	}
	stamp, ok = a.users.Stamp(user)
	return stamp, false, ok
}

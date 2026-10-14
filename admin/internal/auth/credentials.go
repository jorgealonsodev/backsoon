// Package auth implements the panel's authentication primitives: credential checks, signed
// stateless sessions, CSRF tokens and the login rate limiter. It has no HTTP handlers.
package auth

import (
	"crypto/sha256"
	"crypto/subtle"

	"golang.org/x/crypto/bcrypt"
)

// Credentials verifies the owner's user name and password.
type Credentials struct {
	userDigest [sha256.Size]byte
	hash       []byte
}

// NewCredentials builds a checker from the user name and a bcrypt hash (see config.Config).
func NewCredentials(user string, bcryptHash []byte) *Credentials {
	return &Credentials{userDigest: sha256.Sum256([]byte(user)), hash: bcryptHash}
}

// MatchesUser reports, in constant time, whether user is the owner's name.
func (c *Credentials) MatchesUser(user string) bool {
	digest := sha256.Sum256([]byte(user))
	return subtle.ConstantTimeCompare(digest[:], c.userDigest[:]) == 1
}

// Check reports whether user and password match. The user name is compared in constant time
// and the bcrypt comparison always runs, so response time does not reveal which part was wrong.
func (c *Credentials) Check(user, password string) bool {
	userOK := c.MatchesUser(user)
	passOK := bcrypt.CompareHashAndPassword(c.hash, []byte(password)) == nil
	return userOK && passOK
}

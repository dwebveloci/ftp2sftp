package auth

import (
	"fmt"

	"golang.org/x/crypto/bcrypt"

	errs "github.com/Dmn117/ftp2sftp/internal/errors"
)

// dummyHash is compared against on every lookup of an unknown username, so
// that authentication takes a comparable amount of CPU time whether or not
// the username exists. This does not make timing fully constant (map
// lookups and network jitter dominate over a bcrypt compare in practice)
// but avoids the cheapest, most obvious enumeration signal: skipping
// bcrypt entirely for unknown users.
var dummyHash = mustHash("ftp2sftp-dummy-comparison-only")

func mustHash(s string) []byte {
	h, err := bcrypt.GenerateFromPassword([]byte(s), bcrypt.DefaultCost)
	if err != nil {
		panic(fmt.Sprintf("auth: failed to precompute dummy hash: %v", err))
	}

	return h
}

// ErrInvalidCredentials is the single, generic error returned for any
// authentication failure (unknown user, wrong password, rate-limited). Its
// message is intentionally identical across all failure reasons.
var ErrInvalidCredentials = errs.New(errs.KindAuthentication, "auth.Authenticate", "credenciales inválidas")

// ErrRateLimited is returned when a client or username has exceeded the
// allowed number of failed attempts and is temporarily locked out.
var ErrRateLimited = errs.New(errs.KindRateLimited, "auth.Authenticate", "demasiados intentos fallidos, intente más tarde")

// Authenticator validates FTP username/password pairs against a Store,
// applying brute-force protection keyed by client IP and by username.
type Authenticator struct {
	store  *Store
	byIP   *Limiter
	byUser *Limiter
}

// NewAuthenticator builds an Authenticator. byIP and byUser are independent
// rate limiters; an attempt is only allowed when both permit it.
func NewAuthenticator(store *Store, byIP, byUser *Limiter) *Authenticator {
	return &Authenticator{store: store, byIP: byIP, byUser: byUser}
}

// Authenticate validates username/password for a connection originating
// from clientIP. It never distinguishes "unknown user" from "wrong
// password" in its returned error, and never logs the password.
func (a *Authenticator) Authenticate(clientIP, username, password string) error {
	if !a.byIP.Allow(clientIP) || !a.byUser.Allow(username) {
		return ErrRateLimited
	}

	record, found := a.store.Lookup(username)

	hash := dummyHash
	if found {
		hash = []byte(record.PasswordHash)
	}

	err := bcrypt.CompareHashAndPassword(hash, []byte(password))

	if !found || err != nil {
		a.byIP.RecordFailure(clientIP)
		a.byUser.RecordFailure(username)

		return ErrInvalidCredentials
	}

	a.byIP.RecordSuccess(clientIP)
	a.byUser.RecordSuccess(username)

	return nil
}

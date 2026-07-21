// Package auth validates FTP control-channel credentials against a static,
// bcrypt-hashed user store and protects it against brute-force guessing.
//
// It never logs a password, never reveals whether a username exists, and
// never returns a differentiated error for "unknown user" versus "wrong
// password" (RF-002: impedir enumeración evidente de usuarios).
package auth

// UserRecord is the authentication-relevant subset of a configured user.
type UserRecord struct {
	Username     string
	PasswordHash string
}

// Store is a read-only lookup of configured FTP users, keyed by username.
type Store struct {
	users map[string]UserRecord
}

// NewStore builds a Store from the given records.
func NewStore(records []UserRecord) *Store {
	users := make(map[string]UserRecord, len(records))
	for _, r := range records {
		users[r.Username] = r
	}

	return &Store{users: users}
}

// Lookup returns the record for username, if configured.
func (s *Store) Lookup(username string) (UserRecord, bool) {
	r, ok := s.users[username]

	return r, ok
}

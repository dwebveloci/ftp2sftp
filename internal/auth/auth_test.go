package auth_test

import (
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/Dmn117/ftp2sftp/internal/auth"
)

func newTestAuthenticator(t *testing.T, username, password string) *auth.Authenticator {
	t.Helper()

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}

	store := auth.NewStore([]auth.UserRecord{{Username: username, PasswordHash: string(hash)}})
	byIP := auth.NewLimiter(3, time.Minute, 100*time.Millisecond)
	byUser := auth.NewLimiter(3, time.Minute, 100*time.Millisecond)

	return auth.NewAuthenticator(store, byIP, byUser)
}

func TestAuthenticateSuccess(t *testing.T) {
	a := newTestAuthenticator(t, "ax2012", "correct-horse")

	if err := a.Authenticate("10.0.0.1", "ax2012", "correct-horse"); err != nil {
		t.Fatalf("Authenticate() with correct credentials failed: %v", err)
	}
}

func TestAuthenticateWrongPassword(t *testing.T) {
	a := newTestAuthenticator(t, "ax2012", "correct-horse")

	err := a.Authenticate("10.0.0.1", "ax2012", "wrong")
	if err == nil {
		t.Fatal("Authenticate() with wrong password should fail")
	}

	if err != auth.ErrInvalidCredentials {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestAuthenticateUnknownUserSameErrorAsWrongPassword(t *testing.T) {
	a := newTestAuthenticator(t, "ax2012", "correct-horse")

	errUnknown := a.Authenticate("10.0.0.1", "does-not-exist", "whatever")
	errWrong := a.Authenticate("10.0.0.2", "ax2012", "wrong")

	if errUnknown.Error() != errWrong.Error() {
		t.Fatalf("unknown user and wrong password must produce identical error text: %q vs %q",
			errUnknown.Error(), errWrong.Error())
	}
}

func TestAuthenticateLocksOutAfterRepeatedFailures(t *testing.T) {
	a := newTestAuthenticator(t, "ax2012", "correct-horse")

	for i := 0; i < 3; i++ {
		_ = a.Authenticate("10.0.0.1", "ax2012", "wrong")
	}

	err := a.Authenticate("10.0.0.1", "ax2012", "correct-horse")
	if err != auth.ErrRateLimited {
		t.Fatalf("expected ErrRateLimited after repeated failures, got %v", err)
	}
}

func TestAuthenticateLockoutIsPerKeyNotGlobal(t *testing.T) {
	a := newTestAuthenticator(t, "ax2012", "correct-horse")

	for i := 0; i < 3; i++ {
		_ = a.Authenticate("10.0.0.1", "ax2012", "wrong")
	}

	// A different source IP for the same username is still rate-limited
	// because the per-username limiter also tripped.
	if err := a.Authenticate("10.0.0.9", "ax2012", "correct-horse"); err != auth.ErrRateLimited {
		t.Fatalf("expected per-username lockout to also apply, got %v", err)
	}
}

func TestAuthenticateSuccessResetsFailureCounter(t *testing.T) {
	a := newTestAuthenticator(t, "ax2012", "correct-horse")

	_ = a.Authenticate("10.0.0.1", "ax2012", "wrong")
	_ = a.Authenticate("10.0.0.1", "ax2012", "wrong")

	if err := a.Authenticate("10.0.0.1", "ax2012", "correct-horse"); err != nil {
		t.Fatalf("Authenticate() with correct credentials should succeed before lockout threshold: %v", err)
	}

	// After a success, failure count is reset; two more failures should
	// not yet trigger the lockout that requires three.
	_ = a.Authenticate("10.0.0.1", "ax2012", "wrong")
	err := a.Authenticate("10.0.0.1", "ax2012", "wrong")

	if err == auth.ErrRateLimited {
		t.Fatal("failure counter should have reset after a success")
	}
}

package auth_test

import (
	"testing"
	"time"

	"github.com/Dmn117/ftp2sftp/internal/auth"
)

func TestLimiterAllowsUntilThreshold(t *testing.T) {
	l := auth.NewLimiter(3, time.Minute, time.Hour)

	for i := 0; i < 2; i++ {
		if !l.Allow("k") {
			t.Fatalf("Allow() should be true before reaching maxFailures (iteration %d)", i)
		}

		l.RecordFailure("k")
	}

	if !l.Allow("k") {
		t.Fatal("Allow() should still be true right before the third failure")
	}
}

func TestLimiterLocksAfterThreshold(t *testing.T) {
	l := auth.NewLimiter(2, time.Minute, time.Hour)

	l.RecordFailure("k")
	l.RecordFailure("k")

	if l.Allow("k") {
		t.Fatal("Allow() should be false once maxFailures is reached")
	}
}

func TestLimiterLockoutExpires(t *testing.T) {
	l := auth.NewLimiter(1, time.Minute, 20*time.Millisecond)

	l.RecordFailure("k")

	if l.Allow("k") {
		t.Fatal("Allow() should be false immediately after lockout")
	}

	time.Sleep(40 * time.Millisecond)

	if !l.Allow("k") {
		t.Fatal("Allow() should be true after the lockout window expires")
	}
}

func TestLimiterRecordSuccessClearsState(t *testing.T) {
	l := auth.NewLimiter(1, time.Minute, time.Hour)

	l.RecordFailure("k")
	l.RecordSuccess("k")

	if !l.Allow("k") {
		t.Fatal("Allow() should be true after RecordSuccess clears the lockout")
	}
}

func TestLimiterKeysAreIndependent(t *testing.T) {
	l := auth.NewLimiter(1, time.Minute, time.Hour)

	l.RecordFailure("a")

	if l.Allow("a") {
		t.Fatal("key a should be locked")
	}

	if !l.Allow("b") {
		t.Fatal("key b should be unaffected by key a's lockout")
	}
}

func TestLimiterConcurrentUse(t *testing.T) {
	l := auth.NewLimiter(1000, time.Minute, time.Second)

	done := make(chan struct{})

	for i := 0; i < 50; i++ {
		go func(n int) {
			defer func() { done <- struct{}{} }()

			key := "concurrent"
			l.Allow(key)
			l.RecordFailure(key)
		}(i)
	}

	for i := 0; i < 50; i++ {
		<-done
	}
}

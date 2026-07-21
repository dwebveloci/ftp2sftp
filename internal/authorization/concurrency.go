package authorization

// ConcurrencyGate bounds the number of simultaneous transfers a single user
// may have in flight. It is a non-blocking semaphore: TryAcquire never
// waits, so a session over the limit gets an immediate FTP error instead of
// stalling the control channel.
type ConcurrencyGate struct {
	slots chan struct{}
}

// NewConcurrencyGate creates a gate allowing up to max concurrent
// transfers. max must be greater than 0 (enforced by config validation).
func NewConcurrencyGate(max int) *ConcurrencyGate {
	return &ConcurrencyGate{slots: make(chan struct{}, max)}
}

// TryAcquire reserves a slot and reports whether it succeeded.
func (g *ConcurrencyGate) TryAcquire() bool {
	select {
	case g.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

// Release frees a previously acquired slot. Calling it without a matching
// successful TryAcquire is a no-op.
func (g *ConcurrencyGate) Release() {
	select {
	case <-g.slots:
	default:
	}
}

// InUse returns the number of slots currently held. Intended for metrics.
func (g *ConcurrencyGate) InUse() int {
	return len(g.slots)
}

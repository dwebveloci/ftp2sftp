package observability

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync/atomic"
	"time"
)

var fallbackCounter atomic.Uint64

// NewCorrelationID returns a random hex identifier suitable for session and
// transfer correlation (RF-015). It is generated with crypto/rand rather
// than a counter so identifiers stay unique across process restarts,
// keeping the RF-007 temporary file name ("<file>.part-<sessionID>")
// collision-free even after a crash and restart.
//
// This is called on every new FTP session and every transfer, so a
// transient entropy-source failure must not crash the whole process (and
// every other in-flight session) over one ID: it falls back to a
// timestamp-plus-counter identifier instead. That fallback is unique but
// not unpredictable, which is fine here — these IDs are correlation keys
// for logs/audit and a filename suffix, never a security token.
func NewCorrelationID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err == nil {
		return hex.EncodeToString(buf)
	}

	return fmt.Sprintf("f%x%x", time.Now().UnixNano(), fallbackCounter.Add(1))
}

package protocol

import (
	"bufio"
	"net"
	"strings"
	"testing"
	"time"
)

// TestAuthTLSIsRejectedWithoutCrashing is a regression test for a
// pre-auth, unauthenticated denial-of-service: ftpserverlib's AUTH command
// handler only takes its error branch when driver.GetTLSConfig() returns
// a non-nil error; if it returns (nil, nil) — which internal/ftpserver
// used to do, reasoning that FTPS was simply "unreachable" since
// TLSRequired was never set to ImplicitEncryption — the library
// unconditionally does tls.Server(conn, nilConfig) and the next read
// panics inside crypto/tls, crashing the whole process (every other
// in-flight FTP session included, since a panic in one goroutine takes
// down the entire Go program unless recovered).
//
// Many real clients, including FileZilla with its default site
// encryption setting, send "AUTH TLS" automatically before login, so this
// was reachable pre-auth by any client on the network, not just a
// deliberate attacker.
func TestAuthTLSIsRejectedWithoutCrashing(t *testing.T) {
	gw := startGateway(t, nil)

	conn, err := net.DialTimeout("tcp", gw.Addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	reader := bufio.NewReader(conn)

	banner, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("reading banner: %v", err)
	}

	if !strings.HasPrefix(banner, "220") {
		t.Fatalf("expected a 220 banner, got %q", banner)
	}

	if _, err := conn.Write([]byte("AUTH TLS\r\n")); err != nil {
		t.Fatalf("writing AUTH TLS: %v", err)
	}

	resp, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("reading AUTH TLS response (the bug this guards against reads as a connection reset/EOF here): %v", err)
	}

	// Any 5xx rejection is acceptable; what matters is that it is a plain
	// FTP reply, not an upgrade to a (broken) TLS connection.
	if !strings.HasPrefix(resp, "5") {
		t.Fatalf("expected AUTH TLS to be rejected with a 5xx reply, got %q", resp)
	}

	// The real assertion: the gateway process must still be alive and
	// able to serve an unrelated, fully normal session afterward.
	c := loginOrFail(t, gw)
	defer c.Quit()

	if _, err := c.CurrentDir(); err != nil {
		t.Fatalf("gateway did not survive AUTH TLS: PWD after it failed: %v", err)
	}
}

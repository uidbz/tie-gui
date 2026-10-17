package httpx

import (
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/uidbz/tie/client"
)

// TestTimeoutsAreSet is the regression for review §3.6: tie's HTTPClientFor
// sets no deadline, so a filehost that accepts the connection and never
// answers parked the gallery loader worker or the ops goroutine forever, with
// no error and no way out short of killing the app.
func TestTimeoutsAreSet(t *testing.T) {
	host := client.FileHost{URL: "http://127.0.0.1:1", Store: "default"}

	for _, tc := range []struct {
		name string
		c    *http.Client
		want time.Duration
	}{
		{"Small", Small(host), SmallTimeout},
		{"Bulk", Bulk(host), BulkTimeout},
		{"WithTimeout", WithTimeout(host, 3 * time.Second), 3 * time.Second},
	} {
		if tc.c.Timeout != tc.want {
			t.Errorf("%s: Timeout = %v, want %v", tc.name, tc.c.Timeout, tc.want)
		}
		if tc.c.Transport == nil {
			t.Errorf("%s: no transport", tc.name)
		}
	}
	if SmallTimeout >= BulkTimeout {
		t.Errorf("SmallTimeout %v should be shorter than BulkTimeout %v", SmallTimeout, BulkTimeout)
	}
}

// TestSharesTieTransport pins the pooling half of the finding: adding a
// deadline must not cost a fresh connection pool per call, or every thumbnail
// would pay a TLS handshake again.
func TestSharesTieTransport(t *testing.T) {
	host := client.FileHost{URL: "http://127.0.0.1:1", Store: "default"}
	tie := client.HTTPClientFor(host)
	if got := Small(host).Transport; got != tie.Transport {
		t.Error("Small built its own transport instead of reusing tie's")
	}
	if got := Bulk(host).Transport; got != tie.Transport {
		t.Error("Bulk built its own transport instead of reusing tie's")
	}
	// Same host, twice: the transport identity is what makes reuse work.
	if a, b := Small(host).Transport, Bulk(host).Transport; a != b {
		t.Error("two clients for one host got different transports")
	}
}

// TestZeroTimeoutPassesThrough documents the escape hatch: a caller that wants
// tie's untimed client gets tie's own client, not a copy.
func TestZeroTimeoutPassesThrough(t *testing.T) {
	host := client.FileHost{URL: "http://127.0.0.1:1"}
	if got := WithTimeout(host, 0); got != client.HTTPClientFor(host) {
		t.Error("WithTimeout(0) should return tie's client unchanged")
	}
}

// TestRequestAbandonsDeadHost is the behaviour the timeouts buy: a listener
// that accepts and never replies must produce an error, not a hang.
func TestRequestAbandonsDeadHost(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("cannot open a listener:", err)
	}
	defer ln.Close()
	// Accept and then sit on the connection: the classic half-dead server.
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c // never answered, never closed
		}
	}()

	host := client.FileHost{URL: "http://" + ln.Addr().String()}
	c := WithTimeout(host, 300*time.Millisecond)

	done := make(chan error, 1)
	go func() {
		resp, err := c.Get(host.URL + "/blob")
		if err == nil {
			resp.Body.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a server that never answered produced no error")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the request outlived its timeout")
	}
}

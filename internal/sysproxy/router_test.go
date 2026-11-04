package sysproxy

import (
	"context"
	"crypto/tls"
	"net"
	"strings"
	"testing"
	"time"
)

// clientHello returns the first TLS record a crypto/tls client sends for name.
func clientHello(t *testing.T, name string) (string, []byte) {
	t.Helper()
	c1, c2 := net.Pipe()
	defer c2.Close()
	go func() {
		_ = tls.Client(c1, &tls.Config{ServerName: name}).Handshake()
	}()
	_ = c2.SetReadDeadline(time.Now().Add(5 * time.Second))
	sni, raw, err := ReadHello(c2)
	if err != nil {
		t.Fatalf("read hello: %v", err)
	}
	_ = c1.Close()
	return sni, raw
}

func TestParseSNIFromRealClientHello(t *testing.T) {
	sni, raw := clientHello(t, "API.Example.com")
	if sni != "api.example.com" {
		t.Fatalf("sni = %q", sni)
	}
	if len(raw) < 6 || raw[0] != 0x16 {
		t.Fatalf("raw does not start with a handshake record: %x", raw[:6])
	}
	// An IP literal is never sent as SNI.
	if sni, _ := clientHello(t, "127.0.0.1"); sni != "" {
		t.Fatalf("IP address sent as SNI: %q", sni)
	}
}

func TestParseSNIRejectsGarbage(t *testing.T) {
	for _, rec := range [][]byte{nil, {0x01}, {0x02, 0, 0, 0}, {0x01, 0, 0, 200}} {
		if _, ok := ParseSNI(rec); ok {
			t.Fatalf("garbage %x accepted", rec)
		}
	}
}

func TestReadHelloNonTLSIsReplayable(t *testing.T) {
	in := []byte("GET / HTTP/1.1\r\nHost: x\r\n\r\n")
	sni, raw, err := ReadHello(strings.NewReader(string(in)))
	if err != nil || sni != "" {
		t.Fatalf("non-TLS: sni=%q err=%v", sni, err)
	}
	if string(raw) != string(in[:5]) {
		t.Fatalf("raw = %q", raw)
	}
}

// TestRouterDecidesByName: intercepted names go to MITM, everything else
// (other names, no SNI) is blind-tunnelled to the original destination.
func TestRouterDecidesByName(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("loopback unavailable: %v", err)
	}
	defer ln.Close()

	type hit struct{ kind, host string }
	hits := make(chan hit, 8)
	rt := &Router{
		IsMITM: func(h string) bool { return h == "api.example.com" },
		MITM: func(_ context.Context, c net.Conn, host string, raw []byte) error {
			hits <- hit{"mitm", host}
			return nil
		},
		Blind: func(_ context.Context, c net.Conn, dst string, raw []byte) error {
			hits <- hit{"blind:" + dst, ""}
			return nil
		},
		OrigDst:      func(net.Conn) (string, error) { return "203.0.113.9:443", nil },
		HelloTimeout: 5 * time.Second,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = rt.Serve(ctx, ln) }()

	dial := func(name string) {
		c, err := net.Dial("tcp4", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			defer c.Close()
			_ = tls.Client(c, &tls.Config{ServerName: name}).Handshake()
		}()
	}
	dial("api.example.com")
	dial("other.example")
	dial("127.0.0.1")

	// Connections are handled concurrently, so count the routes instead of
	// assuming an order.
	counts := map[hit]int{}
	for i := 0; i < 3; i++ {
		select {
		case h := <-hits:
			counts[h]++
		case <-time.After(5 * time.Second):
			t.Fatalf("connection %d was not routed; got so far %v", i, counts)
		}
	}
	if counts[hit{"mitm", "api.example.com"}] != 1 || counts[hit{"blind:203.0.113.9:443", ""}] != 2 {
		t.Fatalf("routes = %v", counts)
	}
}

// TestRouterRefusesLoopbackAndNon443: a connection whose original destination
// is the listener itself, or any loopback or non-443 address, is never relayed.
func TestRouterRefusesLoopbackAndNon443(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("loopback unavailable: %v", err)
	}
	defer ln.Close()
	self := ln.Addr().String()
	for _, dst := range []string{self, "127.0.0.2:443", "203.0.113.9:8443", "0.0.0.0:443"} {
		called := make(chan string, 4)
		rt := &Router{
			IsMITM: func(string) bool { return true },
			MITM: func(_ context.Context, _ net.Conn, host string, _ []byte) error {
				called <- "mitm"
				return nil
			},
			Blind: func(_ context.Context, _ net.Conn, _ string, _ []byte) error {
				called <- "blind"
				return nil
			},
			OrigDst:      func(net.Conn) (string, error) { return dst, nil },
			HelloTimeout: 2 * time.Second,
		}
		ctx, cancel := context.WithCancel(context.Background())
		go func() { _ = rt.Serve(ctx, ln) }()
		c, err := net.Dial("tcp4", self)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		go func() { _ = tls.Client(c, &tls.Config{ServerName: "api.example.com"}).Handshake() }()
		select {
		case what := <-called:
			t.Fatalf("destination %s was relayed (%s)", dst, what)
		case <-time.After(300 * time.Millisecond):
		}
		_ = c.Close()
		cancel()
	}
}

// TestRouterPanicIsReported: a panic in a handler is passed to OnPanic and does
// not escape the goroutine.
func TestRouterPanicIsReported(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("loopback unavailable: %v", err)
	}
	defer ln.Close()
	reported := make(chan any, 1)
	rt := &Router{
		OrigDst:      func(net.Conn) (string, error) { return "203.0.113.9:443", nil },
		Blind:        func(context.Context, net.Conn, string, []byte) error { panic("boom") },
		OnPanic:      func(v any) { reported <- v },
		HelloTimeout: 2 * time.Second,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = rt.Serve(ctx, ln) }()
	c, err := net.Dial("tcp4", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, _ = c.Write([]byte("GET / HTTP/1.1\r\n\r\n"))
	select {
	case v := <-reported:
		if v != "boom" {
			t.Fatalf("reported %v", v)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("panic not reported")
	}
}

// helloBytes returns the SNI and first record of a crypto/tls ClientHello.
func helloBytes(name string) (string, []byte) {
	c1, c2 := net.Pipe()
	defer c2.Close()
	go func() { _ = tls.Client(c1, &tls.Config{ServerName: name}).Handshake() }()
	_ = c2.SetReadDeadline(time.Now().Add(5 * time.Second))
	sni, raw, _ := ReadHello(c2)
	_ = c1.Close()
	return sni, raw
}

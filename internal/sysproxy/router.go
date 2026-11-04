package sysproxy

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"sync"
	"time"
)

// Router handles the connections the redirect delivers. It reads the TLS
// ClientHello to learn the SNI and then either hands the connection to the MITM
// path (SNI in the allowlist) or blind-tunnels it to its original destination.
// Connections without an SNI are tunnelled too, never intercepted.
type Router struct {
	// IsMITM reports whether host is intercepted.
	IsMITM func(host string) bool
	// MITM serves an intercepted connection; raw holds the bytes already read.
	MITM func(ctx context.Context, c net.Conn, host string, raw []byte) error
	// Blind tunnels an unintercepted connection to dst.
	Blind func(ctx context.Context, c net.Conn, dst string, raw []byte) error
	// OrigDst returns the original destination. Nil means OriginalDst.
	OrigDst func(c net.Conn) (string, error)
	// HelloTimeout bounds the wait for the ClientHello. Zero means 10s.
	HelloTimeout time.Duration
	// OnPanic receives a panic from a connection handler. The connection is
	// closed, and the caller decides how the run ends (transparent mode ends
	// the run, so its rules are restored).
	OnPanic func(v any)
}

// refusedDestination reports whether a redirected connection must not be
// relayed. The redirect only matches TCP 443 to public addresses, so a
// loopback or non-443 original destination means the connection did not come
// from the rule. Relaying it could loop back into this listener.
func refusedDestination(dst string) bool {
	ap, err := netip.ParseAddrPort(dst)
	if err != nil {
		return true
	}
	return ap.Port() != 443 || ap.Addr().IsLoopback() || ap.Addr().IsUnspecified()
}

// Serve accepts until ln is closed or ctx is done. Each connection is handled in
// its own goroutine; open tunnels outlive Serve and end when their peers close.
func (rt *Router) Serve(ctx context.Context, ln net.Listener) error {
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return err
		}
		go rt.handle(ctx, c)
	}
}

func (rt *Router) handle(ctx context.Context, c net.Conn) {
	defer c.Close()
	defer func() {
		if v := recover(); v != nil && rt.OnPanic != nil {
			rt.OnPanic(v)
		}
	}()
	orig := rt.OrigDst
	if orig == nil {
		orig = OriginalDst
	}
	dst, err := orig(c)
	if err != nil || refusedDestination(dst) {
		return
	}
	timeout := rt.HelloTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	_ = c.SetReadDeadline(time.Now().Add(timeout))
	sni, raw, err := ReadHello(c)
	_ = c.SetReadDeadline(time.Time{})
	if err != nil {
		return
	}
	if sni != "" && rt.IsMITM != nil && rt.IsMITM(sni) && rt.MITM != nil {
		_ = rt.MITM(ctx, c, sni, raw)
		return
	}
	if rt.Blind != nil {
		_ = rt.Blind(ctx, c, dst, raw)
	}
}

// maxRecord is the largest TLS record the parser reads (RFC 8446 §5.1).
const maxRecord = 16384

// ReadHello reads the first TLS record of c and returns its SNI. It returns the
// bytes it read even when the record is not a ClientHello, so the caller can
// replay them. A non-TLS first byte yields an empty SNI and no error.
func ReadHello(r io.Reader) (string, []byte, error) {
	hdr := make([]byte, 5)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return "", nil, err
	}
	raw := append([]byte{}, hdr...)
	if hdr[0] != 0x16 {
		return "", raw, nil
	}
	n := int(binary.BigEndian.Uint16(hdr[3:5]))
	if n == 0 || n > maxRecord {
		return "", raw, nil
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return "", raw, err
	}
	raw = append(raw, body...)
	sni, _ := ParseSNI(body)
	return sni, raw, nil
}

// ParseSNI extracts the server_name from a handshake record payload that starts
// with a ClientHello. It is strict about lengths and returns false on anything
// it does not understand.
func ParseSNI(rec []byte) (string, bool) {
	// Handshake header: type 1 (ClientHello), 3-byte length.
	if len(rec) < 4 || rec[0] != 0x01 {
		return "", false
	}
	hlen := int(rec[1])<<16 | int(rec[2])<<8 | int(rec[3])
	body := rec[4:]
	if hlen > len(body) {
		return "", false
	}
	body = body[:hlen]
	// version (2) + random (32)
	p := body
	if len(p) < 34 {
		return "", false
	}
	p = p[34:]
	// session_id, cipher_suites, compression_methods: each length-prefixed.
	for _, width := range []int{1, 2, 1} {
		if len(p) < width {
			return "", false
		}
		n := readLen(p[:width])
		if len(p) < width+n {
			return "", false
		}
		p = p[width+n:]
	}
	if len(p) < 2 {
		return "", false
	}
	exts := p[2:]
	if int(binary.BigEndian.Uint16(p[:2])) > len(exts) {
		return "", false
	}
	exts = exts[:binary.BigEndian.Uint16(p[:2])]
	for len(exts) >= 4 {
		typ := binary.BigEndian.Uint16(exts[:2])
		l := int(binary.BigEndian.Uint16(exts[2:4]))
		if len(exts) < 4+l {
			return "", false
		}
		data := exts[4 : 4+l]
		exts = exts[4+l:]
		if typ != 0 {
			continue
		}
		// server_name_list: length(2), then entries of type(1) length(2) name.
		if len(data) < 2 || int(binary.BigEndian.Uint16(data[:2])) > len(data)-2 {
			return "", false
		}
		list := data[2 : 2+int(binary.BigEndian.Uint16(data[:2]))]
		for len(list) >= 3 {
			nt := list[0]
			nl := int(binary.BigEndian.Uint16(list[1:3]))
			if len(list) < 3+nl {
				return "", false
			}
			if nt == 0 && nl > 0 {
				return lowerASCII(string(list[3 : 3+nl])), true
			}
			list = list[3+nl:]
		}
		return "", false
	}
	return "", false
}

func readLen(b []byte) int {
	n := 0
	for _, x := range b {
		n = n<<8 | int(x)
	}
	return n
}

func lowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// Splice copies bytes both ways between a and b until one side ends, then
// closes both.
func Splice(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	cp := func(dst, src net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(dst, src)
		_ = dst.Close()
		_ = src.Close()
	}
	go cp(a, b)
	go cp(b, a)
	wg.Wait()
}

// Dial connects to dst with a timeout.
func Dial(ctx context.Context, dst string) (net.Conn, error) {
	d := net.Dialer{Timeout: 10 * time.Second}
	return d.DialContext(ctx, "tcp4", dst)
}

// netAddrPort converts an address to netip form, when it is IPv4.
func netAddrPort(a net.Addr) (netip.AddrPort, bool) {
	ta, ok := a.(*net.TCPAddr)
	if !ok {
		return netip.AddrPort{}, false
	}
	ip, ok := netip.AddrFromSlice(ta.IP)
	if !ok {
		return netip.AddrPort{}, false
	}
	return netip.AddrPortFrom(ip.Unmap(), uint16(ta.Port)), true
}

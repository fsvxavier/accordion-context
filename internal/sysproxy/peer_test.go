package sysproxy

import (
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
)

// fakeProc lays out a /proc with one IPv4 connection whose uid column is uid.
// The uid column is the socket owner's uid, which is what PeerUID reads.
func fakeProc(t *testing.T, clientPort, serverPort int, uid uint64) string {
	t.Helper()
	root := t.TempDir()
	netDir := filepath.Join(root, "net")
	if err := os.MkdirAll(netDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// local 127.0.0.1:client, remote 127.0.0.1:server. 0x0100007F is 127.0.0.1 on little-endian.
	row := "   0: 0100007F:" + hex4(clientPort) + " 0100007F:" + hex4(serverPort) +
		" 01 00000000:00000000 00:00000000 00000000  " + itoa(uid) + "        0 55555 1 0000000000000000 100 0 0 10 0\n"
	header := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"
	if err := os.WriteFile(filepath.Join(netDir, "tcp"), []byte(header+row), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func hex4(v int) string {
	const digits = "0123456789ABCDEF"
	return string([]byte{digits[(v>>12)&15], digits[(v>>8)&15], digits[(v>>4)&15], digits[v&15]})
}

func itoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	return string(b)
}

func TestParseProcAddrLittleEndian(t *testing.T) {
	ap, err := parseProcAddr("0100007F:1F90")
	if err != nil {
		t.Fatal(err)
	}
	if ap != netip.MustParseAddrPort("127.0.0.1:8080") {
		t.Fatalf("parsed %v", ap)
	}
	if _, err := parseProcAddr("0100007F"); err == nil {
		t.Fatal("missing port accepted")
	}
}

func TestPeerUIDFromFakeProc(t *testing.T) {
	root := fakeProc(t, 46544, 8080, 1001)
	uid, err := PeerUID(root,
		netip.MustParseAddrPort("127.0.0.1:46544"), netip.MustParseAddrPort("127.0.0.1:8080"))
	if err != nil {
		t.Fatal(err)
	}
	if uid != 1001 {
		t.Fatalf("uid = %d, want the uid column 1001", uid)
	}
	// A connection that is not in the table cannot be attributed, so it is refused.
	if _, err := PeerUID(root,
		netip.MustParseAddrPort("127.0.0.1:1"), netip.MustParseAddrPort("127.0.0.1:8080")); err == nil {
		t.Fatal("unknown connection attributed")
	}
	check := LinuxPeerCheck(root, netip.MustParseAddrPort("127.0.0.1:8080"), 1000)
	if err := check(netip.MustParseAddrPort("127.0.0.1:46544")); err == nil {
		t.Fatal("peer of another uid accepted")
	}
	check = LinuxPeerCheck(root, netip.MustParseAddrPort("127.0.0.1:8080"), 1001)
	if err := check(netip.MustParseAddrPort("127.0.0.1:46544")); err != nil {
		t.Fatalf("peer of the owner uid refused: %v", err)
	}
}

// TestPeerListenerRefusesThenAccepts: a refused peer is closed before the
// server sees it; the next peer passes.
func TestPeerListenerRefusesThenAccepts(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("loopback unavailable: %v", err)
	}
	defer ln.Close()
	calls := 0
	pl := PeerListener{Listener: ln, Check: func(netip.AddrPort) error {
		calls++
		if calls == 1 {
			return os.ErrPermission
		}
		return nil
	}}
	first, err := net.Dial("tcp4", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := net.Dial("tcp4", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	c, err := pl.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if calls != 2 {
		t.Fatalf("check ran %d times, want 2", calls)
	}
	if _, err := first.Read(make([]byte, 1)); err == nil {
		t.Fatal("refused peer was not closed")
	}
}

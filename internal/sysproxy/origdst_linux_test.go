//go:build linux

package sysproxy

import (
	"encoding/binary"
	"net/netip"
	"syscall"
	"testing"
)

// The kernel's SO_ORIGINAL_DST answers decode to the original destination. The
// family is in host order and the port in network order.
func TestDecodeOriginalDstIPv4AndIPv6(t *testing.T) {
	var v4 [16]byte
	binary.NativeEndian.PutUint16(v4[0:2], syscall.AF_INET)
	binary.BigEndian.PutUint16(v4[2:4], 443)
	copy(v4[4:8], []byte{93, 184, 216, 34})
	got, err := decodeOriginalDst(v4[:], false)
	if err != nil || got != "93.184.216.34:443" {
		t.Fatalf("v4 = %q, %v", got, err)
	}

	var v6 [28]byte
	binary.NativeEndian.PutUint16(v6[0:2], syscall.AF_INET6)
	binary.BigEndian.PutUint16(v6[2:4], 443)
	addr := netip.MustParseAddr("2001:db8::34").As16()
	copy(v6[8:24], addr[:])
	got, err = decodeOriginalDst(v6[:], true)
	if err != nil || got != "[2001:db8::34]:443" {
		t.Fatalf("v6 = %q, %v", got, err)
	}
}

// A family the answer does not carry is refused, not guessed.
func TestDecodeOriginalDstRefusesWrongFamily(t *testing.T) {
	var v4 [16]byte
	binary.NativeEndian.PutUint16(v4[0:2], syscall.AF_INET6)
	if _, err := decodeOriginalDst(v4[:], false); err == nil {
		t.Fatal("IPv6 answer accepted as IPv4")
	}
	var short [4]byte
	if _, err := decodeOriginalDst(short[:], true); err == nil {
		t.Fatal("short IPv6 answer accepted")
	}
}

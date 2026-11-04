//go:build linux

package sysproxy

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"syscall"
	"unsafe"
)

// soOriginalDst is SO_ORIGINAL_DST (linux/netfilter_ipv4.h) at SOL_IP, and
// IP6T_SO_ORIGINAL_DST (linux/netfilter_ipv6/ip6_tables.h) at SOL_IPV6. Both are
// 80.
const soOriginalDst = 80

// sockaddrIn is struct sockaddr_in, as the kernel writes it for SO_ORIGINAL_DST.
type sockaddrIn struct {
	Family uint16
	Port   [2]byte // network byte order
	Addr   [4]byte
	Zero   [8]byte
}

// sockaddrIn6 is struct sockaddr_in6 for IP6T_SO_ORIGINAL_DST.
type sockaddrIn6 struct {
	Family   uint16
	Port     [2]byte // network byte order
	Flowinfo uint32
	Addr     [16]byte
	ScopeID  uint32
}

// OriginalDst returns the destination a redirected TCP connection had before the
// nat rule rewrote it. The family is the one of the local socket address.
func OriginalDst(c net.Conn) (string, error) {
	tc, ok := c.(*net.TCPConn)
	if !ok {
		return "", errors.New("original destination: not a TCP connection")
	}
	local, ok := netAddrPort(tc.LocalAddr())
	if !ok {
		return "", errors.New("original destination: local address is not an IP address")
	}
	v6 := local.Addr().Is6()
	rc, err := tc.SyscallConn()
	if err != nil {
		return "", err
	}
	var buf [28]byte // large enough for sockaddr_in6
	var n uint32
	var sockErr error
	err = rc.Control(func(fd uintptr) {
		n = uint32(len(buf))
		level := uintptr(syscall.SOL_IP)
		if v6 {
			level = syscall.SOL_IPV6
		}
		_, _, errno := syscall.Syscall6(syscall.SYS_GETSOCKOPT, fd, level,
			uintptr(soOriginalDst), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)), 0)
		if errno != 0 {
			sockErr = fmt.Errorf("SO_ORIGINAL_DST: %w", errno)
		}
	})
	if err != nil {
		return "", err
	}
	if sockErr != nil {
		return "", sockErr
	}
	return decodeOriginalDst(buf[:n], v6)
}

// decodeOriginalDst reads the sockaddr the kernel returned. Family is in host
// byte order, the port in network byte order.
func decodeOriginalDst(raw []byte, v6 bool) (string, error) {
	if v6 {
		if len(raw) < 24 {
			return "", errors.New("original destination: short IPv6 sockaddr")
		}
		var a sockaddrIn6
		copy((*[28]byte)(unsafe.Pointer(&a))[:], raw)
		if a.Family != syscall.AF_INET6 {
			return "", errors.New("original destination: not IPv6")
		}
		return netip.AddrPortFrom(netip.AddrFrom16(a.Addr), binary.BigEndian.Uint16(a.Port[:])).String(), nil
	}
	if len(raw) < 8 {
		return "", errors.New("original destination: short IPv4 sockaddr")
	}
	var a sockaddrIn
	copy((*[16]byte)(unsafe.Pointer(&a))[:], raw)
	if a.Family != syscall.AF_INET {
		return "", errors.New("original destination: not IPv4")
	}
	return netip.AddrPortFrom(netip.AddrFrom4(a.Addr), binary.BigEndian.Uint16(a.Port[:])).String(), nil
}

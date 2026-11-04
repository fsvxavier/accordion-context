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

// soOriginalDst is SO_ORIGINAL_DST from linux/netfilter_ipv4.h.
const soOriginalDst = 80

// sockaddrIn is struct sockaddr_in, as the kernel writes it for SO_ORIGINAL_DST.
type sockaddrIn struct {
	Family uint16
	Port   [2]byte // network byte order
	Addr   [4]byte
	Zero   [8]byte
}

// OriginalDst returns the destination a redirected IPv4 TCP connection had
// before the nat rule rewrote it.
func OriginalDst(c net.Conn) (string, error) {
	tc, ok := c.(*net.TCPConn)
	if !ok {
		return "", errors.New("original destination: not a TCP connection")
	}
	rc, err := tc.SyscallConn()
	if err != nil {
		return "", err
	}
	var addr sockaddrIn
	var sockErr error
	err = rc.Control(func(fd uintptr) {
		l := uint32(unsafe.Sizeof(addr))
		_, _, errno := syscall.Syscall6(syscall.SYS_GETSOCKOPT, fd, uintptr(syscall.SOL_IP),
			uintptr(soOriginalDst), uintptr(unsafe.Pointer(&addr)), uintptr(unsafe.Pointer(&l)), 0)
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
	if addr.Family != syscall.AF_INET {
		return "", errors.New("original destination: not IPv4")
	}
	ip := netip.AddrFrom4(addr.Addr)
	return netip.AddrPortFrom(ip, binary.BigEndian.Uint16(addr.Port[:])).String(), nil
}

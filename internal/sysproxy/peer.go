package sysproxy

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// PeerListener wraps a listener and closes every connection whose peer fails
// Check. The system proxy cannot carry the per-run token, so this is the check
// that stands in for it: only processes of the owner's uid may use the proxy.
type PeerListener struct {
	net.Listener
	// Check returns an error for a peer that must be refused. Nil accepts all.
	Check func(peer netip.AddrPort) error
}

// Accept returns the next connection whose peer passes Check.
func (l PeerListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if l.Check == nil {
			return c, nil
		}
		ap, ok := netAddrPort(c.RemoteAddr())
		if !ok {
			_ = c.Close()
			continue
		}
		if err := l.Check(ap); err != nil {
			_ = c.Close()
			continue
		}
		return c, nil
	}
}

// LinuxPeerCheck refuses peers whose socket belongs to another uid. It is built
// from /proc under procRoot. A peer it cannot attribute is refused.
func LinuxPeerCheck(procRoot string, server netip.AddrPort, selfUID int) func(netip.AddrPort) error {
	if procRoot == "" {
		procRoot = "/proc"
	}
	return func(peer netip.AddrPort) error {
		uid, err := PeerUID(procRoot, peer, server)
		if err != nil {
			return err
		}
		if uid != selfUID {
			return fmt.Errorf("peer uid %d is not %d", uid, selfUID)
		}
		return nil
	}
}

// PeerUID returns the owner uid of the IPv4 TCP connection whose local end is
// client and whose remote end is server. The uid column of /proc/net/tcp is the
// socket owner's uid, so no process table walk is needed. That walk is slow
// with many connections, and it fails for same-uid processes that are not
// dumpable.
func PeerUID(procRoot string, client, server netip.AddrPort) (int, error) {
	data, err := os.ReadFile(filepath.Join(procRoot, "net", "tcp"))
	if err != nil {
		return 0, fmt.Errorf("peer lookup: %w", err)
	}
	uid, ok := findUID(string(data), client, server)
	if !ok {
		return 0, errors.New("peer lookup: connection not found in net/tcp")
	}
	return uid, nil
}

// findUID scans the rows of /proc/net/tcp for the connection and returns its
// uid column.
func findUID(text string, client, server netip.AddrPort) (int, bool) {
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		// sl local rem st tx:rx tr:tm retrnsmt uid timeout inode
		if len(f) < 10 {
			continue
		}
		local, err1 := parseProcAddr(f[1])
		rem, err2 := parseProcAddr(f[2])
		if err1 != nil || err2 != nil {
			continue
		}
		// Only an ESTABLISHED row (01) is the live connection. A TIME_WAIT row
		// reports uid 0 whatever the owner was.
		if f[3] == "01" && local == client && rem == server {
			uid, err := strconv.Atoi(f[7])
			if err == nil && uid >= 0 {
				return uid, true
			}
			return 0, false
		}
	}
	return 0, false
}

// parseProcAddr decodes "0100007F:1F90" from /proc/net/tcp. The address is
// the kernel's host-order u32, which is little-endian on the supported hosts.
func parseProcAddr(s string) (netip.AddrPort, error) {
	ip, port, ok := strings.Cut(s, ":")
	if !ok || len(ip) != 8 {
		return netip.AddrPort{}, errors.New("bad address")
	}
	v, err := strconv.ParseUint(ip, 16, 32)
	if err != nil {
		return netip.AddrPort{}, err
	}
	p, err := strconv.ParseUint(port, 16, 16)
	if err != nil {
		return netip.AddrPort{}, err
	}
	addr := netip.AddrFrom4([4]byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)})
	return netip.AddrPortFrom(addr, uint16(p)), nil
}

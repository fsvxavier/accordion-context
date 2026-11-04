//go:build !linux

package sysproxy

import "net"

// OriginalDst is not available off Linux, where transparent mode is not offered.
func OriginalDst(net.Conn) (string, error) { return "", ErrUnsupported }

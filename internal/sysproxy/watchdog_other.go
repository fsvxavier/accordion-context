//go:build !linux

package sysproxy

import "os"

// ExecSpawner is not available off Linux, where transparent mode is not offered.
func ExecSpawner(_ string, _ int, pipe *os.File) (func() error, error) {
	_ = pipe.Close()
	return nil, ErrUnsupported
}

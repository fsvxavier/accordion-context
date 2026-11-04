package proxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// CheckLoopback rejects listen addresses that are reachable from other hosts.
// The proxy holds API credentials in transit, so it binds to loopback only.
func CheckLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("proxy: listen address %q: %w", addr, err)
	}
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("proxy: listen address %q is not loopback; refusing to bind", addr)
	}
	return nil
}

// Run serves h on addr until ctx is done, then shuts down gracefully.
func Run(ctx context.Context, addr string, h http.Handler) error {
	if err := CheckLoopback(addr); err != nil {
		return err
	}
	return serveListen(ctx, addr, h)
}

// serveListen binds addr and serves h until ctx is done. Callers check the address first.
func serveListen(ctx context.Context, addr string, h http.Handler) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			_ = srv.Close() // streams that outlive the grace period are cut
			return fmt.Errorf("proxy: shutdown: %w", err)
		}
		return nil
	}
}

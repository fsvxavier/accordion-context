package proxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
)

// CheckListen is CheckLoopback with the explicit opt-in for container
// deployments. A non-loopback address needs both allowNonLoopback and a
// non-empty allowedHosts list; the list is what the Host guard then enforces.
// The default (loopback only) is unchanged.
func CheckListen(addr string, allowNonLoopback bool, allowedHosts []string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("proxy: listen address %q: %w", addr, err)
	}
	if isLoopbackHost(host) {
		return nil
	}
	if !allowNonLoopback {
		return fmt.Errorf("proxy: listen address %q is not loopback; pass --allow-non-loopback with --allowed-hosts to publish it", addr)
	}
	if len(allowedHosts) == 0 {
		return fmt.Errorf("proxy: listen address %q is not loopback; --allow-non-loopback requires a non-empty --allowed-hosts", addr)
	}
	return nil
}

// RunListen serves h on addr like Run, but accepts a non-loopback address when
// CheckListen allows it.
func RunListen(ctx context.Context, addr string, allowNonLoopback bool, allowedHosts []string, h http.Handler) error {
	if err := CheckListen(addr, allowNonLoopback, allowedHosts); err != nil {
		return err
	}
	return serveListen(ctx, addr, h)
}

// NormalizeHosts turns Host header values (for example "localhost:8787",
// "[::1]" or "host.docker.internal") into the bare lower-case names the guard
// compares against. Wildcards and paths are refused, since they would widen
// the guard silently.
func NormalizeHosts(list []string) ([]string, error) {
	var out []string
	for _, raw := range list {
		h := strings.ToLower(strings.TrimSpace(raw))
		if h == "" {
			continue
		}
		if strings.ContainsAny(h, "*/ \t") {
			return nil, fmt.Errorf("proxy: allowed host %q: wildcards, paths and spaces are not allowed", raw)
		}
		if host, _, err := net.SplitHostPort(h); err == nil {
			h = host
		} else {
			h = strings.TrimSuffix(strings.TrimPrefix(h, "["), "]")
		}
		if h == "" {
			return nil, fmt.Errorf("proxy: allowed host %q is empty", raw)
		}
		out = append(out, h)
	}
	return out, nil
}

// guardHosts builds the set of Host names the guard accepts. With no explicit
// list it is the loopback names plus the listen host (the default). With an
// explicit list it is exactly that list.
func guardHosts(listenHost string, allowed []string) (map[string]bool, error) {
	if len(allowed) > 0 {
		names, err := NormalizeHosts(allowed)
		if err != nil {
			return nil, err
		}
		set := make(map[string]bool, len(names))
		for _, n := range names {
			set[n] = true
		}
		return set, nil
	}
	hosts := map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}
	if h := strings.ToLower(listenHost); h != "" {
		hosts[h] = true
	}
	return hosts, nil
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

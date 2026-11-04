package proxy

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/fsvxavier/accordion-context/internal/kernel"
)

// routed is the upstream target of one request.
type routed struct {
	path    string   // path at the upstream, for example /v1/messages
	project string   // project id from a /p/<id>/ prefix, "" when absent
	up      *url.URL // upstream base URL
	viaCtx  bool     // the target came from a /ctx/ prefix
}

// route strips the /p/<id>/ and /ctx/<upstream>/ prefixes (spec §18.2, §19.3 item 1).
// /ctx/ takes an absolute http(s) URL and is refused unless its origin is allowed.
// It returns a non-zero HTTP status with a message when the request must be refused.
func (s *Server) route(p string) (routed, int, string) {
	rt := routed{path: p, up: s.cfg.Upstream}
	if rest, ok := strings.CutPrefix(rt.path, "/p/"); ok {
		id, tail, found := strings.Cut(rest, "/")
		if !found || !projectIDPattern.MatchString(id) {
			return routed{}, http.StatusBadRequest, "invalid project prefix"
		}
		rt.project = id
		rt.path = "/" + tail
	}
	if rest, ok := strings.CutPrefix(rt.path, "/ctx/"); ok {
		up, tail, err := parseCtxTarget(rest)
		if err != nil {
			return routed{}, http.StatusBadRequest, err.Error()
		}
		if !s.allow[originOf(up)] {
			return routed{}, http.StatusForbidden, "upstream not in the allowlist"
		}
		rt.up = up
		rt.path = tail
		rt.viaCtx = true
	}
	return rt, 0, ""
}

// parseCtxTarget splits "https://host/tail" (the text after /ctx/) into the
// upstream base URL and the path to request there.
func parseCtxTarget(rest string) (*url.URL, string, error) {
	scheme, after, ok := strings.Cut(rest, "://")
	if !ok || (scheme != "http" && scheme != "https") {
		return nil, "", errors.New("ctx: target must be an absolute http(s) URL")
	}
	host, tail, _ := strings.Cut(after, "/")
	if host == "" || strings.ContainsAny(host, "@?# \\") {
		return nil, "", errors.New("ctx: invalid upstream host")
	}
	up, err := url.Parse(scheme + "://" + host)
	if err != nil || up.User != nil || up.Hostname() == "" {
		return nil, "", errors.New("ctx: invalid upstream host")
	}
	return up, "/" + tail, nil
}

// originOf returns the allowlist key of an upstream: scheme and lower-case host.
func originOf(u *url.URL) string {
	return u.Scheme + "://" + strings.ToLower(u.Host)
}

// allowSet builds the origin allowlist. Without entries, only the origin of the
// configured upstream is allowed.
func allowSet(list []string, upstream *url.URL) (map[string]bool, error) {
	out := map[string]bool{}
	if len(list) == 0 {
		out[originOf(upstream)] = true
		return out, nil
	}
	for _, raw := range list {
		u, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
			(u.Path != "" && u.Path != "/") || u.RawQuery != "" {
			return nil, fmt.Errorf("proxy: allowed upstream %q must be an http(s) origin", raw)
		}
		out[originOf(u)] = true
	}
	return out, nil
}

func sameMsg(a, b kernel.Message) bool {
	return a.Role == b.Role && a.Kind == b.Kind && a.ToolName == b.ToolName &&
		a.ToolCallID == b.ToolCallID && a.Text == b.Text
}

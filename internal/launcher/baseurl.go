package launcher

import (
	"fmt"
	"regexp"
	"strings"
)

// Placeholder sets. A template may use only the names given for its use.
var (
	baseVars = map[string]string{"url": "", "port": "", "token": ""}
	pathVars = map[string]string{"path": ""}
	// placeholderPattern matches {name} only, so JSON or other braces pass.
	placeholderPattern = regexp.MustCompile(`\{([A-Za-z_]+)\}`)
)

// FlagsAfter reports whether the flags of this profile go after the client's
// own arguments (flag_position: after).
func (p Profile) FlagsAfter() bool {
	return p.FlagPosition == "after"
}

// IgnoresBaseURL reports whether quirks.ignores_base_url is set. Such a client
// does not honor a base URL, so attach skips the base URL degrees for it.
func (p Profile) IgnoresBaseURL() bool {
	v, ok := p.Quirks["ignores_base_url"].(bool)
	return ok && v
}

// Render replaces each {name} in tmpl with vars[name]. A placeholder that is
// not a key of vars is an error, so a typo cannot reach the client.
func Render(tmpl string, vars map[string]string) (string, error) {
	var bad []string
	out := placeholderPattern.ReplaceAllStringFunc(tmpl, func(m string) string {
		name := m[1 : len(m)-1]
		v, ok := vars[name]
		if !ok {
			bad = append(bad, m)
			return m
		}
		return v
	})
	if len(bad) > 0 {
		return "", fmt.Errorf("unknown placeholder %s", strings.Join(bad, ", "))
	}
	return out, nil
}

// ExpandFlag splits an option template on whitespace and renders each token,
// so a value containing spaces stays one argument. "--api-base {url}" becomes
// ["--api-base", "<url>"].
func ExpandFlag(tmpl string, vars map[string]string) ([]string, error) {
	fields := strings.Fields(tmpl)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		s, err := Render(f, vars)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// ArgvWith returns argv (argv[0] is the command) with flags inserted before the
// user arguments, or after them when after is true. argv is not modified.
func ArgvWith(argv, flags []string, after bool) []string {
	if len(argv) == 0 {
		return nil
	}
	out := make([]string, 0, len(argv)+len(flags))
	out = append(out, argv[0])
	if !after {
		out = append(out, flags...)
		return append(out, argv[1:]...)
	}
	out = append(out, argv[1:]...)
	return append(out, flags...)
}

// BaseURLFlagArgs returns the argv tokens of the base URL flag for baseURL.
func BaseURLFlagArgs(p Profile, baseURL, port, token string) ([]string, error) {
	if p.BaseURLFlag == "" {
		return nil, nil
	}
	return ExpandFlag(p.BaseURLFlag, map[string]string{"url": baseURL, "port": port, "token": token})
}

// PathFlagArgs returns the argv tokens of the temp config path flag for path.
func PathFlagArgs(t TempConfig, path string) ([]string, error) {
	if t.PathFlag == "" {
		return nil, nil
	}
	return ExpandFlag(t.PathFlag, map[string]string{"path": path})
}

// RenderTempConfig returns the content of a template-based temp config.
func RenderTempConfig(t TempConfig, baseURL, port, token string) (string, error) {
	return Render(t.Template, map[string]string{"url": baseURL, "port": port, "token": token})
}

// ConfigExt returns the file extension for a temp config format.
func ConfigExt(format string) string {
	if format == "env" {
		return ".env"
	}
	return "." + format
}

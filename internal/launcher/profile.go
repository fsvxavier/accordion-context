// Package launcher starts a coding client with its model traffic routed
// through the Accordion Context proxy (spec §19.3, item 2).
//
// The client is configured only through environment variables built from a
// declarative Profile. User config files are never read or written.
package launcher

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Profile declares how one client family is pointed at the proxy.
type Profile struct {
	Name string `yaml:"name"`
	// Match lists command names that select this profile (basename, case
	// insensitive, without .exe). Defaults to Name when empty.
	Match []string `yaml:"match"`
	// Upstream is the real provider base URL, including any fixed path
	// prefix the SDK expects (e.g. https://api.openai.com/v1).
	Upstream string `yaml:"upstream"`
	// BaseURLEnv receives the proxy base URL. Empty means the client has no
	// base URL variable; Run refuses such clients with ErrNoBaseURL.
	BaseURLEnv string `yaml:"base_url_env"`
	// Env holds static extra variables for the client process.
	Env map[string]string `yaml:"env"`
}

// ErrNoBaseURL reports a client whose profile has no base URL variable. Such
// clients need the MITM mode (§19.3, item 3), which this package does not do.
var ErrNoBaseURL = errors.New("launcher: client has no base URL variable; the MITM mode is required")

var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var builtinProfiles = []Profile{
	{
		Name:       "claude",
		Match:      []string{"claude"},
		Upstream:   "https://api.anthropic.com",
		BaseURLEnv: "ANTHROPIC_BASE_URL",
	},
	{
		Name:       "codex",
		Match:      []string{"codex"},
		Upstream:   "https://api.openai.com/v1",
		BaseURLEnv: "OPENAI_BASE_URL",
	},
	{
		Name:       "gemini",
		Match:      []string{"gemini"},
		Upstream:   "https://generativelanguage.googleapis.com",
		BaseURLEnv: "GOOGLE_GEMINI_BASE_URL",
	},
	{
		// OpenCode has no documented base URL variable; its providers are set
		// in its own config. Launching it needs the MITM mode.
		Name:  "opencode",
		Match: []string{"opencode"},
	},
}

// BuiltinProfiles returns the profiles compiled into the binary.
func BuiltinProfiles() []Profile {
	return append([]Profile(nil), builtinProfiles...)
}

// ProfileFor returns the built-in profile for a client command.
func ProfileFor(cmd string) (Profile, bool) {
	return MatchProfile(builtinProfiles, cmd)
}

// MatchProfile returns the first profile in profiles that matches cmd.
func MatchProfile(profiles []Profile, cmd string) (Profile, bool) {
	name := commandName(cmd)
	for _, p := range profiles {
		names := p.Match
		if len(names) == 0 {
			names = []string{p.Name}
		}
		for _, m := range names {
			if strings.EqualFold(m, name) {
				return p, true
			}
		}
	}
	return Profile{}, false
}

// LoadProfiles parses a YAML document of the form
//
//	profiles:
//	  - name: my-client
//	    match: [my-client]
//	    upstream: https://api.example.com
//	    base_url_env: MY_CLIENT_BASE_URL
//
// Unknown keys are rejected. An empty document yields no profiles.
func LoadProfiles(r io.Reader) ([]Profile, error) {
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	var doc struct {
		Profiles []Profile `yaml:"profiles"`
	}
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		return nil, fmt.Errorf("launcher: parse profiles: %w", err)
	}
	for _, p := range doc.Profiles {
		if err := validate(p); err != nil {
			return nil, fmt.Errorf("launcher: profile %q: %w", p.Name, err)
		}
	}
	return doc.Profiles, nil
}

func validate(p Profile) error {
	if p.Name == "" {
		return errors.New("name is required")
	}
	if p.BaseURLEnv != "" {
		if !envNamePattern.MatchString(p.BaseURLEnv) {
			return fmt.Errorf("base_url_env %q is not a valid variable name", p.BaseURLEnv)
		}
		u, err := url.Parse(p.Upstream)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("upstream %q must be an absolute http(s) URL", p.Upstream)
		}
	}
	for k := range p.Env {
		if !envNamePattern.MatchString(k) {
			return fmt.Errorf("env name %q is not a valid variable name", k)
		}
	}
	return nil
}

// BaseURL is the value of the profile's base URL variable: the proxy address,
// the project prefix and the real upstream carried in the path (§19.3).
func BaseURL(addr, projectID string, p Profile) string {
	return "http://" + addr + "/p/" + projectID + "/ctx/" + p.Upstream
}

func commandName(cmd string) string {
	base := filepath.Base(cmd)
	return strings.TrimSuffix(strings.ToLower(base), ".exe")
}

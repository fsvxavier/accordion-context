// Package launcher starts a coding client with its model traffic routed
// through the Accordion Context proxy (spec §19.3, item 2).
//
// The client is configured through environment variables, command-line flags
// or a temporary config file built from a declarative Profile. User config
// files are read at most (merge_from) and never written.
package launcher

import (
	"bytes"
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
	// insensitive, without .exe). When empty it defaults to Detect.Binaries,
	// and then to Name.
	Match []string `yaml:"match"`
	// Upstream is the real provider base URL, including any fixed path
	// prefix the SDK expects (e.g. https://api.openai.com/v1). Required when
	// any base URL degree is declared.
	Upstream string `yaml:"upstream"`
	// BaseURLEnv receives the proxy base URL (degree base-url-env). Empty means
	// the client has no base URL variable; Run refuses such clients with
	// ErrNoBaseURL.
	BaseURLEnv string `yaml:"base_url_env"`
	// BaseURLFlag is a command-line option template that receives the proxy
	// base URL through {url}, e.g. "--api-base {url}" (degree base-url-flag).
	// It is split on whitespace before the placeholder is filled.
	BaseURLFlag string `yaml:"base_url_flag"`
	// FlagPosition is "before" (default) or "after": where BaseURLFlag and the
	// temp config path flag go relative to the user's own arguments.
	FlagPosition string `yaml:"flag_position"`
	// TempConfig describes a temporary config file that carries the base URL
	// (degree temp-config). The file lives in a per-run directory that is
	// removed when the run ends.
	TempConfig *TempConfig `yaml:"temp_config"`
	// Env holds static extra variables for the client process.
	Env map[string]string `yaml:"env"`

	// Detect, Integration, ModelInfo and Quirks are the declarative fields of
	// spec §19.9. Detect.Binaries takes part in matching; the others are
	// loaded and validated, and Quirks.ignores_base_url is honored by attach.
	Detect      Detect         `yaml:"detect"`
	Integration Integration    `yaml:"integration"`
	ModelInfo   ModelInfo      `yaml:"model_info"`
	Quirks      map[string]any `yaml:"quirks"`
}

// TempConfig tells the client where its config file is and what it contains.
// Exactly one of PathEnv or PathFlag names how the client finds the file, and
// exactly one of Template or MergeFrom says what the file contains.
type TempConfig struct {
	// PathEnv is the variable that holds the config file path.
	PathEnv string `yaml:"path_env"`
	// PathFlag is an option template with {path}, e.g. "--config {path}".
	PathFlag string `yaml:"path_flag"`
	// Format is one of json, yaml, toml, env, ini.
	Format string `yaml:"format"`
	// Template is the file content, with {url}, {port} and {token}.
	Template string `yaml:"template"`
	// MergeFrom is an existing user file, read only, whose content is copied
	// with the base URL set at Key.
	MergeFrom string `yaml:"merge_from"`
	// Key is the base URL key used with MergeFrom: a dotted path for json,
	// yaml and toml, "section.name" for ini, and the variable name for env.
	Key string `yaml:"key"`
}

// Detect identifies the client on this machine (spec §19.9).
type Detect struct {
	Binaries     []string `yaml:"binaries"`
	ConfigPaths  []string `yaml:"config_paths"`
	VersionRegex string   `yaml:"version_regex"`
}

// Integration lists the preferred degrees and the data for them (spec §19.9).
type Integration struct {
	// Prefer orders and filters the base URL degrees the profile declares.
	// Names: base_url_env, base_url_flag, temp_config, https_proxy, mitm.
	// Empty keeps the default ladder order.
	Prefer []string `yaml:"prefer"`
	// BaseURLEnvMap maps variables to URL templates (spec §19.9 form). It is
	// loaded but not used for the base-url-env degree yet.
	BaseURLEnvMap map[string]string `yaml:"base_url_env"`
	// HTTPSProxy holds the CA and domain data of the MITM degree.
	HTTPSProxy *HTTPSProxy `yaml:"https_proxy"`
}

// HTTPSProxy is the spec §19.9 https_proxy block. Loaded, not yet used.
type HTTPSProxy struct {
	CAEnv   []string `yaml:"ca_env"`
	Domains []string `yaml:"domains"`
}

// ModelInfo says where to read the model's limits from (spec §19.9). Loaded,
// not yet used.
type ModelInfo struct {
	File          string `yaml:"file"`
	ContextWindow string `yaml:"context_window"`
	MaxOutput     string `yaml:"max_output"`
}

// ErrNoBaseURL reports a client whose profile has no base URL variable. Such
// clients need the MITM mode (§19.3, item 3), which this package does not do.
var ErrNoBaseURL = errors.New("launcher: client has no base URL variable; the MITM mode is required")

var (
	envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	flagPositions  = map[string]bool{"": true, "before": true, "after": true}
	tempFormats    = map[string]bool{"json": true, "yaml": true, "toml": true, "env": true, "ini": true}
	preferNames    = map[string]bool{"base_url_env": true, "base_url_flag": true, "temp_config": true, "https_proxy": true, "mitm": true}
)

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
			names = p.Detect.Binaries
		}
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

// LoadProfiles parses a YAML document in one of two shapes:
//
//	profiles:
//	  - name: my-client
//	    match: [my-client]
//	    upstream: https://api.example.com
//	    base_url_env: MY_CLIENT_BASE_URL
//
// or a single profile at the top level, the form of spec §19.9. Unknown keys
// are rejected. An empty document yields no profiles.
func LoadProfiles(r io.Reader) ([]Profile, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("launcher: read profiles: %w", err)
	}
	var top map[string]yaml.Node
	if err := yaml.Unmarshal(data, &top); err != nil {
		return nil, fmt.Errorf("launcher: parse profiles: %w", err)
	}
	if top == nil {
		return nil, nil
	}
	var profiles []Profile
	if _, ok := top["profiles"]; ok {
		var doc struct {
			Profiles []Profile `yaml:"profiles"`
		}
		if err := decodeStrict(data, &doc); err != nil {
			return nil, fmt.Errorf("launcher: parse profiles: %w", err)
		}
		profiles = doc.Profiles
	} else {
		var p Profile
		if err := decodeStrict(data, &p); err != nil {
			return nil, fmt.Errorf("launcher: parse profile: %w", err)
		}
		profiles = []Profile{p}
	}
	for _, p := range profiles {
		if err := validate(p); err != nil {
			return nil, fmt.Errorf("launcher: profile %q: %w", p.Name, err)
		}
	}
	return profiles, nil
}

func decodeStrict(data []byte, v any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func validate(p Profile) error {
	if p.Name == "" {
		return errors.New("name is required")
	}
	if p.BaseURLEnv != "" && !envNamePattern.MatchString(p.BaseURLEnv) {
		return fmt.Errorf("base_url_env %q is not a valid variable name", p.BaseURLEnv)
	}
	if p.BaseURLEnv != "" || p.BaseURLFlag != "" || p.TempConfig != nil {
		u, err := url.Parse(p.Upstream)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("upstream %q must be an absolute http(s) URL", p.Upstream)
		}
	}
	if !flagPositions[p.FlagPosition] {
		return fmt.Errorf("flag_position %q must be \"before\" or \"after\"", p.FlagPosition)
	}
	if p.BaseURLFlag != "" {
		if err := validateFlag(p.BaseURLFlag, "{url}", baseVars); err != nil {
			return fmt.Errorf("base_url_flag: %w", err)
		}
	}
	if p.TempConfig != nil {
		if err := validateTempConfig(*p.TempConfig); err != nil {
			return fmt.Errorf("temp_config: %w", err)
		}
	}
	for k := range p.Env {
		if !envNamePattern.MatchString(k) {
			return fmt.Errorf("env name %q is not a valid variable name", k)
		}
	}
	if p.Detect.VersionRegex != "" {
		if _, err := regexp.Compile(p.Detect.VersionRegex); err != nil {
			return fmt.Errorf("detect.version_regex: %w", err)
		}
	}
	for _, d := range p.Integration.Prefer {
		if !preferNames[d] {
			return fmt.Errorf("integration.prefer: unknown degree %q", d)
		}
	}
	return nil
}

// validateFlag checks an option template: it must hold the required
// placeholder and use only placeholders in vars.
func validateFlag(tmpl, required string, vars map[string]string) error {
	if len(strings.Fields(tmpl)) == 0 {
		return errors.New("template is empty")
	}
	if !strings.Contains(tmpl, required) {
		return fmt.Errorf("template %q must contain %s", tmpl, required)
	}
	_, err := Render(tmpl, vars)
	return err
}

func validateTempConfig(t TempConfig) error {
	if (t.PathEnv == "") == (t.PathFlag == "") {
		return errors.New("needs exactly one of path_env or path_flag")
	}
	if t.PathEnv != "" && !envNamePattern.MatchString(t.PathEnv) {
		return fmt.Errorf("path_env %q is not a valid variable name", t.PathEnv)
	}
	if t.PathFlag != "" {
		if err := validateFlag(t.PathFlag, "{path}", pathVars); err != nil {
			return fmt.Errorf("path_flag: %w", err)
		}
	}
	if !tempFormats[t.Format] {
		return fmt.Errorf("format %q must be one of json, yaml, toml, env, ini", t.Format)
	}
	if (t.Template == "") == (t.MergeFrom == "") {
		return errors.New("needs exactly one of template or merge_from")
	}
	if t.Template != "" {
		if !strings.Contains(t.Template, "{url}") && !strings.Contains(t.Template, "{port}") {
			return errors.New("template must contain {url} or {port}")
		}
		if _, err := Render(t.Template, baseVars); err != nil {
			return fmt.Errorf("template: %w", err)
		}
		if t.Key != "" {
			return errors.New("key is only used with merge_from")
		}
		return nil
	}
	if t.Format == "toml" {
		return errors.New("merge_from is not supported for toml; use a template")
	}
	if t.Key == "" {
		return errors.New("merge_from needs key")
	}
	if t.Format == "env" {
		if !envNamePattern.MatchString(t.Key) {
			return fmt.Errorf("key %q is not a valid variable name", t.Key)
		}
		return nil
	}
	for _, seg := range strings.Split(t.Key, ".") {
		if seg == "" {
			return fmt.Errorf("key %q has an empty segment", t.Key)
		}
	}
	return nil
}

// BaseURL is the value of the profile's base URL variable: the proxy address,
// the project prefix and the real upstream carried in the path (§19.3).
func BaseURL(addr, projectID string, p Profile) string {
	return "http://" + addr + "/p/" + projectID + "/ctx/" + p.Upstream
}

// commandName returns the lower-case basename of cmd without a .exe suffix.
func commandName(cmd string) string {
	base := filepath.Base(cmd)
	return strings.TrimSuffix(strings.ToLower(base), ".exe")
}

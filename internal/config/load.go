package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Options selects the inputs for Load. Zero values mean "not provided".
type Options struct {
	// GlobalPath is the global config file. Empty means DefaultGlobalPath().
	GlobalPath string
	// Root is the project root (the directory that holds .acd-ctx/, or the
	// repository root). Empty disables local config and project overrides.
	Root string
	// Flags maps dotted keys (e.g. "trigger.step_base") to raw values. This is
	// the highest-precedence layer.
	Flags map[string]string
	// LookupEnv reads environment variables. Nil means os.LookupEnv.
	LookupEnv func(string) (string, bool)
}

// Entry is one key in the effective configuration.
type Entry struct {
	Key    string
	Value  string
	Source string // "default", "global:<path>", "global:projects[<pattern>]", "local:<path>", "env:<VAR>", "flag"
}

// Result is the outcome of Load.
type Result struct {
	Config   Config
	Warnings []string
	entries  []Entry
}

// Effective returns every known key with its final value and the layer that
// supplied it, in spec-table order.
func (r Result) Effective() []Entry {
	out := make([]Entry, len(r.entries))
	copy(out, r.entries)
	return out
}

// DefaultGlobalPath returns the global config path for this OS (§18.1).
func DefaultGlobalPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate user config dir: %w", err)
	}
	return filepath.Join(dir, "acd-ctx", "config.yaml"), nil
}

// layer is one source of values, applied in slice order (later wins).
// values holds YAML leaves or parsed env/flag values, keyed by dotted path.
type layer struct {
	name   string
	values map[string]any
}

// Load merges all layers, validates the result, and records the source of
// each key.
func Load(opts Options) (Result, error) {
	lookup := opts.LookupEnv
	if lookup == nil {
		lookup = os.LookupEnv
	}
	globalPath := opts.GlobalPath
	if globalPath == "" {
		p, err := DefaultGlobalPath()
		if err != nil {
			return Result{}, err
		}
		globalPath = p
	}

	var (
		layers   []layer
		warnings []string
	)

	// 1. Global file, then the matching "projects" overrides from the same file.
	top, err := readFile(globalPath)
	if err != nil {
		return Result{}, err
	}
	projects, hasProjects := top["projects"]
	delete(top, "projects")
	gvals, gwarn, err := scopeGlobal(top, globalPath)
	if err != nil {
		return Result{}, err
	}
	warnings = append(warnings, gwarn...)
	layers = append(layers, layer{name: "global:" + globalPath, values: gvals})

	if hasProjects {
		ov, owarn, err := matchProjects(projects, opts.Root, globalPath)
		if err != nil {
			return Result{}, err
		}
		warnings = append(warnings, owarn...)
		layers = append(layers, ov...)
	}

	// 2. Local project file, allowlist only (§18.6).
	if opts.Root != "" {
		localPath := filepath.Join(opts.Root, ".acd-ctx", "config.yaml")
		ltop, err := readFile(localPath)
		if err != nil {
			return Result{}, err
		}
		if _, ok := ltop["projects"]; ok {
			warnings = append(warnings, fmt.Sprintf("ignored \"projects\" in %s: only the global config may define it", localPath))
			delete(ltop, "projects")
		}
		lvals, lwarn := scopeLocal(ltop, localPath)
		warnings = append(warnings, lwarn...)
		layers = append(layers, layer{name: "local:" + localPath, values: lvals})
	}

	// 3. Environment: one layer per variable, so each source names its variable.
	// An empty variable counts as unset.
	scratch := Defaults()
	for _, s := range specs {
		name := EnvName(s.key)
		raw, ok := lookup(name)
		if !ok || strings.TrimSpace(raw) == "" {
			continue
		}
		v, err := parseRaw(s.field(&scratch), raw)
		if err != nil {
			return Result{}, fmt.Errorf("env %s: %w", name, err)
		}
		layers = append(layers, layer{name: "env:" + name, values: map[string]any{s.key: v}})
	}

	// 4. Flags.
	if len(opts.Flags) > 0 {
		fl := layer{name: "flag", values: map[string]any{}}
		for _, k := range sortedKeys(opts.Flags) {
			s, ok := specByKey[k]
			if !ok {
				return Result{}, fmt.Errorf("flag %s: unknown configuration key", k)
			}
			v, err := parseRaw(s.field(&scratch), opts.Flags[k])
			if err != nil {
				return Result{}, fmt.Errorf("flag %s: %w", k, err)
			}
			fl.values[k] = v
		}
		layers = append(layers, fl)
	}

	// Apply layers in order. Each key ends with the value from the last layer
	// that sets it.
	cfg := Defaults()
	sources := map[string]string{}
	for _, l := range layers {
		for _, k := range sortedKeys(l.values) {
			s := specByKey[k] // every layer holds only known keys
			if err := assign(s.field(&cfg), l.values[k]); err != nil {
				return Result{}, fmt.Errorf("%s: %s: %w", l.name, k, err)
			}
			sources[k] = l.name
		}
	}

	if ps := validate(cfg); len(ps) > 0 {
		return Result{}, validationError(ps, cfg, sources)
	}

	entries := make([]Entry, 0, len(specs))
	for _, s := range specs {
		src, ok := sources[s.key]
		if !ok {
			src = "default"
		}
		entries = append(entries, Entry{Key: s.key, Value: formatValue(s.field(&cfg)), Source: src})
	}
	return Result{Config: cfg, Warnings: warnings, entries: entries}, nil
}

// readFile parses a YAML file into a generic map. A missing file yields an
// empty map.
func readFile(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	m := map[string]any{}
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

// isMapKey reports whether key is a known key whose value is a mapping. Such a
// mapping is one leaf, so flatten must not split it into dotted keys.
func isMapKey(key string) bool {
	s, ok := specByKey[key]
	if !ok {
		return false
	}
	_, isMap := s.field(&Config{}).(*map[string]int)
	return isMap
}

// flatten turns nested maps into dotted keys. Lists, scalars and mappings
// under a map-valued key (window.models) are leaves.
func flatten(prefix string, m map[string]any, out map[string]any) {
	for k, v := range m {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		if sub, ok := v.(map[string]any); ok && !isMapKey(key) {
			flatten(key, sub, out)
			continue
		}
		out[key] = v
	}
}

// scopeGlobal keeps known keys. Unknown keys are a hard error, so typos are
// caught. Reserved keys (§18.6) are accepted but ignored, with a warning.
func scopeGlobal(top map[string]any, path string) (map[string]any, []string, error) {
	flat := map[string]any{}
	flatten("", top, flat)
	out := map[string]any{}
	var warns []string
	for _, k := range sortedKeys(flat) {
		if _, ok := specByKey[k]; ok {
			out[k] = flat[k]
			continue
		}
		if isReserved(k) {
			warns = append(warns, fmt.Sprintf("ignored %s in %s: reserved key, not implemented yet", k, path))
			continue
		}
		return nil, nil, fmt.Errorf("%s: unknown configuration key %q", path, k)
	}
	return out, warns, nil
}

// scopeLocal keeps only keys the local layer may set (§18.6). Anything else is
// dropped with a warning, not an error.
func scopeLocal(top map[string]any, path string) (map[string]any, []string) {
	flat := map[string]any{}
	flatten("", top, flat)
	out := map[string]any{}
	var warns []string
	for _, k := range sortedKeys(flat) {
		if s, ok := specByKey[k]; ok && s.local {
			out[k] = flat[k]
			continue
		}
		warns = append(warns, fmt.Sprintf("ignored %s in %s: not allowed in project config (§18.6)", k, path))
	}
	return out, warns
}

// matchProjects returns one layer per "projects" pattern that matches root.
// Glob patterns come first, sorted. Exact paths come last, so an exact match
// beats a glob. Later layers win.
func matchProjects(projects any, root, globalPath string) ([]layer, []string, error) {
	pm, ok := projects.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("%s: \"projects\" must be a mapping", globalPath)
	}
	if root == "" {
		return nil, nil, nil
	}
	root = filepath.Clean(root)

	var globs, exacts []string
	for pat := range pm {
		if _, err := filepath.Match(pat, ""); err != nil {
			return nil, nil, fmt.Errorf("%s: invalid project pattern %q: %w", globalPath, pat, err)
		}
		if strings.ContainsAny(pat, "*?[") {
			globs = append(globs, pat)
		} else {
			exacts = append(exacts, pat)
		}
	}
	sort.Strings(globs)
	sort.Strings(exacts)

	var (
		out   []layer
		warns []string
	)
	for _, group := range [][]string{globs, exacts} {
		for _, pat := range group {
			if ok, _ := filepath.Match(filepath.Clean(pat), root); !ok {
				continue
			}
			body, ok := pm[pat].(map[string]any)
			if !ok {
				return nil, nil, fmt.Errorf("%s: projects[%q] must be a mapping", globalPath, pat)
			}
			flat := map[string]any{}
			flatten("", body, flat)
			vals := map[string]any{}
			for _, k := range sortedKeys(flat) {
				if _, ok := specByKey[k]; ok {
					vals[k] = flat[k]
					continue
				}
				if isReserved(k) {
					warns = append(warns, fmt.Sprintf("ignored projects[%q].%s in %s: reserved key, not implemented yet", pat, k, globalPath))
					continue
				}
				return nil, nil, fmt.Errorf("%s: projects[%q]: unknown configuration key %q", globalPath, pat, k)
			}
			out = append(out, layer{name: fmt.Sprintf("global:projects[%s]", pat), values: vals})
		}
	}
	return out, warns, nil
}

// validationError names each violated key's value and source.
func validationError(ps []problem, cfg Config, sources map[string]string) error {
	byKey := map[string]string{}
	for _, s := range specs {
		byKey[s.key] = formatValue(s.field(&cfg))
	}
	var parts []string
	for _, p := range ps {
		var details []string
		for _, k := range p.keys {
			src := sources[k]
			if src == "" {
				src = "default"
			}
			details = append(details, fmt.Sprintf("%s=%s (from %s)", k, byKey[k], src))
		}
		parts = append(parts, strings.Join(details, ", ")+": "+p.msg)
	}
	return fmt.Errorf("invalid configuration: %s", strings.Join(parts, "; "))
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

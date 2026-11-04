package attach

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fsvxavier/accordion-context/internal/launcher"
)

// tempFileBase names the temp config file inside the run's temp dir.
const tempFileBase = "client-config"

// degreeByPreference maps integration.prefer names to base URL degrees.
var degreeByPreference = map[string]string{
	"base_url_env":  DegreeBaseURL,
	"base_url_flag": DegreeFlag,
	"temp_config":   DegreeTempConfig,
}

// defaultBaseOrder is the ladder order of the base URL degrees (spec §19.9).
var defaultBaseOrder = []string{"base_url_env", "base_url_flag", "temp_config"}

// declares reports whether the matched profile declares a base URL degree.
func (sc *scope) declares(degree string) bool {
	switch degree {
	case DegreeBaseURL:
		return sc.prof.BaseURLEnv != ""
	case DegreeFlag:
		return sc.prof.BaseURLFlag != ""
	case DegreeTempConfig:
		return sc.prof.TempConfig != nil
	}
	return false
}

// declaredDegrees lists the base URL degrees the matched profile declares.
func (sc *scope) declaredDegrees() []string {
	var out []string
	for _, d := range []string{DegreeBaseURL, DegreeFlag, DegreeTempConfig} {
		if sc.declares(d) {
			out = append(out, d)
		}
	}
	return out
}

// baseOrder returns the declared base URL degrees in the order to try them.
// integration.prefer, when set, selects and orders them; otherwise the ladder
// order applies. The MITM degree always follows them.
func (sc *scope) baseOrder() []string {
	prefs := sc.prof.Integration.Prefer
	if len(prefs) == 0 {
		prefs = defaultBaseOrder
	}
	var out []string
	for _, name := range prefs {
		d, ok := degreeByPreference[name]
		if ok && sc.declares(d) && !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	return out
}

// basePlan builds the plan of one declared base URL degree. For the temp config
// degree it writes the file, so the plan is ready to run.
func (sc *scope) basePlan(degree string) (plan, error) {
	base := launcher.BaseURL(sc.rt.addr, sc.projID, sc.prof)
	switch degree {
	case DegreeBaseURL:
		return sc.baseURLPlan(), nil
	case DegreeFlag:
		token, err := sc.cfgToken()
		if err != nil {
			return plan{}, err
		}
		args, err := launcher.BaseURLFlagArgs(sc.prof, base, sc.port(), token)
		if err != nil {
			return plan{}, fmt.Errorf("base_url_flag: %w", err)
		}
		return plan{
			degree:     degree,
			flags:      args,
			flagsAfter: sc.prof.FlagsAfter(),
			all:        true,
			probes:     []probe{sc.baseProbe(degree, base)},
		}, nil
	case DegreeTempConfig:
		return sc.tempPlan(base)
	}
	return plan{}, fmt.Errorf("unknown degree %q", degree)
}

// tempPlan writes the temp config and points the client at it, through an
// environment variable or a flag. The file is removed with the run's temp dir.
func (sc *scope) tempPlan(base string) (plan, error) {
	t := *sc.prof.TempConfig
	token, err := sc.cfgToken()
	if err != nil {
		return plan{}, err
	}
	content, err := sc.tempContent(t, base, token)
	if err != nil {
		return plan{}, fmt.Errorf("temp_config: %w", err)
	}
	path, err := sc.writeTempFile(t.Format, content)
	if err != nil {
		return plan{}, err
	}
	p := plan{degree: DegreeTempConfig, all: true, probes: []probe{sc.baseProbe(DegreeTempConfig, base)}}
	if t.PathEnv != "" {
		p.env = []kv{{t.PathEnv, path}}
		return p, nil
	}
	args, err := launcher.PathFlagArgs(t, path)
	if err != nil {
		return plan{}, fmt.Errorf("temp_config path_flag: %w", err)
	}
	p.flags = args
	p.flagsAfter = sc.prof.FlagsAfter()
	return p, nil
}

// tempContent returns the file content: the rendered template, or the user's
// merge_from file with the base URL set at key.
func (sc *scope) tempContent(t launcher.TempConfig, base, token string) (string, error) {
	if t.Template != "" {
		return launcher.RenderTempConfig(t, base, sc.port(), token)
	}
	src, err := readMergeSource(t.MergeFrom)
	if err != nil {
		return "", err
	}
	out, err := launcher.MergeConfig(t.Format, src, t.Key, base)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// readMergeSource reads the user's file. It is the only access to that file:
// nothing is ever written to it.
func readMergeSource(path string) ([]byte, error) {
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("merge_from: %w", err)
		}
		path = filepath.Join(home, rest)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("merge_from: %w", err)
	}
	return b, nil
}

// writeTempFile creates the config file in the run's 0700 temp dir with mode
// 0600. It refuses to overwrite an existing file.
func (sc *scope) writeTempFile(format, content string) (string, error) {
	path := filepath.Join(sc.tmp, tempFileBase+launcher.ConfigExt(format))
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("temp config: %w", err)
	}
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("temp config: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("temp config: %w", err)
	}
	return path, nil
}

// cfgToken returns the per-run random value of the {token} placeholder. It is
// not the probe nonce, which is a secret of the proxy.
func (sc *scope) cfgToken() (string, error) {
	if sc.token == "" {
		t, err := randomToken()
		if err != nil {
			return "", err
		}
		sc.token = t
	}
	return sc.token, nil
}

func (sc *scope) port() string {
	_, port, _ := net.SplitHostPort(sc.rt.addr)
	return port
}

// baseProbe sends a probe along a base URL the client gets through a flag or
// a config file.
func (sc *scope) baseProbe(name, base string) probe {
	return probe{name: name, run: func(ctx context.Context) error {
		return sc.rt.probeBase(ctx, base, sc.o.ProbeTimeout)
	}}
}

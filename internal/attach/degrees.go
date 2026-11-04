package attach

import (
	"context"
	"fmt"
	"net"
	"slices"

	"github.com/fsvxavier/accordion-context/internal/launcher"
)

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
	path, err := launcher.WriteTempConfig(sc.tmp, t, base, sc.port(), token)
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

// cfgToken returns the per-run random value of the {token} placeholder. It is
// not the probe nonce, which is a secret of the proxy.
func (sc *scope) cfgToken() (string, error) {
	if sc.token == "" {
		t, err := launcher.RandomToken()
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

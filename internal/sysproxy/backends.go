package sysproxy

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// backendByName builds the backend recorded in a state file, or the one that
// Select would choose on this machine for the same name.
func backendByName(name string, e Env) (Backend, error) {
	switch name {
	case "gnome":
		return &gnome{}, nil
	case "kde5":
		return &kde{read: "kreadconfig5", write: "kwriteconfig5", name: "kde5"}, nil
	case "kde6":
		return &kde{read: "kreadconfig6", write: "kwriteconfig6", name: "kde6"}, nil
	case "darwin":
		return &darwin{}, nil
	case "windows":
		return &windows{}, nil
	}
	return nil, fmt.Errorf("sysproxy: unknown backend %q", name)
}

// Select returns the backend for this machine. Linux uses XDG_CURRENT_DESKTOP
// (GNOME or KDE). KDE 6 is preferred when kwriteconfig6 is installed.
func Select(e Env) (Backend, error) {
	switch e.goos() {
	case "darwin":
		return backendByName("darwin", e)
	case "windows":
		return backendByName("windows", e)
	case "linux":
		desk := strings.ToUpper(e.getenv("XDG_CURRENT_DESKTOP"))
		switch {
		case strings.Contains(desk, "KDE"):
			if e.LookPath != nil && e.LookPath("kwriteconfig6") {
				return backendByName("kde6", e)
			}
			return backendByName("kde5", e)
		case strings.Contains(desk, "GNOME"):
			return backendByName("gnome", e)
		}
		return nil, fmt.Errorf("%w: XDG_CURRENT_DESKTOP=%q is not GNOME or KDE", ErrNoBackend, e.getenv("XDG_CURRENT_DESKTOP"))
	}
	return nil, fmt.Errorf("%w on %s", ErrNoBackend, e.goos())
}

// quoteGVariant wraps s as a GVariant string literal.
func quoteGVariant(s string) string { return "'" + s + "'" }

// gnome drives org.gnome.system.proxy with gsettings. Values are GVariant text,
// so the raw output of `gsettings get` is written back unchanged on restore.
type gnome struct{}

const (
	gnomeSchema = "org.gnome.system.proxy"
	gnomeHTTPS  = "org.gnome.system.proxy.https"
)

// gnomeKeys maps backend keys to gsettings schema and key.
var gnomeKeys = map[string][2]string{
	"gnome:mode":       {gnomeSchema, "mode"},
	"gnome:ignore":     {gnomeSchema, "ignore-hosts"},
	"gnome:https.host": {gnomeHTTPS, "host"},
	"gnome:https.port": {gnomeHTTPS, "port"},
}

func (*gnome) Name() string { return "gnome" }

func (*gnome) Plan(_ context.Context, _ Runner, ep Endpoint) ([]Setting, error) {
	if err := ep.Validate(); err != nil {
		return nil, err
	}
	var ignore []string
	for _, b := range ep.Bypass {
		ignore = append(ignore, quoteGVariant(b))
	}
	return []Setting{
		{Key: "gnome:mode", Value: quoteGVariant("manual"), Present: true},
		{Key: "gnome:ignore", Value: "[" + strings.Join(ignore, ", ") + "]", Present: true},
		{Key: "gnome:https.host", Value: quoteGVariant(ep.Host), Present: true},
		{Key: "gnome:https.port", Value: strconv.Itoa(ep.Port), Present: true},
	}, nil
}

func (*gnome) Read(ctx context.Context, r Runner, key string) (Setting, error) {
	k, ok := gnomeKeys[key]
	if !ok {
		return Setting{}, fmt.Errorf("gnome: unknown key %q", key)
	}
	out, err := r.Run(ctx, Command{Name: "gsettings", Args: []string{"get", k[0], k[1]}})
	if err != nil {
		return Setting{}, err
	}
	return Setting{Key: key, Value: strings.TrimSpace(out), Present: true}, nil
}

func (*gnome) Write(s Setting) ([]Command, error) {
	k, ok := gnomeKeys[s.Key]
	if !ok {
		return nil, fmt.Errorf("gnome: unknown key %q", s.Key)
	}
	if !s.Present {
		return nil, fmt.Errorf("gnome: %s cannot be unset", s.Key)
	}
	return []Command{{Name: "gsettings", Args: []string{"set", k[0], k[1], s.Value}}}, nil
}

// kde writes kioslaverc with kwriteconfig, then asks KIO to reread it.
type kde struct {
	read, write, name string
}

const (
	kdeFile  = "kioslaverc"
	kdeGroup = "Proxy Settings"
	// kdeUnset is printed by kreadconfig for a missing key, through --default.
	kdeUnset = "__acd_ctx_unset__"
)

var kdeKeys = map[string]string{
	"kde:ProxyType":  "ProxyType",
	"kde:httpsProxy": "httpsProxy",
	"kde:NoProxyFor": "NoProxyFor",
}

func (k *kde) Name() string { return k.name }

func (*kde) Plan(_ context.Context, _ Runner, ep Endpoint) ([]Setting, error) {
	if err := ep.Validate(); err != nil {
		return nil, err
	}
	return []Setting{
		{Key: "kde:ProxyType", Value: "1", Present: true},
		{Key: "kde:httpsProxy", Value: fmt.Sprintf("http://%s:%d", ep.Host, ep.Port), Present: true},
		{Key: "kde:NoProxyFor", Value: strings.Join(ep.Bypass, ","), Present: true},
	}, nil
}

func (k *kde) Read(ctx context.Context, r Runner, key string) (Setting, error) {
	name, ok := kdeKeys[key]
	if !ok {
		return Setting{}, fmt.Errorf("kde: unknown key %q", key)
	}
	out, err := r.Run(ctx, Command{Name: k.read, Args: []string{
		"--file", kdeFile, "--group", kdeGroup, "--key", name, "--default", kdeUnset,
	}})
	if err != nil {
		return Setting{}, err
	}
	v := strings.TrimRight(out, "\n")
	if v == kdeUnset {
		return Setting{Key: key, Present: false}, nil
	}
	return Setting{Key: key, Value: v, Present: true}, nil
}

func (k *kde) Write(s Setting) ([]Command, error) {
	name, ok := kdeKeys[s.Key]
	if !ok {
		return nil, fmt.Errorf("kde: unknown key %q", s.Key)
	}
	base := []string{"--file", kdeFile, "--group", kdeGroup, "--key", name}
	var c Command
	if s.Present {
		c = Command{Name: k.write, Args: append(base, s.Value)}
	} else {
		c = Command{Name: k.write, Args: append(base, "--delete")}
	}
	reload := Command{Name: "dbus-send", Args: []string{
		"--session", "--type=signal", "/KIO/Scheduler",
		"org.kde.KIO.Scheduler.reparseSlaveConfiguration", "string:",
	}}
	return []Command{c, reload}, nil
}

// darwin uses networksetup on every active network service. A key is
// "macos|<service>|https" or "macos|<service>|bypass". The https value is
// "<enabled 0|1>\t<server>\t<port>", and bypass is one domain per line.
type darwin struct{}

const darwinPrefix = "macos|"

func (*darwin) Name() string { return "darwin" }

func darwinKey(svc, field string) string { return darwinPrefix + svc + "|" + field }

func (*darwin) Plan(ctx context.Context, r Runner, ep Endpoint) ([]Setting, error) {
	if err := ep.Validate(); err != nil {
		return nil, err
	}
	list, err := r.Run(ctx, Command{Name: "networksetup", Args: []string{"-listallnetworkservices"}})
	if err != nil {
		return nil, err
	}
	var out []Setting
	for _, svc := range darwinActiveServices(ctx, r, list) {
		out = append(out,
			Setting{Key: darwinKey(svc, "https"), Value: fmt.Sprintf("1\t%s\t%d", ep.Host, ep.Port), Present: true},
			Setting{Key: darwinKey(svc, "bypass"), Value: strings.Join(ep.Bypass, "\n"), Present: true},
		)
	}
	if len(out) == 0 {
		return nil, errors.New("networksetup: no active network service found")
	}
	return out, nil
}

// darwinActiveServices returns the enabled services that hold an IP address.
func darwinActiveServices(ctx context.Context, r Runner, list string) []string {
	var active []string
	for i, line := range strings.Split(list, "\n") {
		line = strings.TrimSpace(line)
		if i == 0 && strings.HasPrefix(line, "An asterisk") || line == "" || strings.HasPrefix(line, "*") || strings.HasPrefix(line, "-") {
			continue
		}
		info, err := r.Run(ctx, Command{Name: "networksetup", Args: []string{"-getinfo", line}})
		if err != nil {
			continue
		}
		if ip := kvValue(info, "IP address"); ip != "" && ip != "none" {
			active = append(active, line)
		}
	}
	return active
}

// kvValue returns the value of the first "Name: value" line with that name.
func kvValue(text, name string) string {
	for _, line := range strings.Split(text, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(k) == name {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func (*darwin) Read(ctx context.Context, r Runner, key string) (Setting, error) {
	if !strings.HasPrefix(key, darwinPrefix) {
		return Setting{}, fmt.Errorf("macos: unknown key %q", key)
	}
	rest := strings.TrimPrefix(key, darwinPrefix)
	i := strings.LastIndexByte(rest, '|')
	if i < 0 {
		return Setting{}, fmt.Errorf("macos: malformed key %q", key)
	}
	svc, field := rest[:i], rest[i+1:]
	if svc == "" || strings.HasPrefix(svc, "-") {
		return Setting{}, fmt.Errorf("macos: invalid service name %q", svc)
	}
	switch field {
	case "https":
		out, err := r.Run(ctx, Command{Name: "networksetup", Args: []string{"-getsecurewebproxy", svc}})
		if err != nil {
			return Setting{}, err
		}
		enabled := "0"
		if strings.EqualFold(kvValue(out, "Enabled"), "Yes") {
			enabled = "1"
		}
		return Setting{Key: key, Present: true, Value: fmt.Sprintf("%s\t%s\t%s", enabled, kvValue(out, "Server"), kvValue(out, "Port"))}, nil
	case "bypass":
		out, err := r.Run(ctx, Command{Name: "networksetup", Args: []string{"-getproxybypassdomains", svc}})
		if err != nil {
			return Setting{}, err
		}
		var doms []string
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "There aren't") {
				doms = append(doms, line)
			}
		}
		return Setting{Key: key, Present: true, Value: strings.Join(doms, "\n")}, nil
	}
	return Setting{}, fmt.Errorf("macos: unknown field %q", field)
}

func (*darwin) Write(s Setting) ([]Command, error) {
	if !strings.HasPrefix(s.Key, darwinPrefix) || !s.Present {
		return nil, fmt.Errorf("macos: cannot write %q", s.Key)
	}
	rest := strings.TrimPrefix(s.Key, darwinPrefix)
	i := strings.LastIndexByte(rest, '|')
	if i < 0 {
		return nil, fmt.Errorf("macos: malformed key %q", s.Key)
	}
	svc, field := rest[:i], rest[i+1:]
	if svc == "" || strings.HasPrefix(svc, "-") {
		return nil, fmt.Errorf("macos: invalid service name %q", svc)
	}
	switch field {
	case "https":
		parts := strings.Split(s.Value, "\t")
		if len(parts) != 3 {
			return nil, fmt.Errorf("macos: malformed https value for %q", svc)
		}
		var cmds []Command
		if parts[1] != "" {
			cmds = append(cmds, Command{Name: "networksetup", Args: []string{"-setsecurewebproxy", svc, parts[1], parts[2]}})
		}
		state := "off"
		if parts[0] == "1" {
			state = "on"
		}
		return append(cmds, Command{Name: "networksetup", Args: []string{"-setsecurewebproxystate", svc, state}}), nil
	case "bypass":
		doms := strings.Split(s.Value, "\n")
		if s.Value == "" {
			doms = []string{"Empty"}
		}
		return []Command{{Name: "networksetup", Args: append([]string{"-setproxybypassdomains", svc}, doms...)}}, nil
	}
	return nil, fmt.Errorf("macos: unknown field %q", field)
}

// windows writes the per-user Internet Settings values with reg.exe.
type windows struct{}

const winKey = `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`

// winValues lists the values acd-ctx manages and their registry types.
var winValues = map[string]string{
	"win:ProxyEnable":   "REG_DWORD",
	"win:ProxyServer":   "REG_SZ",
	"win:ProxyOverride": "REG_SZ",
}

var winNames = map[string]string{
	"win:ProxyEnable":   "ProxyEnable",
	"win:ProxyServer":   "ProxyServer",
	"win:ProxyOverride": "ProxyOverride",
}

func (*windows) Name() string { return "windows" }

func (*windows) Plan(_ context.Context, _ Runner, ep Endpoint) ([]Setting, error) {
	if err := ep.Validate(); err != nil {
		return nil, err
	}
	override := []string{}
	for _, b := range ep.Bypass {
		switch b {
		case "localhost":
			override = append(override, "localhost")
		case "127.0.0.0/8":
			override = append(override, "127.*")
		case "::1":
			override = append(override, "[::1]")
		default:
			override = append(override, b)
		}
	}
	return []Setting{
		{Key: "win:ProxyEnable", Value: "1", Present: true},
		// The https= form limits the proxy to HTTPS. A bare host:port would also
		// send plain HTTP to the listener, which answers it with 405.
		{Key: "win:ProxyServer", Value: fmt.Sprintf("https=%s:%d", ep.Host, ep.Port), Present: true},
		{Key: "win:ProxyOverride", Value: strings.Join(override, ";"), Present: true},
	}, nil
}

func (*windows) Read(ctx context.Context, r Runner, key string) (Setting, error) {
	name, ok := winNames[key]
	if !ok {
		return Setting{}, fmt.Errorf("windows: unknown key %q", key)
	}
	out, err := r.Run(ctx, Command{Name: "reg", Args: []string{"query", winKey, "/v", name}})
	var ee *ExitError
	if errors.As(err, &ee) && ee.Code == 1 {
		// reg query exits 1 when the value does not exist.
		return Setting{Key: key, Present: false}, nil
	}
	if err != nil {
		return Setting{}, err
	}
	re := regexp.MustCompile(`(?m)^\s+` + regexp.QuoteMeta(name) + `\s+REG_\w+\s+(.*?)\s*$`)
	m := re.FindStringSubmatch(out)
	if m == nil {
		return Setting{}, fmt.Errorf("windows: value %s not found in reg output", name)
	}
	val := m[1]
	if winValues[key] == "REG_DWORD" {
		v, err := strconv.ParseUint(val, 0, 32)
		if err != nil {
			return Setting{}, fmt.Errorf("windows: %s: %w", name, err)
		}
		val = strconv.FormatUint(v, 10)
	}
	return Setting{Key: key, Value: val, Present: true}, nil
}

func (*windows) Write(s Setting) ([]Command, error) {
	name, ok := winNames[s.Key]
	if !ok {
		return nil, fmt.Errorf("windows: unknown key %q", s.Key)
	}
	if !s.Present {
		return []Command{{Name: "reg", Args: []string{"delete", winKey, "/v", name, "/f"}}}, nil
	}
	return []Command{{Name: "reg", Args: []string{"add", winKey, "/v", name, "/t", winValues[s.Key], "/d", s.Value, "/f"}}}, nil
}

package sysproxy

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// scripted answers commands by their exact "name arg..." line.
type scripted struct {
	out  map[string]string
	fail map[string]error
	seen []string
}

func (s *scripted) Run(_ context.Context, c Command) (string, error) {
	line := strings.Join(append([]string{c.Name}, c.Args...), " ")
	s.seen = append(s.seen, line)
	if err, ok := s.fail[line]; ok {
		return "", err
	}
	if out, ok := s.out[line]; ok {
		return out, nil
	}
	return "", &ExitError{Command: c, Code: 1, Output: "unscripted: " + line}
}

func TestSelectBackend(t *testing.T) {
	env := func(goos, desk string, kwrite6 bool) Env {
		return Env{
			GOOS:     goos,
			Getenv:   func(k string) string { return map[string]string{"XDG_CURRENT_DESKTOP": desk}[k] },
			LookPath: func(n string) bool { return kwrite6 && n == "kwriteconfig6" },
		}
	}
	cases := []struct {
		name string
		e    Env
		want string
	}{
		{"gnome", env("linux", "GNOME", false), "gnome"},
		{"ubuntu gnome", env("linux", "ubuntu:GNOME", false), "gnome"},
		{"kde5", env("linux", "KDE", false), "kde5"},
		{"kde6", env("linux", "KDE", true), "kde6"},
		{"darwin", env("darwin", "", false), "darwin"},
		{"windows", env("windows", "", false), "windows"},
	}
	for _, c := range cases {
		b, err := Select(c.e)
		if err != nil || b.Name() != c.want {
			t.Fatalf("%s: got %v, %v; want %s", c.name, nameOf(b), err, c.want)
		}
	}
	if _, err := Select(env("linux", "XFCE", false)); !errors.Is(err, ErrNoBackend) {
		t.Fatalf("xfce must have no backend, got %v", err)
	}
	if _, err := Select(env("freebsd", "", false)); !errors.Is(err, ErrNoBackend) {
		t.Fatalf("freebsd must have no backend, got %v", err)
	}
}

func nameOf(b Backend) string {
	if b == nil {
		return "<nil>"
	}
	return b.Name()
}

func TestKDEArgv(t *testing.T) {
	b, err := backendByName("kde6", Env{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	s := &scripted{out: map[string]string{
		"kreadconfig6 --file kioslaverc --group Proxy Settings --key ProxyType --default __acd_ctx_unset__":  "0\n",
		"kreadconfig6 --file kioslaverc --group Proxy Settings --key httpsProxy --default __acd_ctx_unset__": "__acd_ctx_unset__\n",
		"kreadconfig6 --file kioslaverc --group Proxy Settings --key NoProxyFor --default __acd_ctx_unset__": "localhost\n",
	}}
	ep := Endpoint{Host: "127.0.0.1", Port: 4000, Bypass: []string{"localhost", "::1"}}
	desired, err := b.Plan(ctx, s, ep)
	if err != nil {
		t.Fatal(err)
	}
	if desired[1].Value != "http://127.0.0.1:4000" || desired[2].Value != "localhost,::1" {
		t.Fatalf("plan = %+v", desired)
	}
	pt, _ := b.Read(ctx, s, "kde:ProxyType")
	if !pt.Present || pt.Value != "0" {
		t.Fatalf("ProxyType read = %+v", pt)
	}
	hp, _ := b.Read(ctx, s, "kde:httpsProxy")
	if hp.Present {
		t.Fatalf("missing key read as present: %+v", hp)
	}
	cmds, err := b.Write(desired[0])
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, c := range cmds {
		got = append(got, c.String())
	}
	want := []string{
		"kwriteconfig6 --file kioslaverc --group Proxy Settings --key ProxyType 1",
		"dbus-send --session --type=signal /KIO/Scheduler org.kde.KIO.Scheduler.reparseSlaveConfiguration string:",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("write argv\n got %q\nwant %q", got, want)
	}
	del, _ := b.Write(Setting{Key: "kde:httpsProxy", Present: false})
	if del[0].String() != "kwriteconfig6 --file kioslaverc --group Proxy Settings --key httpsProxy --delete" {
		t.Fatalf("delete argv = %s", del[0])
	}
}

func TestKDE5UsesKwriteconfig5(t *testing.T) {
	b, _ := backendByName("kde5", Env{})
	cmds, _ := b.Write(Setting{Key: "kde:ProxyType", Value: "1", Present: true})
	if cmds[0].Name != "kwriteconfig5" {
		t.Fatalf("kde5 writes with %s", cmds[0].Name)
	}
}

func TestDarwinPlanReadWrite(t *testing.T) {
	ctx := context.Background()
	s := &scripted{out: map[string]string{
		"networksetup -listallnetworkservices":      "An asterisk (*) denotes that a network service is disabled.\nWi-Fi\n*USB 2\nThunderbolt\n",
		"networksetup -getinfo Wi-Fi":               "DHCP Configuration\nIP address: 192.168.0.9\nSubnet mask: 255.255.255.0\n",
		"networksetup -getinfo Thunderbolt":         "IP address: none\n",
		"networksetup -getsecurewebproxy Wi-Fi":     "Enabled: No\nServer: \nPort: 0\nAuthenticated Proxy Enabled: 0\n",
		"networksetup -getproxybypassdomains Wi-Fi": "There aren't any bypass domains set on Wi-Fi.\n",
	}}
	b, err := backendByName("darwin", Env{})
	if err != nil {
		t.Fatal(err)
	}
	desired, err := b.Plan(ctx, s, Endpoint{Host: "127.0.0.1", Port: 4444, Bypass: []string{"localhost", "::1"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(desired) != 2 || desired[0].Key != "macos|Wi-Fi|https" || desired[0].Value != "1\t127.0.0.1\t4444" {
		t.Fatalf("plan = %+v (only the active service may be planned)", desired)
	}
	prev, err := b.Read(ctx, s, "macos|Wi-Fi|https")
	if err != nil || prev.Value != "0\t\t0" {
		t.Fatalf("https read = %+v, %v", prev, err)
	}
	bp, err := b.Read(ctx, s, "macos|Wi-Fi|bypass")
	if err != nil || bp.Value != "" {
		t.Fatalf("bypass read = %+v, %v", bp, err)
	}

	cmds, err := b.Write(desired[0])
	if err != nil {
		t.Fatal(err)
	}
	if got := cmdLines(cmds); !reflect.DeepEqual(got, []string{
		"networksetup -setsecurewebproxy Wi-Fi 127.0.0.1 4444",
		"networksetup -setsecurewebproxystate Wi-Fi on",
	}) {
		t.Fatalf("https write = %q", got)
	}
	// Restoring the disabled, unconfigured state only switches the proxy off.
	cmds, _ = b.Write(prev)
	if got := cmdLines(cmds); !reflect.DeepEqual(got, []string{"networksetup -setsecurewebproxystate Wi-Fi off"}) {
		t.Fatalf("restore write = %q", got)
	}
	cmds, _ = b.Write(desired[1])
	if got := cmdLines(cmds); !reflect.DeepEqual(got, []string{"networksetup -setproxybypassdomains Wi-Fi localhost ::1"}) {
		t.Fatalf("bypass write = %q", got)
	}
	cmds, _ = b.Write(Setting{Key: "macos|Wi-Fi|bypass", Value: "", Present: true})
	if got := cmdLines(cmds); !reflect.DeepEqual(got, []string{"networksetup -setproxybypassdomains Wi-Fi Empty"}) {
		t.Fatalf("empty bypass write = %q", got)
	}
}

func cmdLines(cmds []Command) []string {
	out := []string{}
	for _, c := range cmds {
		out = append(out, c.String())
	}
	return out
}

func TestWindowsArgvAndRead(t *testing.T) {
	ctx := context.Background()
	b, _ := backendByName("windows", Env{})
	desired, err := b.Plan(ctx, nil, Endpoint{Host: "127.0.0.1", Port: 4321, Bypass: []string{"localhost", "127.0.0.0/8", "::1"}})
	if err != nil {
		t.Fatal(err)
	}
	if desired[1].Value != "https=127.0.0.1:4321" || desired[2].Value != "localhost;127.*;[::1]" {
		t.Fatalf("plan = %+v", desired)
	}
	cmds, _ := b.Write(desired[0])
	if got := cmdLines(cmds); !reflect.DeepEqual(got, []string{
		`reg add HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings /v ProxyEnable /t REG_DWORD /d 1 /f`,
	}) {
		t.Fatalf("enable write = %q", got)
	}
	del, _ := b.Write(Setting{Key: "win:ProxyServer", Present: false})
	if got := cmdLines(del); got[0] != `reg delete HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings /v ProxyServer /f` {
		t.Fatalf("delete = %q", got)
	}

	const q = `reg query HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings /v `
	s := &scripted{
		out: map[string]string{
			q + "ProxyEnable": "\r\nHKEY_CURRENT_USER\\...\\Internet Settings\r\n    ProxyEnable    REG_DWORD    0x0\r\n\r\n",
			q + "ProxyServer": "    ProxyServer    REG_SZ    https=proxy.corp:3128\r\n",
		},
		fail: map[string]error{
			q + "ProxyOverride": &ExitError{Code: 1, Output: "ERROR: The system was unable to find the specified registry key or value."},
		},
	}
	en, err := b.Read(ctx, s, "win:ProxyEnable")
	if err != nil || en.Value != "0" || !en.Present {
		t.Fatalf("ProxyEnable = %+v, %v", en, err)
	}
	srv, err := b.Read(ctx, s, "win:ProxyServer")
	if err != nil || srv.Value != "https=proxy.corp:3128" {
		t.Fatalf("ProxyServer = %+v, %v", srv, err)
	}
	ov, err := b.Read(ctx, s, "win:ProxyOverride")
	if err != nil || ov.Present {
		t.Fatalf("missing ProxyOverride must be absent, got %+v, %v", ov, err)
	}
}

func TestGnomeWriteRefusesUnset(t *testing.T) {
	b, _ := backendByName("gnome", Env{})
	if _, err := b.Write(Setting{Key: "gnome:mode", Present: false}); err == nil {
		t.Fatal("gnome must not unset a key")
	}
}

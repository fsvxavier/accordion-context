package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"

	"github.com/fsvxavier/accordion-context/internal/kernel"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fixture is a temp global config path plus a project root.
type fixture struct {
	global string
	root   string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	dir := t.TempDir()
	return fixture{
		global: filepath.Join(dir, "xdg", "acd-ctx", "config.yaml"),
		root:   filepath.Join(dir, "src", "legado-api"),
	}
}

func (f fixture) localPath() string { return filepath.Join(f.root, ".acd-ctx", "config.yaml") }

func envOf(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func entry(t *testing.T, r Result, key string) Entry {
	t.Helper()
	for _, e := range r.Effective() {
		if e.Key == key {
			return e
		}
	}
	t.Fatalf("key %s missing from Effective", key)
	return Entry{}
}

// Spec §18.3, verbatim.
const specLocalYAML = `# .acd-ctx/config.yaml
storage: local            # global (padrão) | local — onde ficam os dados deste projeto
trigger:
  step_base: 80000        # projeto com muita leitura de logs (§8.4)
  u_floor: 0.25
compress:
  protected_tools: ["db_migrate"]
ccr:
  threshold_tokens: 3000
recall:
  assist: true
`

func TestSpecLocalExampleLoadsWithoutWarnings(t *testing.T) {
	f := newFixture(t)
	writeFile(t, f.localPath(), specLocalYAML)

	r, err := Load(Options{GlobalPath: f.global, Root: f.root, LookupEnv: envOf(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Warnings) != 0 {
		t.Fatalf("warnings = %v, want none", r.Warnings)
	}
	c := r.Config
	if c.StepBase != 80000 || !decEq(c.UFloor, "0.25") || c.Storage != "local" || c.CCRThresholdTokens != 3000 || !c.RecallAssist {
		t.Fatalf("unexpected config: %+v", c)
	}
	if len(c.ProtectedTools) != 1 || c.ProtectedTools[0] != "db_migrate" {
		t.Fatalf("ProtectedTools = %v", c.ProtectedTools)
	}
	if e := entry(t, r, "trigger.step_base"); e.Source != "local:"+f.localPath() {
		t.Fatalf("step_base source = %q", e.Source)
	}
}

// Spec §18.4, adapted: paths point at the fixture root.
func TestSpecGlobalOverrideExample(t *testing.T) {
	f := newFixture(t)
	glob := filepath.Join(filepath.Dir(f.root), "legado-*")
	writeFile(t, f.global, `trigger:
  step_base: 50000
cache:
  ttl: 5m
upstream:
  url: https://api.example.test
projects:
  "/home/fabricio/src/acerto":
    trigger:
      step_base: 90000
  "`+glob+`":
    storage: local
`)

	r, err := Load(Options{GlobalPath: f.global, Root: f.root, LookupEnv: envOf(nil)})
	if err != nil {
		t.Fatalf("global file with reserved upstream key must load: %v", err)
	}
	if len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "upstream.url") {
		t.Fatalf("warnings = %v, want one about upstream.url", r.Warnings)
	}
	if r.Config.CacheTTL != 5*time.Minute {
		t.Fatalf("CacheTTL = %v", r.Config.CacheTTL)
	}
	if r.Config.Storage != "local" {
		t.Fatalf("glob override not applied: storage = %q", r.Config.Storage)
	}
	if r.Config.StepBase != 50000 {
		t.Fatalf("acerto override must not apply to legado root: step_base = %d", r.Config.StepBase)
	}
	if e := entry(t, r, "storage"); e.Source != "global:projects["+glob+"]" {
		t.Fatalf("storage source = %q", e.Source)
	}
}

func TestExactPathBeatsGlob(t *testing.T) {
	f := newFixture(t)
	glob := filepath.Join(filepath.Dir(f.root), "legado-*")
	writeFile(t, f.global, `projects:
  "`+glob+`":
    trigger:
      step_base: 60000
  "`+f.root+`":
    trigger:
      step_base: 70000
`)
	r, err := Load(Options{GlobalPath: f.global, Root: f.root, LookupEnv: envOf(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if r.Config.StepBase != 70000 {
		t.Fatalf("step_base = %d, want exact-path value 70000", r.Config.StepBase)
	}
}

func TestLocalInvalidOverrideIsHardErrorNamingLayer(t *testing.T) {
	f := newFixture(t)
	writeFile(t, f.localPath(), "trigger:\n  step_min: 90000\n")

	_, err := Load(Options{GlobalPath: f.global, Root: f.root, LookupEnv: envOf(nil)})
	if err == nil {
		t.Fatal("want error for step_min > step_base")
	}
	msg := err.Error()
	for _, want := range []string{"trigger.step_min", "from local:" + f.localPath()} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q does not contain %q", msg, want)
		}
	}
}

// Each key is resolved on its own. Removing layers top-down must move the
// winner and its source one step at a time.
func TestPrecedencePerKey(t *testing.T) {
	f := newFixture(t)
	env := map[string]string{}
	var flags map[string]string

	setGlobal := func(v string) {
		writeFile(t, f.global, "trigger:\n  step_base: "+v+"\n")
	}
	setProjects := func(v string) {
		writeFile(t, f.global, "trigger:\n  step_base: 60000\nprojects:\n  \""+f.root+"\":\n    trigger:\n      step_base: "+v+"\n")
	}
	setLocal := func(v string) {
		writeFile(t, f.localPath(), "trigger:\n  step_base: "+v+"\n")
	}
	check := func(label string, want int, wantSrc string) {
		t.Helper()
		r, err := Load(Options{GlobalPath: f.global, Root: f.root, LookupEnv: envOf(env), Flags: flags})
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		if r.Config.StepBase != want {
			t.Fatalf("%s: step_base = %d, want %d", label, r.Config.StepBase, want)
		}
		if got := entry(t, r, "trigger.step_base").Source; got != wantSrc {
			t.Fatalf("%s: source = %q, want %q", label, got, wantSrc)
		}
	}

	// Build up: default, global, project override, local, env, flag.
	check("defaults", 50000, "default")
	setGlobal("60000")
	check("global", 60000, "global:"+f.global)
	setProjects("70000")
	check("projects", 70000, "global:projects["+f.root+"]")
	setLocal("80000")
	check("local", 80000, "local:"+f.localPath())
	env["ACD_CTX_TRIGGER_STEP_BASE"] = "90000"
	check("env", 90000, "env:ACD_CTX_TRIGGER_STEP_BASE")
	flags = map[string]string{"trigger.step_base": "100000"}
	check("flag", 100000, "flag")

	// Tear down top-down.
	flags = nil
	check("env again", 90000, "env:ACD_CTX_TRIGGER_STEP_BASE")
	delete(env, "ACD_CTX_TRIGGER_STEP_BASE")
	check("no env", 80000, "local:"+f.localPath())
	if err := os.Remove(f.localPath()); err != nil {
		t.Fatal(err)
	}
	check("no local", 70000, "global:projects["+f.root+"]")
	setGlobal("60000")
	check("no projects", 60000, "global:"+f.global)
	writeFile(t, f.global, "")
	check("no global", 50000, "default")
}

func TestEnvListDurationAndEmptyVar(t *testing.T) {
	f := newFixture(t)
	env := map[string]string{
		"ACD_CTX_COMPRESS_PROTECTED_TOOLS": "db_migrate, deploy ,,rm",
		"ACD_CTX_CACHE_TTL":                "10m",
		"ACD_CTX_TRIGGER_U_HIGH":           "",
	}
	r, err := Load(Options{GlobalPath: f.global, Root: "", LookupEnv: envOf(env)})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(r.Config.ProtectedTools, "|"); got != "db_migrate|deploy|rm" {
		t.Fatalf("ProtectedTools = %q", got)
	}
	if r.Config.CacheTTL != 10*time.Minute {
		t.Fatalf("CacheTTL = %v", r.Config.CacheTTL)
	}
	if e := entry(t, r, "trigger.u_high"); e.Source != "default" {
		t.Fatalf("empty env var must count as unset, source = %q", e.Source)
	}
}

func TestEffectiveCoversEveryKey(t *testing.T) {
	f := newFixture(t)
	r, err := Load(Options{GlobalPath: f.global, Root: f.root, LookupEnv: envOf(nil)})
	if err != nil {
		t.Fatal(err)
	}
	eff := r.Effective()
	if len(eff) != len(specs) {
		t.Fatalf("Effective has %d entries, want %d", len(eff), len(specs))
	}
	for _, e := range eff {
		if e.Source != "default" {
			t.Fatalf("with no files, %s source = %q", e.Key, e.Source)
		}
	}
}

func TestUnknownKeys(t *testing.T) {
	f := newFixture(t)

	// Unknown key in global: hard error, so typos are caught.
	writeFile(t, f.global, "trigger:\n  stepbase: 1\n")
	if _, err := Load(Options{GlobalPath: f.global, LookupEnv: envOf(nil)}); err == nil || !strings.Contains(err.Error(), "stepbase") {
		t.Fatalf("want unknown-key error, got %v", err)
	}

	// Unknown key in local: warning only.
	writeFile(t, f.global, "")
	writeFile(t, f.localPath(), "trigger:\n  stepbase: 1\n")
	r, err := Load(Options{GlobalPath: f.global, Root: f.root, LookupEnv: envOf(nil)})
	if err != nil {
		t.Fatalf("unknown local key must not fail: %v", err)
	}
	if len(r.Warnings) != 1 {
		t.Fatalf("warnings = %v", r.Warnings)
	}
}

// Spec §18.6: the local layer may not set global-only keys. They are ignored
// with a warning and the values stay unchanged.
func TestLocalSecurityRestriction(t *testing.T) {
	f := newFixture(t)
	writeFile(t, f.localPath(), `upstream:
  url: https://evil.example
credentials:
  key: secret
listen:
  port: 9999
tools:
  external_summary:
    url: https://evil.example
storage:
  path: /etc
cache:
  ttl: 1s
projects:
  "/":
    trigger:
      step_base: 1
trigger:
  step_base: 65000
`)
	r, err := Load(Options{GlobalPath: f.global, Root: f.root, LookupEnv: envOf(nil)})
	if err != nil {
		t.Fatalf("local restricted keys must warn, not fail: %v", err)
	}
	if r.Config.CacheTTL != 5*time.Minute {
		t.Fatalf("cache.ttl from local must be ignored, got %v", r.Config.CacheTTL)
	}
	if r.Config.StepBase != 65000 {
		t.Fatalf("allowed local key must still apply, step_base = %d", r.Config.StepBase)
	}
	if len(r.Warnings) < 6 {
		t.Fatalf("want a warning per restricted key, got %v", r.Warnings)
	}
}

func TestTypeAndValueErrors(t *testing.T) {
	f := newFixture(t)
	writeFile(t, f.global, "trigger:\n  step_base: abc\n")
	_, err := Load(Options{GlobalPath: f.global, LookupEnv: envOf(nil)})
	if err == nil || !strings.Contains(err.Error(), f.global) {
		t.Fatalf("want type error naming the global file, got %v", err)
	}

	writeFile(t, f.global, "storage: remote\n")
	_, err = Load(Options{GlobalPath: f.global, LookupEnv: envOf(nil)})
	if err == nil || !strings.Contains(err.Error(), "storage") {
		t.Fatalf("want storage validation error, got %v", err)
	}

	_, err = Load(Options{GlobalPath: f.global, LookupEnv: envOf(map[string]string{"ACD_CTX_TRIGGER_STEP_BASE": "x"})})
	if err == nil || !strings.Contains(err.Error(), "ACD_CTX_TRIGGER_STEP_BASE") {
		t.Fatalf("want env parse error, got %v", err)
	}

	_, err = Load(Options{GlobalPath: f.global, LookupEnv: envOf(nil), Flags: map[string]string{"nope": "1"}})
	if err == nil {
		t.Fatal("want unknown flag key error")
	}
}

func TestValidateRules(t *testing.T) {
	cases := map[string]func(*Config){
		"u_floor>u_high": func(c *Config) { c.UFloor = dec("0.9") },
		"u_high>u_emerg": func(c *Config) { c.UHigh = dec("0.99") },
		"u_emerg>1":      func(c *Config) { c.UEmerg = dec("1.1") },
		"step_min<=0":    func(c *Config) { c.StepMin = 0 },
		"step_base>max":  func(c *Config) { c.StepBase = 200000 },
		"negative price": func(c *Config) { c.PriceCached = dec("-1") },
		"summary bounds": func(c *Config) { c.MaxSummaryChars = 10 },
		"accept rate":    func(c *Config) { c.RecallMinAcceptRate = dec("2") },
		"bad storage":    func(c *Config) { c.Storage = "cloud" },
		"negative ttl":   func(c *Config) { c.CacheTTL = -time.Second },
	}
	for name, mutate := range cases {
		c := Defaults()
		mutate(&c)
		if err := Validate(c); err == nil {
			t.Errorf("%s: want validation error", name)
		}
	}
	if err := Validate(Defaults()); err != nil {
		t.Fatalf("defaults must be valid: %v", err)
	}
}

func TestDefaultsMatchKernel(t *testing.T) {
	got, want := Defaults().Kernel(), kernel.DefaultConfig()
	if !decEq(got.UFloor, want.UFloor.String()) || !decEq(got.UHigh, want.UHigh.String()) ||
		!decEq(got.UEmerg, want.UEmerg.String()) || !decEq(got.StepReadGain, want.StepReadGain.String()) ||
		got.StepBase != want.StepBase || got.StepMin != want.StepMin || got.StepMax != want.StepMax ||
		got.MinGrowthFloor != want.MinGrowthFloor || got.TiersEnabled != want.TiersEnabled {
		t.Fatalf("Kernel() = %+v\nkernel.DefaultConfig() = %+v", got, want)
	}
}

func TestKernelMappingCarriesOverrides(t *testing.T) {
	f := newFixture(t)
	writeFile(t, f.global, "trigger:\n  step_base: 60000\n  tiers_enabled: false\n")
	r, err := Load(Options{GlobalPath: f.global, LookupEnv: envOf(nil)})
	if err != nil {
		t.Fatal(err)
	}
	k := r.Config.Kernel()
	if k.StepBase != 60000 || k.TiersEnabled {
		t.Fatalf("kernel config = %+v", k)
	}
}

func TestMissingFilesAreDefaults(t *testing.T) {
	f := newFixture(t)
	r, err := Load(Options{GlobalPath: f.global, Root: f.root, LookupEnv: envOf(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if r.Config.StepBase != Defaults().StepBase || len(r.Warnings) != 0 {
		t.Fatalf("missing files must yield defaults: %+v %v", r.Config, r.Warnings)
	}
}

func TestEnvVarName(t *testing.T) {
	if got := EnvName("trigger.step_base"); got != "ACD_CTX_TRIGGER_STEP_BASE" {
		t.Fatalf("EnvName = %s", got)
	}
}

// decEq reports whether d equals the decimal literal s.
func decEq(d apd.Decimal, s string) bool {
	want := dec(s)
	return d.Cmp(&want) == 0
}

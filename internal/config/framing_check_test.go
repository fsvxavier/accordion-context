package config

import "testing"

// compress.framing_check (spec §11.2) is an opt-in bool: default false, settable
// from the environment and from the local project layer, and listed by Effective.
func TestFramingCheckKey(t *testing.T) {
	f := newFixture(t)

	r, err := Load(Options{GlobalPath: f.global, Root: f.root, LookupEnv: envOf(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if r.Config.FramingCheck {
		t.Fatal("framing_check must default to false")
	}
	if e := entry(t, r, "compress.framing_check"); e.Source != "default" || e.Value != "false" {
		t.Fatalf("default entry = %+v", e)
	}

	r, err = Load(Options{GlobalPath: f.global, Root: f.root, LookupEnv: envOf(map[string]string{
		"ACD_CTX_COMPRESS_FRAMING_CHECK": "true",
	})})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Config.FramingCheck {
		t.Fatal("env must enable framing_check")
	}
	if e := entry(t, r, "compress.framing_check"); e.Source != "env:ACD_CTX_COMPRESS_FRAMING_CHECK" {
		t.Fatalf("env entry source = %q", e.Source)
	}

	writeFile(t, f.localPath(), "compress:\n  framing_check: true\n")
	r, err = Load(Options{GlobalPath: f.global, Root: f.root, LookupEnv: envOf(nil)})
	if err != nil {
		t.Fatalf("local layer must accept framing_check: %v", err)
	}
	if !r.Config.FramingCheck {
		t.Fatal("local layer must set framing_check")
	}
	if e := entry(t, r, "compress.framing_check"); e.Source != "local:"+f.localPath() {
		t.Fatalf("local entry source = %q", e.Source)
	}
}

func TestFramingCheckRejectsNonBool(t *testing.T) {
	f := newFixture(t)
	writeFile(t, f.global, "compress:\n  framing_check: maybe\n")
	if _, err := Load(Options{GlobalPath: f.global, Root: f.root, LookupEnv: envOf(nil)}); err == nil {
		t.Fatal("non-bool framing_check must be an error")
	}
}

package launcher

import (
	"reflect"
	"strings"
	"testing"
)

// specProfile is the example of spec §19.9 ("Perfis declarativos de cliente"),
// verbatim.
const specProfile = `# clients/exemplo-cli.yaml
name: exemplo-cli
detect:
  binaries: ["exemplo"]                 # nome do executável
  config_paths: ["~/.exemplo/config.json"]
integration:
  prefer: [base_url_env, https_proxy]   # degraus a tentar, em ordem
  base_url_env:
    OPENAI_BASE_URL: "{proxy}/ctx/https://api.openai.com/v1"
  https_proxy:
    ca_env: [NODE_EXTRA_CA_CERTS]
    domains: ["api.openai.com"]
model_info:                             # de onde ler janela e limite de saída
  file: "~/.exemplo/config.json"
  context_window: "$.model.contextWindow"
  max_output: "$.model.maxTokens"
quirks:
  single_system_message: true           # backends que rejeitam system no meio
`

func TestSpecSection19_9ProfileLoads(t *testing.T) {
	ps, err := LoadProfiles(strings.NewReader(specProfile))
	if err != nil {
		t.Fatalf("spec example rejected: %v", err)
	}
	if len(ps) != 1 || ps[0].Name != "exemplo-cli" {
		t.Fatalf("profiles = %+v", ps)
	}
	p := ps[0]
	if !reflect.DeepEqual(p.Detect.Binaries, []string{"exemplo"}) || p.Detect.ConfigPaths[0] != "~/.exemplo/config.json" {
		t.Fatalf("detect = %+v", p.Detect)
	}
	if !reflect.DeepEqual(p.Integration.Prefer, []string{"base_url_env", "https_proxy"}) {
		t.Fatalf("prefer = %v", p.Integration.Prefer)
	}
	if p.Integration.BaseURLEnvMap["OPENAI_BASE_URL"] == "" || p.Integration.HTTPSProxy.CAEnv[0] != "NODE_EXTRA_CA_CERTS" {
		t.Fatalf("integration = %+v", p.Integration)
	}
	if p.ModelInfo.MaxOutput != "$.model.maxTokens" || p.Quirks["single_system_message"] != true {
		t.Fatalf("model_info/quirks = %+v / %v", p.ModelInfo, p.Quirks)
	}
	// Matched through detect.binaries, since the example has no match list.
	if got, ok := MatchProfile(ps, "/usr/bin/Exemplo"); !ok || got.Name != "exemplo-cli" {
		t.Fatalf("MatchProfile via detect.binaries = (%q, %v)", got.Name, ok)
	}
}

func TestFlagAndTempConfigProfileLoads(t *testing.T) {
	doc := `
profiles:
  - name: flagged
    match: [flagged]
    upstream: https://api.example.com/v1
    base_url_flag: "--api-base {url}"
    flag_position: after
  - name: filed
    match: [filed]
    upstream: https://api.example.com/v1
    temp_config:
      path_env: FILED_CONFIG
      format: json
      template: '{"base_url": "{url}"}'
  - name: merged
    match: [merged]
    upstream: https://api.example.com/v1
    quirks:
      ignores_base_url: false
    temp_config:
      path_flag: "--config {path}"
      format: ini
      merge_from: "~/.config/merged.ini"
      key: api.base_url
`
	ps, err := LoadProfiles(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}
	if len(ps) != 3 || ps[0].BaseURLFlag != "--api-base {url}" || !ps[0].FlagsAfter() {
		t.Fatalf("flagged profile = %+v", ps[0])
	}
	if ps[1].TempConfig == nil || ps[1].TempConfig.PathEnv != "FILED_CONFIG" || ps[1].BaseURLEnv != "" {
		t.Fatalf("filed profile = %+v", ps[1].TempConfig)
	}
	if ps[2].TempConfig.MergeFrom != "~/.config/merged.ini" || ps[2].TempConfig.Key != "api.base_url" {
		t.Fatalf("merged profile = %+v", ps[2].TempConfig)
	}
}

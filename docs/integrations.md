# Integrations research

Research date: 2026-11-04. Read-only research: official documentation and GitHub READMEs were fetched; no provider was called and nothing from the web was executed. All fetched text is treated as untrusted data.

Rules used in this file:

- Every external fact carries a source URL. A fact from a third-party page (a gateway vendor's guide, a blog, a forum) is tagged `[3rd-party]` and is weaker than an official one.
- `UNVERIFIED` means it could not be confirmed from a source fetched in this session. Nothing marked UNVERIFIED should be coded as fact.
- Repo facts (what acd-ctx does today) come from `CLAUDE.md`, `internal/launcher/profile.go`, `internal/proxy/route.go`, `internal/attach/attach.go`, `cmd/acd-ctx/main.go` and `docs/compatibility.md`.
- Status for every row is `documented/unverified`: nothing here was run against a live client, gateway or provider.

## Repo facts the analysis relies on

- acd-ctx speaks four wire protocols: Anthropic `POST /v1/messages`, OpenAI `POST /v1/chat/completions`, Responses `POST /v1/responses`, Google `models/*:generateContent` (`docs/compatibility.md`).
- A launcher base URL is `http://<addr>/p/<projectID>/ctx/<profile.upstream>` (`launcher.BaseURL`). The client appends its own path, and `route()` splits the text after `/ctx/` into the origin (scheme and host only) and a tail path (`internal/proxy/route.go`). So a fixed path prefix such as `/v1` or `/anthropic` in the upstream travels in the tail, and only the origin is checked against the allowlist.
- The allowlist is `--allow` or `ACD_CTX_ALLOW` (comma-separated origins `scheme://host[:port]`); without it only the `--upstream` origin is allowed (`cmd/acd-ctx/main.go`, `route.go`). A gateway reached through `/ctx/` must be listed there, for example `ACD_CTX_ALLOW=http://127.0.0.1:20128`.
- Degree names (`internal/attach/attach.go`): `base-url-env`, `base-url-flag`, `temp-config`, `console`, `mitm`, `system-proxy`, `transparent`. `quirks.ignores_base_url` skips the first three.
- Only the Anthropic adapter streams live; the others are read whole and re-emitted (`CLAUDE.md`, request flow). Clients with short idle timeouts may stall on OpenAI-style streams. Client idle timeouts for the non-Claude clients are UNVERIFIED.
- Window size comes from the model name prefix or `window.context_limit`. A gateway alias such as `auto` has no known window, so `window.context_limit` should be set for it (I8 in `CLAUDE.md`).
- `kernel.RefLabel` labels (`[mNNNNN]`) and the rendered prefix must stay byte-stable for the provider cache. Any gateway that rewrites or compresses the request body can break cache reuse.

## Summary table

| Target | Kind | Protocol (documented) | Best integration degree | Status |
|---|---|---|---|---|
| Claude Code CLI | client | Anthropic Messages | `base-url-env` (`ANTHROPIC_BASE_URL`), already built in | documented/unverified |
| Claude Code in VS Code / JetBrains chat | client (IDE) | Anthropic Messages | none from the launcher; user sets `claudeCode.environmentVariables` or settings.json `env` (manual config). JetBrains path UNVERIFIED | documented/unverified |
| Kilo Code CLI | client | OpenAI Chat / Responses / Anthropic Messages (per provider config) | `temp-config` (`KILO_CONFIG`), key schema partly UNVERIFIED | documented/unverified |
| Kilo Code VS Code extension | client (IDE) | same three, chosen per custom provider | manual custom provider in Settings (base URL field); no launcher | documented/unverified |
| GitHub Copilot CLI | client | `openai` (completions or responses), `azure`, `anthropic` provider types (BYOK) | `temp-config` through `COPILOT_PROVIDERS_CONFIG`, file schema UNVERIFIED; else `mitm` | documented/unverified |
| GitHub Copilot Chat (VS Code) | client (IDE) | Chat Completions / Responses / Messages (Custom Endpoint, BYOK) | manual `chatLanguageModels.json`; default Copilot traffic only via `mitm` (UNVERIFIED feasibility) | documented/unverified |
| GitHub Copilot Chat (JetBrains) | client (IDE) | UNVERIFIED | UNVERIFIED (IDE HTTP proxy plus CA is the only documented network path) | documented/unverified |
| OpenCode | client | OpenAI-compatible, OpenAI Responses, Anthropic via `@ai-sdk/*` providers | `temp-config` via `OPENCODE_CONFIG` (the current built-in profile is wrong, see section) | documented/unverified |
| Cursor | client (IDE) | OpenAI-style (override base URL) | none usable; UNVERIFIED whether requests leave from the IDE or from Cursor's servers | documented/unverified |
| Windsurf | client (IDE) | UNVERIFIED | none documented | documented/unverified |
| Cline (VS Code ext.) | client (IDE) | OpenAI-compatible, Anthropic | manual base URL in settings; no launcher | documented/unverified |
| Cline CLI | client | UNVERIFIED | none documented | documented/unverified |
| freellmapi | gateway | OpenAI Chat, Responses, Anthropic `/v1/messages`, Gemini `/v1beta` | acd upstream via `/ctx/` (acd -> gateway) | documented/unverified |
| Bifrost | gateway | OpenAI, Anthropic, GenAI paths | acd upstream via `/ctx/` (acd -> gateway) | documented/unverified |
| OmniRoute | gateway | OpenAI Chat, Responses; Anthropic root surface implied | acd upstream via `/ctx/` (acd -> gateway) | documented/unverified |

## Claude Code (CLI and IDE extensions)

### What it is, protocol

- Claude Code talks the Anthropic Messages format to `ANTHROPIC_BASE_URL`. Inference posts to `/v1/messages?beta=true`; `/v1/messages/count_tokens` is optional (character estimate fallback when missing). Source: https://code.claude.com/docs/en/llm-gateway-protocol.md (fetched 2026-11-04).
- Anthropic says it does not support routing Claude Code to non-Claude models through any gateway. Source: https://code.claude.com/docs/en/llm-gateway (2026-11-04).

### Base URL, credentials, headers

- `ANTHROPIC_BASE_URL` points Claude Code at a gateway. Credentials: `ANTHROPIC_AUTH_TOKEN` is sent as `Authorization: Bearer`, `ANTHROPIC_API_KEY` as `x-api-key`, `apiKeyHelper` in both. A wrong choice gives `401`. Source: https://code.claude.com/docs/en/llm-gateway-connect.md.
- With only `ANTHROPIC_BASE_URL` set and a saved claude.ai login, the login stays the credential and `anthropic-beta` carries an OAuth capability that must be forwarded or the request fails with `401`. Source: https://code.claude.com/docs/en/llm-gateway and the protocol page above. acd must forward `anthropic-beta` and `Authorization` unchanged on this path; whether it does for every header is UNVERIFIED here (code not audited).
- `ANTHROPIC_CUSTOM_HEADERS` sends extra headers, one `Name: Value` per line (`\n` in JSON). Source: llm-gateway-connect page.
- Base URL examples have no `/v1`: `https://llm-gateway.example.com`, and the client appends `/v1/messages`. Source: llm-gateway-connect page (verification curl uses `$ANTHROPIC_BASE_URL/v1/messages`).
- Request headers sent: `anthropic-version`, `anthropic-beta`, `x-claude-code-session-id`, optional `x-claude-code-agent-id`, `x-claude-code-parent-agent-id`; hint headers (`x-claude-code-request-class`, `x-claude-code-compaction`, `x-claude-code-context-compacted`, ...) are off on a custom base URL unless `CLAUDE_CODE_GATEWAY_HINT_HEADERS=1`. Source: protocol page.
- The protocol page says to forward `anthropic-*` headers and body fields unchanged and to keep `system` blocks and `cache_control` as received. Source: protocol page ("Forward as open lists", "Feature pass-through"). acd rewrites the message window by design, so it must keep `system`, `cache_control` and paired beta header/body fields (`context_management`, `output_config`, tool fields) intact. If a rewrite breaks a pair, symptoms are `400 Extra inputs are not permitted`; remedy `CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1`. Source: llm-gateway-connect troubleshooting table.
- The attribution block in `system` must stay first and in its own array entry, or the cache key changes; `CLAUDE_CODE_ATTRIBUTION_HEADER=0` removes it. Source: protocol page.
- Claude Code's own `/compact` and auto-compaction still run beside acd; the `x-claude-code-compaction` header marks that request (only with hint headers on). Interaction with acd compaction is a design question, not a documented fact.
- acd-ctx forwards that request unchanged and records it; see [Client compaction commands](../README.md#client-compaction-commands).
- A `HEAD /api/hello` warm-up probe goes to the gateway and is skipped when an HTTP proxy or client certificate is configured. Source: protocol page. How acd answers it is UNVERIFIED.
- Streaming: Claude Code stalls if the gateway buffers; it expects the full event sequence through `message_delta` and `message_stop`; a byte-level watchdog of 300 s applies on a custom base URL, and SSE `ping` events keep it alive. Source: protocol page and https://code.claude.com/docs/en/network-config (idle watchdog table).

### Proxy and CA (for the `mitm` degree)

- Honors `HTTPS_PROXY`, `HTTP_PROXY`, `NO_PROXY` (lowercase also); SOCKS is not supported. Source: https://code.claude.com/docs/en/network-config.
- Trusts bundled Mozilla CAs and the OS store by default (`CLAUDE_CODE_CERT_STORE=bundled,system`); `NODE_EXTRA_CA_CERTS` adds a PEM. The OS store needs the native installer or Node 22.15+. Same source.
- Certificate pinning: the docs describe TLS-inspection proxies working when their root is installed, and mTLS client certificates. No pinning statement was found; absence of a statement is not proof, so pinning is UNVERIFIED but the documented behavior favors MITM working.
- With the Desktop app managing the provider connection, proxy and CA variables are read only from managed settings and `~/.claude/settings.json`. Same source.

### Where config lives

- `~/.claude/settings.json` (Windows `%USERPROFILE%\.claude\settings.json`), `.claude/settings.json`, `.claude/settings.local.json`, managed settings; `~/.claude.json` is written by Claude Code itself; `CLAUDE_CONFIG_DIR` relocates the user folder. Source: https://code.claude.com/docs/en/settings. Managed-settings file paths per OS: UNVERIFIED (not shown in the fetched text).
- The `env` block in settings accepts these variables. When the shell and a settings `env` block set the same variable, the llm-gateway-connect page says the settings value applies. Another page says that in Desktop sessions whose launch environment already sets a variable the settings value is ignored. These two statements are about different contexts and the exact CLI precedence is UNVERIFIED; a user `env.ANTHROPIC_BASE_URL` in settings can therefore override or be overridden by what the launcher exports. Sources: llm-gateway-connect and network-config pages.
- `claude --settings '<json or file>'` applies a settings layer for one session. Source: settings page. This makes a `temp-config` route possible (see profiles).

### IDE chat

- VS Code extension: set gateway variables in the VS Code user setting `claudeCode.environmentVariables`, a list of `{name, value}`. The page says values in `~/.claude/settings.json` reach the spawned process but not the extension's own login check, so `claudeCode.environmentVariables` is the reliable place for the credential. Source: https://code.claude.com/docs/en/llm-gateway-connect.md (VS Code extension section).
- The extension and CLI share `~/.claude/settings.json`; `claudeCode.disableLoginPrompt` skips the sign-in for third-party setups. Source: https://code.claude.com/docs/en/vs-code (2026-11-04).
- The extension does not necessarily inherit the shell environment: launch VS Code from a terminal with `code .` to inherit it. Same source.
- For a TLS-inspecting proxy, set `NODE_EXTRA_CA_CERTS` in the `env` block of `~/.claude/settings.json` (a value only in `claudeCode.environmentVariables` does not reach dictation or the Web tab; extension 2.1.307+). In WSL, SSH or dev-container windows edit settings on that remote system. Same source.
- The VS Code page does not document `HTTP_PROXY`/`HTTPS_PROXY` for the extension. UNVERIFIED for the panel's model traffic.
- JetBrains plugin: the settings page says JetBrains extensions read the same settings files (https://code.claude.com/docs/en/settings). How the plugin receives environment variables (IDE run configuration, shell, plugin setting) was NOT confirmed; the JetBrains page was not fetched. UNVERIFIED.

### Gotchas

- Using `CLAUDE_CODE_USE_*` variables routes to cloud providers instead of the base URL; unset them. Source: https://docs.getbifrost.ai/cli-agents/claude-code.
- Remote Control and voice dictation are unavailable while a gateway credential is active or the base URL is non-Anthropic. Source: llm-gateway-connect page.
- Fast mode availability check and WebFetch domain check go straight to `api.anthropic.com`, not to the base URL. Source: protocol page.
- A gateway-enforced smaller context window rewrites the error, so Claude Code does not recognise it; set `CLAUDE_CODE_AUTO_COMPACT_WINDOW`. Source: llm-gateway-connect troubleshooting table.
- Unrecognised model IDs (gateway aliases) get the newest-model request fields (adaptive thinking, system messages). Source: protocol page.

## Kilo Code (VS Code extension and CLI)

### What it is, protocol

- A coding-agent platform with a VS Code extension, a CLI and a JetBrains extension. Install: `code --install-extension kilocode.kilo-code`; `npm install -g @kilocode/cli`. Source: https://kilo.ai/docs (2026-11-04).
- The CLI "is a fork of OpenCode". Source: https://github.com/Kilo-Org/kilocode.
- Kilo Gateway: base `https://ai-gateway.kilo.ai/api/v1`, OpenAI chat completions style, SSE, tool calling; BYOK; extra headers `X-KiloCode-OrganizationId`, `X-KiloCode-TaskId`, `X-KiloCode-Version`, `x-kilocode-mode`. Sources: https://kilo.ai/docs/gateway and https://kilo.ai/docs/gateway/authentication.

### Pointing traffic elsewhere

- VS Code extension: Settings (gear), Providers tab, bottom, "Custom provider". Fields: Provider ID, Display name, Provider API, Base URL, API key (optional), Models, Headers (optional). Provider API options: OpenAI Compatible, OpenAI Responses, Anthropic Messages. Base URL may be `https://api.provider.com/v1` or a full endpoint URL such as `.../v1/chat/completions`. Source: https://kilo.ai/docs/providers/openai-compatible.
- Extra keys (token limits, tool calling, variants) go in `kilo.jsonc`. Same source.
- CLI config files: global `~/.config/kilo/kilo.json[c]` (legacy `opencode.json[c]`; Windows directory may differ), project `./kilo.json[c]` or `./.kilo/`, legacy `./.kilocode/`; project overrides global. `{env:VAR}` substitution works only in trusted config (global, `KILO_CONFIG`, `KILO_CONFIG_CONTENT`, managed). `KILO_PROVIDER` and `KILO_<FIELD>` override provider fields. Source: https://kilo.ai/docs/code-with-ai/platforms/cli.
- The CLI docs do not document `options.baseURL` or the npm adapter field for a custom provider (they point to a Custom Models page that was not fetched). Because the CLI is an OpenCode fork, the OpenCode schema (`provider.<id>.npm`, `options.baseURL`) is probable, but for Kilo it is UNVERIFIED.
- `[3rd-party]` OmniRoute's CLI-integration doc says `omniroute setup-kilo` writes `~/.local/share/kilo/auth.json` for the CLI and merges `kilocode.*` into VS Code `settings.json`, with a base URL that includes `/v1`. Source: https://raw.githubusercontent.com/diegosouzapw/OmniRoute/main/docs/guides/CLI-INTEGRATIONS.md. Not confirmed by Kilo docs. UNVERIFIED.
- Whether the extension and CLI share one config: UNVERIFIED.
- HTTP(S)_PROXY, `NODE_EXTRA_CA_CERTS`, system CA, pinning for Kilo: not documented in the pages fetched. UNVERIFIED. As a VS Code extension it falls under VS Code's own network rules (see Copilot section for the VS Code caveat that extensions do not get the same proxy support).
- Whether the extension runs model calls in the extension host (so a loopback base URL works) was not stated. UNVERIFIED.

### Gotchas

- Kilo accepts a full endpoint URL, so a base URL with `/v1` may or may not be appended to; follow the docs: either `.../v1` or the full path, not both.
- Azure GPT-5 must use the native `azure` provider, not custom OpenAI-compatible. Source: openai-compatible page.
- The extension auto-fetches models from `/v1/models`; acd would forward that GET to the upstream.

## GitHub Copilot (CLI, VS Code chat, JetBrains chat)

### Copilot CLI

- BYOK: provider entries in `providers.json` (normally in `~/.copilot`, replaceable by `COPILOT_PROVIDERS_CONFIG`). Fields: `baseUrl`, `type` (`openai` default, `azure`, `anthropic`), `apiKey` or `bearerToken`, models with `id` and `provider`, `maxPromptTokens`, `maxOutputTokens`. Wire API: `completions` (default) or `responses`. The model and endpoint must support tool calling and streaming. `COPILOT_OFFLINE=true` stops contact with GitHub. Credentials are stored in plain text; `$VAR` text is literal. Restart after editing. Sources: https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/use-byok-models and the `enterprise-cloud@latest` variant of the same page (2026-11-04).
- CONFLICT: a web search snippet and `[3rd-party]` guides (Cloudflare, Zuplo, orq.ai) describe environment variables such as `COPILOT_PROVIDER_BASE_URL` and `COPILOT_MODEL`; the official page fetched twice mentions neither and uses `providers.json`. The docs may have changed between versions. Treat the environment-variable form as UNVERIFIED.
- Top-level shape of `providers.json` (array or object, exact key names around the provider list): not shown in the extracted text. UNVERIFIED.
- `[3rd-party]` Zuplo says for Claude models the base URL should omit `/v1` because the CLI appends `/v1/messages`; Cloudflare says the CLI appends `/chat/completions`. Path appending for each `type` is UNVERIFIED.
- Network: Copilot reads `HTTPS_PROXY`, `https_proxy`, `HTTP_PROXY`, `http_proxy` in that order; an `https://` proxy URL is not supported; certificates come from the OS store and `NODE_EXTRA_CA_CERTS`. Source (via search result, page not opened in full): https://docs.github.com/en/copilot/how-tos/configure-personal-settings/configure-network-settings. Whether this applies to the CLI specifically: UNVERIFIED.

### Copilot Chat in VS Code

- BYOK is documented for the chat experience and utility tasks only; inline suggestions, semantic search and embeddings still need a GitHub account. Models must support tool calling to appear for agents. Source: https://code.visualstudio.com/docs/copilot/customization/language-models.
- Custom Endpoint provider: configured in `chatLanguageModels.json` with `"vendor": "customendpoint"` and a per-model `url`; API types Chat Completions, Responses, Messages (per provider or per model). If a URL has no explicit API path, VS Code appends one and may insert `/v1`. `github.copilot.chat.customOAIModels` is deprecated in its favor. Same source. Location of `chatLanguageModels.json` per OS: UNVERIFIED.
- Copilot Business/Enterprise: the "Bring Your Own Language Model Key in VS Code" policy can disable it. Same source.
- Default (non-BYOK) Copilot traffic goes to GitHub-hosted endpoints; hostnames were not fetched. UNVERIFIED. Intercepting it needs the `mitm`, `system-proxy` or `transparent` degree and would carry the GitHub-side wire format, which is not one of the four codecs. Whether it is a supported shape: UNVERIFIED.
- VS Code network page: `http.proxy`, `http.proxyStrictSSL`, `http.proxyAuthorization` are settings but "only respected in these two scenarios" (not extracted), and "Extensions don't benefit yet from the same proxy support that VS Code supports". Source: https://code.visualstudio.com/docs/setup/network. This is a risk for any extension (Kilo, Cline, Copilot) relying on `http.proxy`.

### Copilot Chat in JetBrains

- Proxy settings are in the IDE under Appearance and Behavior, System Settings, HTTP Proxy; Copilot-specific options under Languages and Frameworks, GitHub Copilot. Source: search summary of https://docs.github.com/en/copilot/how-tos/configure-personal-settings/configure-network-settings (page not read in full).
- BYOK or a custom endpoint for JetBrains: not found. UNVERIFIED, treat as impossible until a source appears.

## OpenCode (review of the existing profile)

- The built-in profile in `internal/launcher/profile.go` says "OpenCode has no documented base URL variable; its providers are set in its own config. Launching it needs the MITM mode." Part of this is right (no env variable) and part is out of date: OpenCode documents a per-provider `options.baseURL` in its config and an `OPENCODE_CONFIG` variable that loads an extra config file. Sources: https://opencode.ai/docs/providers/ and https://opencode.ai/docs/config/ (2026-11-04).
- Documented: `provider.<id>.options.baseURL`, `options.apiKey` (supports `{env:VAR}`), `options.headers`; for OpenAI-compatible use npm `@ai-sdk/openai-compatible` (`/v1/chat/completions`), for `/v1/responses` use `@ai-sdk/openai`. "You can customize the base URL for any provider by setting the baseURL option", with the built-in example `provider.anthropic.options.baseURL = https://api.anthropic.com/v1`. Source: providers page.
- Config precedence (low to high): remote `.well-known/opencode`, global `~/.config/opencode/opencode.json`, `OPENCODE_CONFIG`, project `opencode.json`, `.opencode` dirs, `OPENCODE_CONFIG_CONTENT`, managed files, macOS managed preferences. Configs merge, they do not replace. Source: config page. Consequence: an `OPENCODE_CONFIG` temp file is overridden by a project `opencode.json` that sets the same key, and by `OPENCODE_CONFIG_CONTENT`. `OPENCODE_CONFIG_CONTENT` (level 6) would win over the project file but its format is not detailed; UNVERIFIED.
- Credentials from `/connect` live in `~/.local/share/opencode/auth.json` and are not touched by a config overlay. Source: providers page.
- Proxy and CA behavior: the pages fetched do not mention `HTTPS_PROXY`. UNVERIFIED, so `mitm` feasibility is unknown.
- Console vs IDE: OpenCode is a terminal client; IDE integration not researched. UNVERIFIED.
- Correction: replace "needs MITM" with `temp-config` through `OPENCODE_CONFIG` (see profiles). The overlay applies only to the provider named in the template, so one profile per provider and upstream.
- The repo `docs/compatibility.md` row for opencode (`MITM`, "no base URL variable") is generated from the built-in profile and would change when the profile changes; regenerate it then (`go run ./scripts/gen-compat-matrix`).

## Cursor

- `[3rd-party]` Cursor has an "Override OpenAI Base URL" plus custom key setting. Sources: https://forum.cursor.com/t/custom-openai-override-and-cursor-models-cannot-both-stay-enabled/172795, https://ngrok.com/docs/ai-gateway/agents/cursor, https://dev.to/orchidfiles/why-localhost-doesnt-work-as-openai-base-url-in-cursor-and-how-to-fix-it-589e.
- CONFLICT between third-party sources: one says requests go from Cursor's backend to the override URL, so `http://localhost` can never work and an HTTPS tunnel is needed; another says requests are sent from the local process and localhost works. Which is right is UNVERIFIED. Cursor's own documentation could not be read (`docs.cursor.com/settings/api-keys` redirects to `cursor.com/docs`, which had no relevant content).
- Reported behavior (3rd-party): the override is global and disables Cursor's own models ("This model does not support custom API keys"); Tab completion and often Agent mode do not use the custom path; Cursor appends `/chat/completions` to the entered URL; a paid plan is needed.
- MITM: proxy and CA behavior of Cursor not researched. UNVERIFIED.
- Conclusion: no launcher profile. If the cloud-routing account is right, acd would have to be reachable from the internet, which `listen.go` only allows with `--allow-non-loopback` and `--allowed-hosts` and which exposes provider credentials. Not recommended.

## Windsurf

- Official docs moved: `https://docs.windsurf.com/windsurf/models` redirects (307) to `https://docs.devin.ai/desktop/models`, which says nothing about BYOK, custom endpoints or base URLs. A search found a JetBrains-proxy troubleshooting page (`docs.windsurf.com/.../jetbrains-proxy`, title only, not read) and third-party pages that disagree on base URLs and say a custom Base URL field may not exist in all versions.
- Base URL override, config file/env, proxy and CA behavior: UNVERIFIED. No profile proposed.

## Cline (VS Code extension and CLI)

- Extension: provider "OpenAI Compatible" takes a Base URL that must not be `https://api.openai.com/v1`. Source: https://docs.cline.bot/provider-config/openai-compatible. The Anthropic provider has "Use custom base URL" (optional). Source: https://docs.cline.bot/provider-config/anthropic. Both are UI settings; where Cline stores them on disk is UNVERIFIED. Custom headers and proxy: not on the fetched pages, UNVERIFIED.
- CLI: documented flags are `--config <dir>`, `--data-dir <dir>` and `-P, --provider <id>`; provider auth is `cline auth`. Source: https://docs.cline.bot/cline-cli/overview. Env vars, base URL options and the file format inside the config dir: UNVERIFIED.
- `[3rd-party]` OmniRoute says Cline uses the root URL without `/v1`. Source: OmniRoute CLI-INTEGRATIONS.md (above). Unconfirmed.
- Conclusion: manual custom provider in the UI pointing at acd's `/p/<id>/ctx/<upstream>`; no launcher profile until the CLI config format is known.

## Gateway: freellmapi

- Several repositories carry this name (3rd-party search: tashfeenahmed/freellmapi, SeanPedersen/freellmapi, vi70x4/freellmapi, S40014/freellmapi). The one read was https://github.com/tashfeenahmed/freellmapi (2026-11-04); that it is canonical is UNVERIFIED.
- Self-hosted OpenAI-compatible router over free tiers of many providers. "Personal experimentation only", no SLA, no frontier models, provider terms still apply. Deploy: one-line Docker installer (not run), Docker Compose, `npm install && npm run dev`, desktop app. Default port 3001 (dev dashboard 5173). Source: repo README (page and raw README).
- Endpoints under `/v1`: `/chat/completions`, `/responses`, `/embeddings`, `/images/generations`, `/audio/speech`, `/audio/transcriptions`, `/messages` (Anthropic-style), `/models`; `/v1beta` Gemini surface; Ollama emulation (opt-in); MCP at `/mcp`; docs at `/v1/docs`.
- Auth: one issued `freellmapi-...` bearer token. Which header the Anthropic surface accepts (`Authorization` or `x-api-key`): the README does not say. UNVERIFIED.
- Models: `auto`, `auto:fast`, `auto:smart`, `auto:<profile>`, a specific model ID, or virtual `fusion` (fans a prompt to several models).
- Tools: "OpenAI-style tools round-trip across providers"; plain-text tool calls are converted into real `tool_calls`. Streaming supported. Failover: retries the next model on 429/5xx with cooldowns and key rotation.
- Claude Code setup it documents: `npx freellmapi setup-claude --url http://localhost:3001 --api-key <key>` or `npx freellmapi launch` (not run). It does not name `ANTHROPIC_BASE_URL`.
- Chaining acd -> freellmapi: base URL `http://<acd>/p/<id>/ctx/http://127.0.0.1:3001/v1` for OpenAI-style clients, with `ACD_CTX_ALLOW=http://127.0.0.1:3001`. For Claude Code: `.../ctx/http://127.0.0.1:3001` (client appends `/v1/messages`). Whether `/v1/messages` there supports the beta fields Claude Code sends, adaptive thinking and `cache_control`: UNVERIFIED.
- Chaining freellmapi -> acd: custom OpenAI-compatible endpoints are configured on the Keys page with "any OpenAI-compatible endpoint". Point it at `http://<acd>/p/<id>/ctx/<real provider>/v1`. UNVERIFIED in practice.
- Conflicts: the router's failover/model switching changes the model between turns, so window sizing (`window.context_limit`) and provider-side prefix cache are unreliable; `fusion` and `auto` hide the real model from acd. The router is local-first and single-user by design.

## Gateway: Bifrost

- "High-performance AI gateway" over 23+ providers, repo https://github.com/maximhq/bifrost (2026-11-04). Deploy: `npx -y @maximhq/bifrost` or `docker run -p 8080:8080 maximhq/bifrost` (add `-v $(pwd)/data:/app/data` to persist). Default port 8080, default host localhost (`-host 0.0.0.0` or `APP_HOST=0.0.0.0`); `BIFROST_SETUP_TOKEN` protects first admin creation. Source: https://docs.getbifrost.ai/quickstart/gateway/setting-up.
- Endpoints: `POST /v1/chat/completions` (OpenAI format); SDK drop-in prefixes `/openai`, `/anthropic`, `/genai` (Gemini naming: "GenAI" only). Sources: GitHub README, https://docs.getbifrost.ai/integrations/anthropic-sdk.
- Claude Code: `ANTHROPIC_BASE_URL=https://bifrost.example.com/anthropic` (no `/v1`), `ANTHROPIC_AUTH_TOKEN=<virtual key sk-bf-...>` sent as Bearer; or `/login` plus `ANTHROPIC_CUSTOM_HEADERS=x-bf-vk: <key>`. A virtual key with no providers denies all (403). Source: https://docs.getbifrost.ai/cli-agents/claude-code.
- Header handling: "Bifrost will block unrecognized headers by default". `anthropic-beta` is not forwarded unless added to the AllowList in Settings, Client Settings. Source: anthropic-sdk page. This is the main conflict with acd: forward needs the allowlist, otherwise beta features silently drop or `400`s appear.
- Model naming: Anthropic models unprefixed; others `provider/model` (`openai/gpt-5.5`, `vertex/...`, `azure/...`, `ollama/...`). Routing rules can rewrite aliases. Keep a recognisable Claude model name in what Claude Code sends. Source: claude-code page.
- Limitations stated: non-Claude models may lack server-side tools; tool-call arguments must stream correctly (OpenRouter reportedly does not); `400 context_management` -> `CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1`. Same page.
- Custom provider in `config.json`: `custom_provider_config.base_provider_type` (`openai`, `anthropic`, `bedrock`), `network_config.base_url`, `keys` (`value` may be `env.VAR`), `request_path_overrides`, `allowed_requests`; path overrides are unsupported for `gemini` and `bedrock` base types. Source: https://docs.getbifrost.ai/providers/custom-providers. `extra_headers` is not mentioned there.
- Chaining acd -> Bifrost: Claude Code base `http://<acd>/p/<id>/ctx/http://127.0.0.1:8080/anthropic`, giving upstream path `/anthropic/v1/messages` (inferred from the SDK-appends-path behavior; UNVERIFIED), `ACD_CTX_ALLOW=http://127.0.0.1:8080`. Bifrost must have `anthropic-beta` allowlisted.
- Chaining Bifrost -> acd: add a custom provider with `base_provider_type: openai` (or `anthropic`) and `network_config.base_url: http://<acd>/p/<id>/ctx/<real upstream>/v1`. Bifrost then calls acd server-side, so acd must be reachable from Bifrost's network (the `/ctx/` origin is the real provider, not Bifrost). UNVERIFIED in practice.
- Other Bifrost features (caching, retries, key weighting): `weight` and `drop_excess_requests` appear in config examples; caching and retry settings were not read. UNVERIFIED.

## Gateway: OmniRoute

- Several repositories exist (diegosouzapw/OmniRoute, funcodingdev/omniroute, v0l/OmniRoute, and a page that rendered as cheaper-inference/OmniRoute release v3.8.53). The README read says MIT, "375 providers (150+ free), 1,400+ models". Which repository is canonical: UNVERIFIED. Source: https://github.com/diegosouzapw/OmniRoute (2026-11-04).
- Deploy: npm `omniroute`, Docker Hub image `diegosouzapw/omniroute` (Railway guide, https://railway.com/deploy/omniroute.md `[3rd-party]`), Electron; install commands were truncated in the fetched text. UNVERIFIED. Default port 20128.
- Endpoints confirmed: `/v1/chat/completions`, `/v1/responses`, `/v1/models`, `POST /v1/models/query`. `/v1/messages` is not listed explicitly; the CLI-integrations doc says Claude Code appends `/v1/messages` to the root URL and that the Anthropic surface is at the root. Treat as implied, UNVERIFIED. Gemini: UNVERIFIED.
- Auth: `Authorization: Bearer <key>` from Dashboard, Endpoints.
- Claude Code: `ANTHROPIC_BASE_URL` = root URL without `/v1` (local `http://localhost:20128`), `ANTHROPIC_AUTH_TOKEN`; `omniroute launch` injects both; `omniroute setup-claude` writes `~/.claude/profiles/<name>/settings.json`. Source: https://raw.githubusercontent.com/diegosouzapw/OmniRoute/main/docs/guides/CLI-INTEGRATIONS.md. Tools using the `/v1` form: Codex, Continue, Crush, Zed, Qwen, Kilo, Roo, Cursor; root form: Cline, Goose, Aider, Gemini CLI (same source).
- Models: `auto`, `auto/coding`, `auto/fast`, `auto/cheap`, `provider/model` (for example `openai/gpt-5.4`), combos with 20 routing strategies.
- Features that conflict with acd: "RTK+Caveman compression saves 15-95% tokens", a semantic response cache, a `cache-optimized` strategy, quota-aware fallback, circuit breaker, connection cooldown, model lockout. Compression and semantic cache rewrite or short-circuit requests, which defeats acd's byte-stable prefix and its confirmed-compression ledger; they should be disabled for traffic that goes through acd. Whether they can be disabled per key: UNVERIFIED.
- Custom provider with a base URL (needed for OmniRoute -> acd) and streaming details: not in the fetched content. UNVERIFIED. Chaining acd -> OmniRoute follows the same `/ctx/` pattern: `.../ctx/http://127.0.0.1:20128` (Claude Code) or `.../ctx/http://127.0.0.1:20128/v1` (OpenAI-style), with `ACD_CTX_ALLOW=http://127.0.0.1:20128`.

## Cross-cutting gotchas

- Path doubling: Claude Code's base has no `/v1`; OpenAI-style clients' bases include `/v1`; Kilo and Copilot Custom Endpoint can take a full endpoint URL or append `/v1` themselves (Kilo and VS Code pages above). The profile `upstream` field must contain the same prefix the real provider expects (`https://api.openai.com/v1`), and the client-appended path lands in the `/ctx/` tail.
- Trailing slashes: not documented by any source read; keep profile `upstream` values without a trailing slash.
- Anthropic format sent to an OpenAI-compatible base URL (or the reverse): the format is chosen per provider setting (Kilo "Provider API", Copilot `type`/`wireApi`, VS Code API type, OpenCode npm adapter). Mismatch gives 404 or 400; acd's `Detect` picks the adapter by path (`/v1/messages` vs `/v1/chat/completions`).
- `x-api-key` vs `Authorization`: Claude Code sends one or both depending on the variable; Bifrost reads Bearer virtual keys; freellmapi's Anthropic surface header is UNVERIFIED.
- `anthropic-beta`: must pass acd and any gateway unchanged (Claude Code docs), and Bifrost drops it by default.
- Non-Claude models behind Claude Code are unsupported by Anthropic and may break on tool streaming.
- Streaming through acd for non-Anthropic dialects is buffered (repo fact), which may trip client idle timeouts. Per-client timeouts UNVERIFIED.

## Proposed profiles

Schema: `internal/launcher/profile.go`. These YAML documents were written against the schema by reading it and were NOT machine-validated or run (no `attach` was executed, because attach may touch networking). Run `acd-ctx attach --profiles <file> -- <cmd>` only in a throwaway HOME after review. Each `temp_config` template uses only `{url}`; JSON braces are safe because the placeholder pattern matches `{name}` of letters and underscores only, and `{env:VAR}` contains a colon so it is not substituted.

Claude Code already has a built-in profile (`ANTHROPIC_BASE_URL`, `https://api.anthropic.com`). For a gateway upstream use an extra profile (match must not collide with the built-in name, so launch via a wrapper name or put it first in a profiles file; collision rule: first match wins, and extra profiles versus built-ins ordering was not checked, UNVERIFIED):

```yaml
profiles:
  # Claude Code through acd to a Bifrost gateway (Anthropic surface).
  - name: claude-bifrost
    match: [claude-bifrost]
    upstream: http://127.0.0.1:8080/anthropic
    base_url_env: ANTHROPIC_BASE_URL
    detect:
      binaries: [claude]
    integration:
      prefer: [base_url_env]
    env:
      CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS: "1"   # only if the gateway rejects beta fields; remove otherwise
```

(Requires `ACD_CTX_ALLOW=http://127.0.0.1:8080`. The virtual key still comes from the user's own `ANTHROPIC_AUTH_TOKEN`.)

OpenCode, replacing the current MITM-only built-in; one profile per provider, because the template can carry one upstream:

```yaml
profiles:
  - name: opencode-anthropic
    match: [opencode]
    upstream: https://api.anthropic.com/v1
    detect:
      binaries: [opencode]
      config_paths: ["~/.config/opencode/opencode.json"]
    integration:
      prefer: [temp_config]
    temp_config:
      path_env: OPENCODE_CONFIG
      format: json
      template: |
        {"provider": {"anthropic": {"options": {"baseURL": "{url}"}}}}
  - name: opencode-openai
    match: [opencode-openai]
    upstream: https://api.openai.com/v1
    detect:
      binaries: [opencode]
    integration:
      prefer: [temp_config]
    temp_config:
      path_env: OPENCODE_CONFIG
      format: json
      template: |
        {"provider": {"openai": {"options": {"baseURL": "{url}"}}}}
```

Caveats: a project `opencode.json` that sets the same key wins over `OPENCODE_CONFIG`; `OPENCODE_CONFIG_CONTENT` wins over both (config precedence above). Using `provider.openai.options.baseURL` for a built-in provider other than Anthropic is inferred from "any provider" and UNVERIFIED for `openai`. The built-in `opencode` entry in `profile.go` should lose its "needs MITM" comment if this lands.

Kilo Code CLI (UNVERIFIED key schema; do not ship until `options.baseURL` is confirmed in Kilo docs or by running the CLI in a sandbox):

```yaml
profiles:
  - name: kilo-openai
    match: [kilo]
    upstream: https://api.openai.com/v1
    detect:
      binaries: [kilo]
      config_paths: ["~/.config/kilo/kilo.json", "~/.config/kilo/kilo.jsonc"]
    integration:
      prefer: [temp_config]
    temp_config:
      path_env: KILO_CONFIG
      format: json
      template: |
        {"provider": {"openai": {"options": {"baseURL": "{url}"}}}}
```

Claude Code via a one-off settings layer (alternative to env, useful where a user `env.ANTHROPIC_BASE_URL` in settings would otherwise win; `--settings` is documented to apply above user, project and local files and below managed):

```yaml
profiles:
  - name: claude-settings
    match: [claude-settings]
    upstream: https://api.anthropic.com
    detect:
      binaries: [claude]
    integration:
      prefer: [temp_config]
    temp_config:
      path_flag: "--settings {path}"
      format: json
      template: |
        {"env": {"ANTHROPIC_BASE_URL": "{url}"}}
```

Whether `env` inside `--settings` overrides a shell-exported variable is UNVERIFIED (see precedence note above); it does override user-file `env` by the documented layer order.

Gateways as upstream (no new profile needed; reuse a client profile with a different `upstream`, and add the gateway origin to `ACD_CTX_ALLOW`):

- freellmapi: `upstream: http://127.0.0.1:3001/v1` (OpenAI-style clients) or `http://127.0.0.1:3001` (Claude Code).
- OmniRoute: `upstream: http://127.0.0.1:20128/v1` or `http://127.0.0.1:20128`.
- Bifrost: `upstream: http://127.0.0.1:8080/anthropic` (Claude Code) or `http://127.0.0.1:8080/openai` (OpenAI SDK clients).

### Not possible, or not supported by the facts

- Copilot CLI profile: withheld. The `providers.json` top-level schema is UNVERIFIED, and the documented environment-variable form conflicts with the official page. When confirmed, the shape would be `temp_config` with `path_env: COPILOT_PROVIDERS_CONFIG`, `format: json`, one provider with `type: openai`, `baseUrl: "{url}"` and `upstream: https://api.openai.com/v1`.
- Copilot Chat in VS Code and Kilo Code in VS Code, Cline in VS Code: no launcher can inject a base URL, because they are IDE UI settings (`chatLanguageModels.json`, Providers tab, Cline settings panel). The launcher cannot edit user files (`merge_from` is read-only by design). The user pastes `http://127.0.0.1:8787/p/<id>/ctx/<upstream>` into the custom provider form. An `attach --system` style degree does not apply to loopback BYOK.
- Claude Code IDE chat: not launchable by the CLI launcher. Manual: `claudeCode.environmentVariables` (reliable for credentials) or the settings.json `env` block. JetBrains: UNVERIFIED.
- Copilot default (non-BYOK) traffic, Copilot JetBrains BYOK, Cursor (any path), Windsurf, Cline CLI, Kilo extension proxy behavior: no usable documented mechanism. MITM, `system-proxy` and `transparent` are the only conceivable degrees and none is confirmed (proxy honoring, CA trust and pinning are UNVERIFIED for all of them), and the Copilot/Cursor/Windsurf hosted-service protocols are not among acd's four codecs.
- Fail-open for a crashed transparent redirect and IPv6 interception are not implemented (`CLAUDE.md`), which limits the `transparent` degree for any of these.

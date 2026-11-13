# Installers

Two scripts install acd-ctx, connect it to the coding clients, run the proxy as a user service, and later update or remove it:

| System | Script | Service |
| --- | --- | --- |
| Linux | `install.sh` | systemd user unit `acd-ctx.service` |
| macOS | `install.sh` | launchd agent `io.github.fsvxavier.acd-ctx` |
| Windows | `install.ps1` | Scheduled Task `acd-ctx` at logon (no administrator rights) |

Both scripts take the same commands and options; PowerShell spells the options `-Method`, `-Scope` and so on. With no arguments on a terminal they open a menu. With options they run without asking (add `--yes` / `-Yes` to skip the confirmation).

```sh
./install/install.sh                       # menu
./install/install.sh --help
```

```powershell
# The default execution policy blocks unsigned scripts; bypass it for this run only.
powershell -ExecutionPolicy Bypass -File .\install\install.ps1            # menu
Get-Help .\install\install.ps1 -Detailed
```

## Commands

| Command | What it does |
| --- | --- |
| `install` | Installs the binary, the service and the client entries. It starts from the defaults and the options given, not from an earlier install. |
| `update` | Installs a new version (default: the latest release) with the saved method, then restarts the service. It also stops running `acd-ctx mcp` processes, so the clients start the new binary on their next tool call; skip this with `--no-restart-mcp`. |
| `configure` | Rewrites the client files and the service from the saved settings plus the options given, for example `configure --port 8788`. Clients dropped from the list lose their acd-ctx entries. |
| `service start\|stop\|restart\|status\|install\|remove` | Manages the proxy service. |
| `status` | Shows the saved settings, the binary version, the service state and the proxy health. |
| `uninstall` | Removes the service and the acd-ctx client entries of the saved scope. `--purge` also removes the binary, the Docker volume and the saved settings. Databases are always kept. |

Settings are saved in `~/.config/acd-ctx/install.env` on Linux and macOS, and in `%APPDATA%\acd-ctx\install.json` on Windows.

## Install methods (`--method`)

| Method | What it does |
| --- | --- |
| `go` | `go install …/cmd/acd-ctx@vX.Y.Z` with `-X main.version`, so `acd-ctx version` reports the release and not `dev`. This is the default when Go is installed. |
| `release` | Downloads `acd-ctx_<version>_<os>_<arch>` from the GitHub release and checks it against `checksums.txt`. **v1.0.5 has no binaries attached**, so this method fails for it with a message; use `go` or `docker`. |
| `docker` | Pulls `ghcr.io/fsvxavier/accordion-context:<version>` and runs it with the flags from [docs/docker.md](../docs/docker.md), using the volume `acd-ctx-data` and `--restart unless-stopped` (the container is the service). MCP entries call `docker exec -i acd-ctx /acd-ctx mcp`. |
| `custom` | Builds a checkout (`--from-source DIR`) or copies a binary (`--binary FILE`) into `--bin-dir`. |

`--version` picks the version (default `latest`). `--bin-dir` sets the target directory; the default is `~/.local/bin`, or `%LOCALAPPDATA%\Programs\acd-ctx` on Windows. For the `go` method the default is `GOBIN` or `GOPATH/bin`.

## Scope (`--scope`)

| Scope | Where the client entries go |
| --- | --- |
| `global` | User files: `~/.claude/settings.json` and `claude mcp add --scope user`, `~/.codex/config.toml`, `~/.gemini/`, `~/.config/opencode/opencode.json`. |
| `local` | Files inside one project (`--project DIR`, default: the git root of the current directory): `.claude/settings.local.json`, `.mcp.json`, `.codex/config.toml`, `.gemini/`, `opencode.json`. Also runs `acd-ctx project init` when the project has no `.acd-ctx/` yet. |

Without `--scope`, the installer uses `local` when the current directory is inside a git repository and `global` otherwise. `acd-ctx mcp` finds its project from the working directory, and only project-scoped proxy settings tell the proxy which project a request belongs to. With global scope, the proxy puts requests in the `default` project, where the MCP recall tools do not find them; the installer warns about this.

With `--local-storage` the project keeps its own database in `.acd-ctx/acd-ctx.db`. Only Claude Code sends the project directory (`X-Acd-Ctx-Project-Dir`), which local storage needs. Codex, Gemini CLI and OpenCode reach the proxy through `/p/<id>/`, which uses the global database. `--local-storage` does not work with `docker`, because the container cannot see the project.

Global proxy settings carry no project signal, so their requests land in the `default` project. Use the local scope or the launchers (`acd-ctx claude`) to keep projects apart.

## Integration (`--integration`) and clients (`--clients`)

`--integration` takes `proxy`, `mcp`, `both` (the default) or `none`. With `--method docker` the default is `proxy`: the containerized `acd-ctx mcp` runs in `/data` and cannot see the project's sessions. `--clients` takes a comma list of `claude`, `codex`, `gemini` and `opencode`, or `all`; the default is `claude`. In proxy mode, `--host` (default `127.0.0.1`) and `--port` (default `8787`) set the listen address. `--service` or `--no-service` decides whether the proxy runs as a service.

| Client | Proxy setting | MCP entry |
| --- | --- | --- |
| Claude Code | `env.ANTHROPIC_BASE_URL` and `CLAUDE_CODE_GATEWAY_HINT_HEADERS=1`; local scope also sets `ANTHROPIC_CUSTOM_HEADERS` | `.mcp.json` plus `enabledMcpjsonServers`, or `claude mcp add --scope user` |
| Codex | `openai_base_url` in `~/.codex/config.toml`. Codex ignores this key in a project file, so it always goes in the user file. | `[mcp_servers.acd-ctx]`; a project file loads only when the project is trusted |
| Gemini CLI | `GOOGLE_GEMINI_BASE_URL` in `.gemini/.env`, used only with API-key auth | `mcpServers.acd-ctx` in `.gemini/settings.json` |
| OpenCode | `provider.anthropic.options.baseURL` | `mcp.servers.acd-ctx` (OpenCode 2), or `mcp.acd-ctx` when the file uses the 1.x layout |

Before changing a file, the installer saves a backup next to it (`<file>.bak-acd-ctx-<time>`). TOML and `.env` changes are wrapped in `# >>> acd-ctx:… >>>` markers. A setting that already exists outside the markers is not overwritten; the installer warns instead. JSON with comments (`.jsonc`) is not edited.

The proxy service gets `--allow` with every origin the chosen clients need. A non-loopback `--host` adds `--allow-non-loopback` and `--allowed-hosts`, with a warning: the Host allowlist is not authentication.

## Gateways (`--gateway`)

acd-ctx can send the traffic on to a gateway instead of the providers: client → acd-ctx → gateway → provider. `--gateway` takes `omniroute`, `freellmapi` or `bifrost`, and `none` goes back to sending directly to the providers. `--gateway-url` overrides the gateway's default address.

| Gateway | Default URL | Claude Code | Codex | Gemini CLI | OpenCode |
| --- | --- | --- | --- | --- | --- |
| OmniRoute | `http://127.0.0.1:20128` | `<url>` | `<url>/v1` | no surface (not written) | `<url>/v1` |
| freellmapi | `http://127.0.0.1:3001` | `<url>` | `<url>/v1` | `<url>` | `<url>/v1` |
| Bifrost | `http://127.0.0.1:8080` | `<url>/anthropic` | `<url>/openai` | `<url>/genai` | `<url>/anthropic/v1` |

Each client's base URL becomes `http://<host>:<port>/ctx/<surface>`, and `--allow` holds the gateway's origin. With `docker` the default URL uses `host.docker.internal`. The clients then send the gateway's key (for example `ANTHROPIC_AUTH_TOKEN`), not the provider's; the installer does not write keys. Bifrost drops `anthropic-beta` unless it is in its header AllowList. These paths come from [docs/integrations.md](../docs/integrations.md) and are documented, not verified.

## Testing the scripts

Do not test against your real setup: `systemctl --user`, `claude mcp` and `docker` reach the live user manager and the live clients even with a fake `HOME`. Use `--dry-run`, which prints every command and file change and writes nothing. Or run with a throwaway `HOME`, `XDG_CONFIG_HOME` and `XDG_DATA_HOME`, a temp project and stub `systemctl`, `docker`, `claude`, `go`, `pgrep` and `curl` first in `PATH`, plus a port other than the live one.

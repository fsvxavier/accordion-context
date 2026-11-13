#!/usr/bin/env bash
# acd-ctx installer for Linux and macOS.
#
# Installs the acd-ctx binary (release download, go install, a custom build or
# copy, or the Docker image), registers it with the coding clients (Claude
# Code, Codex, Gemini CLI, OpenCode) as an MCP server, as a proxy or both,
# globally or for one project, and runs the proxy as a user service
# (systemd --user on Linux, launchd on macOS).
#
# Run with no arguments for a menu, or see --help. Settings are saved in
# ${XDG_CONFIG_HOME:-~/.config}/acd-ctx/install.env so that `update` and
# `configure` reuse them. Every client file is backed up before it is changed.
#
# Compatible with the bash 3.2 that ships with macOS.

set -eu

REPO="fsvxavier/accordion-context"
MODULE="github.com/${REPO}"
IMAGE="ghcr.io/${REPO}"
CONTAINER="acd-ctx"
VOLUME="acd-ctx-data"
SERVICE_NAME="acd-ctx"
LAUNCHD_LABEL="io.github.fsvxavier.acd-ctx"
ANTHROPIC="https://api.anthropic.com"
OPENAI="https://api.openai.com"
GOOGLE="https://generativelanguage.googleapis.com"
ALL_CLIENTS="claude,codex,gemini,opencode"

CONFIG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/acd-ctx"
STATE_FILE="${CONFIG_DIR}/install.env"
STAMP="$(date +%Y%m%d-%H%M%S)"

# Settings. Empty means "not given": the saved state or a default fills it.
CMD=""
METHOD=""
VERSION=""
BIN_DIR=""
FROM_SOURCE=""
FROM_BINARY=""
SCOPE=""
PROJECT=""
LOCAL_STORAGE=""
INTEGRATION=""
CLIENTS=""
GATEWAY=""     # empty: the clients reach the providers directly
GATEWAY_URL=""
HOST=""
PORT=""
SERVICE=""
RESTART_MCP=""
DRY_RUN=0
ASSUME_YES=0
PURGE=0
BIN=""        # absolute path of the installed binary (empty for docker)

# ---------------------------------------------------------------------------
# Output and prompts

if [ -t 1 ]; then
	C_B=$(printf '\033[1m'); C_G=$(printf '\033[32m'); C_Y=$(printf '\033[33m'); C_R=$(printf '\033[31m'); C_0=$(printf '\033[0m')
else
	C_B=""; C_G=""; C_Y=""; C_R=""; C_0=""
fi

info() { printf '%s==>%s %s\n' "$C_G" "$C_0" "$*"; }
warn() { printf '%swarning:%s %s\n' "$C_Y" "$C_0" "$*" >&2; }
die() { printf '%serror:%s %s\n' "$C_R" "$C_0" "$*" >&2; exit 1; }

# run prints the command and runs it, or only prints it with --dry-run.
run() {
	printf "  + %s\n" "$*" >&2
	if [ "$DRY_RUN" = 1 ]; then return 0; fi
	"$@"
}

have() { command -v "$1" >/dev/null 2>&1; }

# The menu reads from the terminal, so it also works under `curl ... | bash`.
TTY=""
if [ -r /dev/tty ] && (exec </dev/tty) 2>/dev/null; then TTY=/dev/tty; fi

ask() { # ask VAR "question" default
	local __v="$1" __q="$2" __d="${3:-}" __a=""
	if [ -z "$TTY" ]; then eval "$__v=\$__d"; return; fi
	if [ -n "$__d" ]; then printf '%s [%s]: ' "$__q" "$__d" >"$TTY"; else printf '%s: ' "$__q" >"$TTY"; fi
	IFS= read -r __a <"$TTY" || true
	if [ -z "$__a" ]; then __a="$__d"; fi
	eval "$__v=\$__a"
}

choose() { # choose VAR "question" default opt1 opt2 ...  (options: value|label)
	local __v="$1" __q="$2" __d="$3" __i=1 __o __a __def=1
	shift 3
	if [ -z "$TTY" ]; then eval "$__v=\$__d"; return; fi
	printf '\n%s%s%s\n' "$C_B" "$__q" "$C_0" >"$TTY"
	for __o in "$@"; do
		if [ "${__o%%|*}" = "$__d" ]; then __def=$__i; fi
		printf '  %d) %s\n' "$__i" "${__o#*|}" >"$TTY"
		__i=$((__i + 1))
	done
	while :; do
		printf 'Choice [%d]: ' "$__def" >"$TTY"
		IFS= read -r __a <"$TTY" || true
		if [ -z "$__a" ]; then __a=$__def; fi
		__i=1
		for __o in "$@"; do
			if [ "$__a" = "$__i" ]; then eval "$__v=\${__o%%|*}"; return; fi
			__i=$((__i + 1))
		done
		printf 'Pick a number from the list.\n' >"$TTY"
	done
}

yesno() { # yesno "question" default(y|n) -> exit status
	local __a
	if [ "$ASSUME_YES" = 1 ]; then return 0; fi
	if [ -z "$TTY" ]; then [ "$2" = y ]; return; fi
	ask __a "$1 (y/n)" "$2"
	case "$__a" in y|Y|yes|s|S|sim) return 0 ;; *) return 1 ;; esac
}

usage() {
	cat <<'EOF'
Usage: install.sh [command] [options]

Commands:
  install      install acd-ctx and configure the clients (default)
  update       update the application to a new version (default: latest),
               restart the service and the MCP servers
  configure    update the configuration: rewrite client files and the service
               from the saved settings plus the options given
  service      start|stop|restart|status|install|remove the proxy service
  status       show the installed version, the service and the proxy health
  uninstall    remove the service and the client entries (--purge also
               removes the binary, the Docker volume and the saved settings)
  menu         interactive menu (the default with no arguments on a terminal)

Install method:
  --method release|go|docker|custom
                         release: download the GitHub release binary (checksum
                         verified); go: go install; docker: ghcr.io image;
                         custom: build --from-source DIR or copy --binary FILE
  --version X            version to install, e.g. 1.0.5 (default: latest)
  --bin-dir DIR          where the binary goes (default ~/.local/bin; for go,
                         GOBIN or GOPATH/bin)
  --from-source DIR      custom: build from this checkout
  --binary FILE          custom: install this binary

Scope and integration:
  --scope global|local   global: user-level client config; local: this project
  --project DIR          project root for --scope local (default: current dir)
  --local-storage        local scope: keep the project database in .acd-ctx/
  --integration proxy|mcp|both|none
  --clients LIST         comma list of claude,codex,gemini,opencode or all
                         (default: claude)
  --gateway NAME         send the proxied traffic on to a gateway instead of
                         the providers: omniroute, freellmapi or bifrost
                         (client -> acd-ctx -> gateway -> provider)
  --gateway-url URL      gateway base URL (default: omniroute
                         http://127.0.0.1:20128, freellmapi http://127.0.0.1:3001,
                         bifrost http://127.0.0.1:8080)
  --host IP              proxy listen address (default 127.0.0.1)
  --port N               proxy port (default 8787)
  --service | --no-service
                         run the proxy as a user service (default: yes for proxy)

Other:
  --restart-mcp | --no-restart-mcp
                         update: stop running `acd-ctx mcp` processes so the
                         clients start the new binary (default: yes)
  --dry-run              print what would change, change nothing
  --yes                  do not ask for confirmation
  --purge                uninstall: also remove binary, volume and settings
  -h, --help             this help

Examples:
  ./install.sh                                     # menu
  ./install.sh install --method go --integration both --clients claude --yes
  ./install.sh install --scope local --project ~/src/app --local-storage \
      --integration both --clients claude,gemini --yes
  ./install.sh install --method docker --integration proxy --port 8787 --yes
  ./install.sh update --version 1.0.5
  ./install.sh configure --port 8788 --clients all
EOF
}

# ---------------------------------------------------------------------------
# Saved settings

load_state() {
	[ -f "$STATE_FILE" ] || return 0
	local k v
	while IFS='=' read -r k v; do
		case "$k" in
		METHOD) S_METHOD=$v ;;
		VERSION) S_VERSION=$v ;;
		BIN_DIR) S_BIN_DIR=$v ;;
		FROM_SOURCE) S_FROM_SOURCE=$v ;;
		SCOPE) [ -z "$SCOPE" ] && SCOPE=$v ;;
		PROJECT) [ -z "$PROJECT" ] && PROJECT=$v ;;
		LOCAL_STORAGE) [ -z "$LOCAL_STORAGE" ] && LOCAL_STORAGE=$v ;;
		INTEGRATION) [ -z "$INTEGRATION" ] && INTEGRATION=$v ;;
		CLIENTS) S_CLIENTS=$v; [ -z "$CLIENTS" ] && CLIENTS=$v ;;
		GATEWAY) [ -z "$GATEWAY" ] && GATEWAY=$v ;;
		GATEWAY_URL) [ -z "$GATEWAY_URL" ] && GATEWAY_URL=$v ;;
		HOST) [ -z "$HOST" ] && HOST=$v ;;
		PORT) [ -z "$PORT" ] && PORT=$v ;;
		SERVICE) [ -z "$SERVICE" ] && SERVICE=$v ;;
		BIN) S_BIN=$v ;;
		esac
	done <"$STATE_FILE"
	# Paths belong to the saved method: drop them when another method is asked for.
	if [ -z "$METHOD" ] || [ "$METHOD" = "$S_METHOD" ]; then
		METHOD=$S_METHOD
		if [ -z "$BIN_DIR" ]; then BIN_DIR=$S_BIN_DIR; fi
		if [ -z "$FROM_SOURCE" ] && [ -z "$FROM_BINARY" ]; then FROM_SOURCE=$S_FROM_SOURCE; fi
	else
		S_BIN=""
	fi
	HAVE_STATE=1
}
S_VERSION=""
S_BIN=""
S_CLIENTS=""
S_METHOD=""
S_BIN_DIR=""
S_FROM_SOURCE=""
HAVE_STATE=0

save_state() {
	if [ "$DRY_RUN" = 1 ]; then info "would save settings to $STATE_FILE"; return; fi
	mkdir -p "$CONFIG_DIR"
	{
		echo "METHOD=$METHOD"
		echo "VERSION=$VERSION"
		echo "BIN_DIR=$BIN_DIR"
		echo "FROM_SOURCE=$FROM_SOURCE"
		echo "BIN=$BIN"
		echo "SCOPE=$SCOPE"
		echo "PROJECT=$PROJECT"
		echo "LOCAL_STORAGE=$LOCAL_STORAGE"
		echo "INTEGRATION=$INTEGRATION"
		echo "CLIENTS=$CLIENTS"
		echo "GATEWAY=$GATEWAY"
		echo "GATEWAY_URL=$GATEWAY_URL"
		echo "HOST=$HOST"
		echo "PORT=$PORT"
		echo "SERVICE=$SERVICE"
	} >"$STATE_FILE"
	info "settings saved to $STATE_FILE"
}

defaults() {
	if [ -z "$METHOD" ]; then METHOD=$(default_method); fi
	# Inside a git repository the default is that project: `acd-ctx mcp` finds its
	# project from the working directory, and only project-scoped proxy settings
	# tell the proxy which project a request belongs to.
	if [ -z "$SCOPE" ]; then
		local top
		top=$(git rev-parse --show-toplevel 2>/dev/null || true)
		if [ -n "$top" ]; then SCOPE=local; if [ -z "$PROJECT" ]; then PROJECT=$top; fi; else SCOPE=global; fi
	fi
	# The containerized MCP server runs in /data and cannot see the project.
	if [ -z "$INTEGRATION" ]; then if [ "$METHOD" = docker ]; then INTEGRATION=proxy; else INTEGRATION=both; fi; fi
	if [ -z "$CLIENTS" ]; then CLIENTS=claude; fi
	if [ "$GATEWAY" = none ]; then GATEWAY=""; GATEWAY_URL=""; fi
	if [ -n "$GATEWAY" ] && [ -z "$GATEWAY_URL" ]; then GATEWAY_URL=$(gateway_default_url "$GATEWAY"); fi
	GATEWAY_URL=${GATEWAY_URL%/}
	if [ "$CLIENTS" = all ]; then CLIENTS=$ALL_CLIENTS; fi
	if [ -z "$HOST" ]; then HOST=127.0.0.1; fi
	if [ -z "$PORT" ]; then PORT=8787; fi
	if [ -z "$LOCAL_STORAGE" ]; then LOCAL_STORAGE=0; fi
	if [ -z "$SERVICE" ]; then
		if uses_proxy; then SERVICE=1; else SERVICE=0; fi
	fi
	if [ "$SCOPE" = local ] && [ -z "$PROJECT" ]; then PROJECT=$(pwd); fi
	if [ -n "$PROJECT" ]; then PROJECT=$(abspath "$PROJECT"); fi
	if [ "$SCOPE" = global ]; then LOCAL_STORAGE=0; fi
	return 0
}

validate() {
	case "$METHOD" in release|go|docker|custom) ;; *) die "--method must be release, go, docker or custom" ;; esac
	case "$SCOPE" in global|local) ;; *) die "--scope must be global or local" ;; esac
	case "$INTEGRATION" in proxy|mcp|both|none) ;; *) die "--integration must be proxy, mcp, both or none" ;; esac
	local c
	for c in $(echo "$CLIENTS" | tr ',' ' '); do
		case "$c" in claude|codex|gemini|opencode) ;; *) die "unknown client: $c (use claude, codex, gemini, opencode)" ;; esac
	done
	case "$GATEWAY" in ''|omniroute|freellmapi|bifrost) ;; *) die "--gateway must be omniroute, freellmapi or bifrost" ;; esac
	case "$PORT" in ''|*[!0-9]*) die "--port must be a number" ;; esac
	if [ "$SCOPE" = local ]; then [ -d "$PROJECT" ] || die "project directory not found: $PROJECT"; fi
	if [ "$METHOD" = docker ] && [ "$LOCAL_STORAGE" = 1 ]; then
		die "--local-storage does not work with --method docker: the container cannot see the project directory"
	fi
	if [ "$METHOD" = custom ] && [ -z "$FROM_SOURCE" ] && [ -z "$FROM_BINARY" ]; then
		die "--method custom needs --from-source DIR or --binary FILE"
	fi
	return 0
}

default_method() {
	if have go; then echo go; elif have docker; then echo docker; else echo release; fi
}

uses_proxy() { [ "$INTEGRATION" = proxy ] || [ "$INTEGRATION" = both ]; }
uses_mcp() { [ "$INTEGRATION" = mcp ] || [ "$INTEGRATION" = both ]; }
has_client() { case ",$CLIENTS," in *",$1,"*) return 0 ;; *) return 1 ;; esac; }

abspath() { (cd "$1" 2>/dev/null && pwd -P) || echo "$1"; }

is_loopback() { case "$1" in 127.*|localhost|::1|'[::1]') return 0 ;; *) return 1 ;; esac; }

# ---------------------------------------------------------------------------
# Platform

detect_platform() {
	case "$(uname -s)" in
	Linux) OS=linux ;;
	Darwin) OS=darwin ;;
	*) die "unsupported system $(uname -s); on Windows use install.ps1" ;;
	esac
	case "$(uname -m)" in
	x86_64|amd64) ARCH=amd64 ;;
	aarch64|arm64) ARCH=arm64 ;;
	*) die "unsupported architecture $(uname -m)" ;;
	esac
}

sha256_of() {
	if have sha256sum; then sha256sum "$1" | awk '{print $1}'; else shasum -a 256 "$1" | awk '{print $1}'; fi
}

fetch() { # fetch URL FILE
	if have curl; then curl -fsSL "$1" -o "$2"; elif have wget; then wget -qO "$2" "$1"; else die "curl or wget is required"; fi
}

fetch_stdout() {
	if have curl; then curl -fsSL "$1"; elif have wget; then wget -qO- "$1"; else return 1; fi
}

# latest_version prints the newest release version without the v.
latest_version() {
	local v=""
	if have go; then
		v=$(cd "${TMPDIR:-/tmp}" && GOFLAGS= go list -m -f '{{.Version}}' "${MODULE}@latest" 2>/dev/null || true)
	fi
	if [ -z "$v" ]; then
		v=$(fetch_stdout "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null |
			sed -n 's/.*"tag_name"[^"]*"\([^"]*\)".*/\1/p' | head -n1 || true)
	fi
	[ -n "$v" ] || die "could not find the latest version; pass --version"
	echo "${v#v}"
}

resolve_version() {
	if [ -z "$VERSION" ] || [ "$VERSION" = latest ]; then
		if [ "$METHOD" = custom ] && [ -n "$FROM_SOURCE" ]; then
			VERSION=$(cd "$FROM_SOURCE" && git describe --tags --always 2>/dev/null || echo dev)
		else
			VERSION=$(latest_version)
		fi
	fi
	VERSION=${VERSION#v}
}

go_bin_dir() {
	local d
	d=$(go env GOBIN 2>/dev/null || true)
	[ -n "$d" ] || d="$(go env GOPATH 2>/dev/null | cut -d: -f1)/bin"
	echo "$d"
}

# ---------------------------------------------------------------------------
# Install methods

install_binary() {
	case "$METHOD" in
	release) install_release ;;
	go) install_go ;;
	custom) install_custom ;;
	docker) install_docker ;;
	esac
}

# place_binary moves a built binary into BIN_DIR without overwriting it in
# place: a rename keeps running `acd-ctx serve` and `acd-ctx mcp` processes on
# the old inode.
place_binary() { # place_binary SRC
	run mkdir -p "$BIN_DIR"
	run cp "$1" "$BIN_DIR/.acd-ctx.new"
	run chmod 755 "$BIN_DIR/.acd-ctx.new"
	run mv -f "$BIN_DIR/.acd-ctx.new" "$BIN_DIR/acd-ctx"
	BIN="$BIN_DIR/acd-ctx"
}

install_release() {
	[ -n "$BIN_DIR" ] || BIN_DIR="$HOME/.local/bin"
	local name="acd-ctx_${VERSION}_${OS}_${ARCH}.tar.gz"
	local base="https://github.com/${REPO}/releases/download/v${VERSION}"
	info "downloading $name"
	if [ "$DRY_RUN" = 1 ]; then
		printf '  + fetch %s/%s (and checksums.txt)\n' "$base" "$name"
		BIN="$BIN_DIR/acd-ctx"
		place_binary "<extracted acd-ctx>"
		return
	fi
	local tmp
	tmp=$(mktemp -d)
	if ! fetch "$base/$name" "$tmp/$name" || ! fetch "$base/checksums.txt" "$tmp/checksums.txt"; then
		rm -rf "$tmp"
		die "release v${VERSION} has no binary for ${OS}/${ARCH} (or it could not be downloaded). Use --method go or --method docker."
	fi
	if [ ! -s "$tmp/$name" ] || [ ! -s "$tmp/checksums.txt" ]; then
		rm -rf "$tmp"
		die "release v${VERSION} has no binary for ${OS}/${ARCH}. Use --method go or --method docker."
	fi
	local want got
	want=$(awk -v n="$name" '$2==n || $2=="*"n {print $1}' "$tmp/checksums.txt")
	got=$(sha256_of "$tmp/$name")
	if [ -z "$want" ] || [ "$want" != "$got" ]; then
		rm -rf "$tmp"
		die "checksum mismatch for $name"
	fi
	tar -xzf "$tmp/$name" -C "$tmp" acd-ctx
	place_binary "$tmp/acd-ctx"
	rm -rf "$tmp"
}

install_go() {
	have go || die "go is not installed; use --method release or --method docker"
	local gobin
	gobin=$(go_bin_dir)
	[ -n "$BIN_DIR" ] || BIN_DIR=$gobin
	info "go install ${MODULE}/cmd/acd-ctx@v${VERSION}"
	# Without -X main.version the binary reports "dev". Same flags as .goreleaser.yaml.
	(cd "${TMPDIR:-/tmp}" && run env GOFLAGS= GOBIN="$BIN_DIR" go install -trimpath \
		-ldflags "-s -w -X main.version=${VERSION}" "${MODULE}/cmd/acd-ctx@v${VERSION}")
	BIN="$BIN_DIR/acd-ctx"
}

install_custom() {
	[ -n "$BIN_DIR" ] || BIN_DIR="$HOME/.local/bin"
	if [ -n "$FROM_BINARY" ]; then
		[ -f "$FROM_BINARY" ] || die "binary not found: $FROM_BINARY"
		place_binary "$FROM_BINARY"
		return
	fi
	if [ -z "$FROM_SOURCE" ]; then die "--method custom needs --from-source DIR or --binary FILE"; fi
	have go || die "go is required to build from source"
	FROM_SOURCE=$(abspath "$FROM_SOURCE")
	[ -f "$FROM_SOURCE/cmd/acd-ctx/main.go" ] || die "not an accordion-context checkout: $FROM_SOURCE"
	info "building $FROM_SOURCE (version $VERSION)"
	local tmp
	tmp=$(mktemp -d)
	(cd "$FROM_SOURCE" && run env CGO_ENABLED=0 go build -trimpath \
		-ldflags "-s -w -X main.version=${VERSION}" -o "$tmp/acd-ctx" ./cmd/acd-ctx)
	place_binary "$tmp/acd-ctx"
	rm -rf "$tmp"
}

# Gateways (docs/integrations.md): the client talks to acd-ctx, and acd-ctx
# forwards to the gateway's surface for that wire format instead of the
# provider. Ports are the gateways' documented defaults.
gateway_default_url() {
	local h=127.0.0.1
	# From inside the container, the host's loopback is host.docker.internal.
	if [ "$METHOD" = docker ]; then h=host.docker.internal; fi
	case "$1" in
	omniroute) echo "http://$h:20128" ;;
	freellmapi) echo "http://$h:3001" ;;
	bifrost) echo "http://$h:8080" ;;
	esac
}

# upstream KIND: where acd-ctx forwards one client's traffic. KIND is
# anthropic (Claude Code: the client appends /v1/messages), openai (Codex),
# google (Gemini CLI) or anthropic-v1 (OpenCode's Anthropic provider).
# Prints nothing when the gateway has no documented surface for KIND.
upstream() {
	local g="$GATEWAY_URL"
	case "$GATEWAY:$1" in
	:anthropic) echo "$ANTHROPIC" ;;
	:openai) echo "$OPENAI/v1" ;;
	:google) echo "$GOOGLE" ;;
	:anthropic-v1) echo "$ANTHROPIC/v1" ;;
	omniroute:anthropic|freellmapi:anthropic) echo "$g" ;;
	omniroute:openai|freellmapi:openai|omniroute:anthropic-v1|freellmapi:anthropic-v1) echo "$g/v1" ;;
	freellmapi:google) echo "$g" ;;
	omniroute:google) ;;
	bifrost:anthropic) echo "$g/anthropic" ;;
	bifrost:openai) echo "$g/openai" ;;
	bifrost:google) echo "$g/genai" ;;
	bifrost:anthropic-v1) echo "$g/anthropic/v1" ;;
	esac
}

origin_of() { echo "$1" | sed -E 's#^(https?://[^/]+).*#\1#'; }

proxy_allow() {
	local a="$ANTHROPIC"
	if [ -n "$GATEWAY" ]; then
		a="$a,$(origin_of "$GATEWAY_URL")"
	else
		if has_client codex; then a="$a,$OPENAI"; fi
		if has_client gemini; then a="$a,$GOOGLE"; fi
	fi
	echo "$a"
}

allowed_hosts() {
	case "$1" in ''|0.0.0.0|::|'[::]') echo "localhost,127.0.0.1,[::1]" ;; *) echo "localhost,127.0.0.1,[::1],$1" ;; esac
}

install_docker() {
	have docker || die "docker is not installed"
	BIN=""
	local img="${IMAGE}:${VERSION}"
	info "docker image $img"
	run docker pull "$img"
	run docker volume create "$VOLUME" >/dev/null
	docker_recreate
}

docker_recreate() {
	local img="${IMAGE}:${VERSION}" hosts="localhost,127.0.0.1,[::1],host.docker.internal"
	if ! is_loopback "$HOST"; then
		warn "publishing the proxy on $HOST: anyone who can reach it can use it (the Host allowlist is not authentication)"
		hosts="$(allowed_hosts "$HOST"),host.docker.internal"
	fi
	if [ "$DRY_RUN" = 1 ] || docker container inspect "$CONTAINER" >/dev/null 2>&1; then
		run docker rm -f "$CONTAINER" >/dev/null
	fi
	run docker run -d --name "$CONTAINER" --restart unless-stopped \
		-p "${HOST}:${PORT}:8787" -v "${VOLUME}:/data" \
		--add-host=host.docker.internal:host-gateway \
		--read-only --tmpfs /tmp --cap-drop ALL --security-opt no-new-privileges:true \
		-e "ACD_CTX_ALLOW=$(proxy_allow)" -e "ACD_CTX_ALLOWED_HOSTS=$hosts" \
		"$img" >/dev/null
}

# ---------------------------------------------------------------------------
# Service (systemd --user on Linux, launchd on macOS)

serve_args() {
	local a="serve --listen ${HOST}:${PORT} --allow $(proxy_allow)"
	if ! is_loopback "$HOST"; then
		a="$a --allow-non-loopback --allowed-hosts $(allowed_hosts "$HOST")"
	fi
	echo "$a"
}

unit_path() { echo "${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/${SERVICE_NAME}.service"; }
plist_path() { echo "$HOME/Library/LaunchAgents/${LAUNCHD_LABEL}.plist"; }

service_install() {
	if [ "$METHOD" = docker ]; then info "docker: the container restarts on its own (--restart unless-stopped)"; return; fi
	[ -n "$BIN" ] || die "no binary to run as a service"
	if ! is_loopback "$HOST"; then
		warn "listening on $HOST: any peer that can reach the port gets the proxy (the Host allowlist is not authentication)"
	fi
	if [ "$OS" = linux ]; then
		have systemctl || { warn "systemctl not found; start the proxy with: $BIN $(serve_args)"; return; }
		local unit
		unit=$(unit_path)
		write_file "$unit" "[Unit]
Description=acd-ctx proxy (context compaction for coding agents)
After=network.target

[Service]
Type=simple
ExecStart=${BIN} $(serve_args)
Restart=on-failure
RestartSec=2

[Install]
WantedBy=default.target
"
		run systemctl --user daemon-reload
		run systemctl --user enable "${SERVICE_NAME}.service"
		run systemctl --user restart "${SERVICE_NAME}.service"
		info "to keep the service running without an open login: loginctl enable-linger \$USER"
	else
		local plist args="" a
		plist=$(plist_path)
		# No globbing: an allowed host such as [::1] is a bracket pattern.
		set -f
		for a in $BIN $(serve_args); do args="$args    <string>$a</string>
"; done
		set +f
		write_file "$plist" "<?xml version=\"1.0\" encoding=\"UTF-8\"?>
<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">
<plist version=\"1.0\">
<dict>
  <key>Label</key><string>${LAUNCHD_LABEL}</string>
  <key>ProgramArguments</key>
  <array>
${args}  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>${HOME}/Library/Logs/acd-ctx.log</string>
  <key>StandardErrorPath</key><string>${HOME}/Library/Logs/acd-ctx.log</string>
</dict>
</plist>
"
		run launchctl bootout "gui/$(id -u)" "$plist" 2>/dev/null || true
		run launchctl bootstrap "gui/$(id -u)" "$plist"
	fi
}

service_remove() {
	if [ "$METHOD" = docker ]; then
		if [ "$DRY_RUN" = 1 ] || docker container inspect "$CONTAINER" >/dev/null 2>&1; then run docker rm -f "$CONTAINER"; fi
		return
	fi
	if [ "$OS" = linux ]; then
		local unit
		unit=$(unit_path)
		[ -f "$unit" ] || return 0
		run systemctl --user disable --now "${SERVICE_NAME}.service" || true
		backup_file "$unit"
		run rm -f "$unit"
		run systemctl --user daemon-reload
	else
		local plist
		plist=$(plist_path)
		[ -f "$plist" ] || return 0
		run launchctl bootout "gui/$(id -u)" "$plist" || true
		run rm -f "$plist"
	fi
}

service_ctl() { # service_ctl start|stop|restart|status
	if [ "$METHOD" = docker ]; then
		case "$1" in
		status) docker ps -a --filter "name=^/${CONTAINER}$" --format '{{.Names}}  {{.Status}}  {{.Image}}' ;;
		*) run docker "$1" "$CONTAINER" ;;
		esac
		return
	fi
	if [ "$OS" = linux ]; then
		case "$1" in
		status) systemctl --user status "${SERVICE_NAME}.service" --no-pager || true ;;
		*) run systemctl --user "$1" "${SERVICE_NAME}.service" ;;
		esac
	else
		local t="gui/$(id -u)/${LAUNCHD_LABEL}"
		case "$1" in
		start) run launchctl bootstrap "gui/$(id -u)" "$(plist_path)" ;;
		stop) run launchctl bootout "gui/$(id -u)" "$(plist_path)" ;;
		restart) run launchctl kickstart -k "$t" ;;
		status) launchctl print "$t" 2>/dev/null | sed -n '1,12p' || echo "not loaded" ;;
		esac
	fi
}

service_restart_if_installed() {
	if [ "$METHOD" = docker ]; then docker_recreate; return; fi
	if [ "$SERVICE" = 1 ]; then service_install; fi
}

# ---------------------------------------------------------------------------
# File editing: every change goes through a backup, and --dry-run only prints.

backup_file() {
	[ -f "$1" ] || return 0
	if [ "$DRY_RUN" = 1 ]; then printf '  + backup %s\n' "$1"; return; fi
	# The first backup of a run is the original; keep it.
	if [ ! -e "$1.bak-acd-ctx-$STAMP" ]; then cp -p "$1" "$1.bak-acd-ctx-$STAMP"; fi
}

write_file() { # write_file PATH CONTENT
	if [ -f "$1" ] && [ "$(cat "$1")" = "$(printf "%s" "$2")" ]; then return 0; fi
	if [ "$DRY_RUN" = 1 ]; then printf '  + write %s\n' "$1"; printf '%s' "$2" | sed 's/^/      | /'; return; fi
	mkdir -p "$(dirname "$1")"
	backup_file "$1"
	printf '%s' "$2" >"$1"
	info "wrote $1"
}

# json_edit FILE OP PATH [VALUE] ...  — OPs: set, del, append, remove.
# PATH separates keys with "/". VALUE is JSON. Needs python3. Exit 3 when the
# file is not plain JSON (for example JSONC with comments): it is left alone.
json_edit() {
	local f="$1"
	shift
	if ! have python3; then
		warn "python3 not found: edit $f by hand ($*)"
		return 0
	fi
	local out rc=0
	out=$(ACD_DRY="$DRY_RUN" ACD_STAMP="$STAMP" python3 - "$f" "$@" <<'PY'
import json, os, shutil, sys
f, ops = sys.argv[1], sys.argv[2:]
data = {}
if os.path.exists(f) and os.path.getsize(f) > 0:
    try:
        with open(f, encoding="utf-8") as h:
            data = json.load(h)
    except ValueError as e:
        print("not plain JSON (%s)" % e)
        sys.exit(3)
before = json.dumps(data, sort_keys=True)
i = 0
while i < len(ops):
    op, path = ops[i], ops[i + 1].split("/")
    i += 2
    val = None
    if op in ("set", "append", "remove"):
        val = json.loads(ops[i])
        i += 1
    cur = data
    for k in path[:-1]:
        if not isinstance(cur, dict):
            cur = None
            break
        if k not in cur:
            if op in ("del", "remove"):
                cur = None
                break
            cur[k] = {}
        cur = cur[k]
    if not isinstance(cur, dict):
        continue
    last = path[-1]
    if op == "set":
        cur[last] = val
    elif op == "del":
        cur.pop(last, None)
    elif op == "append":
        lst = cur.setdefault(last, [])
        if val not in lst:
            lst.append(val)
    elif op == "remove":
        if isinstance(cur.get(last), list) and val in cur[last]:
            cur[last].remove(val)
if json.dumps(data, sort_keys=True) == before:
    print("unchanged")
    sys.exit(0)
if os.environ.get("ACD_DRY") == "1":
    print("would change")
    sys.exit(0)
if os.path.exists(f) and not os.path.exists(f + ".bak-acd-ctx-" + os.environ["ACD_STAMP"]):
    shutil.copy2(f, f + ".bak-acd-ctx-" + os.environ["ACD_STAMP"])
d = os.path.dirname(f)
if d:
    os.makedirs(d, exist_ok=True)
with open(f + ".tmp-acd-ctx", "w", encoding="utf-8") as h:
    json.dump(data, h, indent=2, ensure_ascii=False)
    h.write("\n")
os.replace(f + ".tmp-acd-ctx", f)
print("updated")
PY
) || rc=$?
	if [ $rc = 3 ]; then warn "$f: $out; edit it by hand ($*)"; return 0; fi
	[ $rc = 0 ] || die "editing $f failed"
	case "$out" in
	updated) info "updated $f" ;;
	"would change") printf '  + edit %s: %s\n' "$f" "$*" ;;
	esac
}

# set_block FILE NAME top|bottom CONTENT — replaces the block between the
# acd-ctx markers (or removes it when CONTENT is empty). Used for TOML and
# .env files.
set_block() {
	local f="$1" name="$2" pos="$3" content="$4"
	local b="# >>> acd-ctx:${name} (managed by install.sh) >>>" e="# <<< acd-ctx:${name} <<<"
	local rest new
	if [ -f "$f" ]; then
		rest=$(awk -v b="$b" -v e="$e" '$0==b{s=1;next} $0==e{s=0;next} !s' "$f")
	else
		rest=""
		if [ -z "$content" ]; then return 0; fi
	fi
	if [ -z "$content" ]; then
		new="$rest"
	elif [ "$pos" = top ]; then
		new="$b
$content
$e${rest:+
$rest}"
	else
		new="${rest:+$rest
}$b
$content
$e"
	fi
	new="$new
"
	write_file "$f" "$new"
}

# outside_block FILE NAME REGEX: true when REGEX matches outside the managed block.
outside_block() {
	[ -f "$1" ] || return 1
	local b="# >>> acd-ctx:${2} (managed by install.sh) >>>" e="# <<< acd-ctx:${2} <<<"
	awk -v b="$b" -v e="$e" '$0==b{s=1;next} $0==e{s=0;next} !s' "$1" | grep -Eq "$3"
}

# ---------------------------------------------------------------------------
# Clients

project_id() {
	[ -n "$BIN" ] || return 0
	(cd "$PROJECT" && "$BIN" project show 2>/dev/null | awk '$1=="project"{print $2}') || true
}

# base_url UPSTREAM: the proxy URL for a client. Local scope adds the project
# prefix /p/<id>/ for clients that cannot send the X-Acd-Ctx-Project-Dir header.
base_url() {
	local p=""
	if [ "$SCOPE" = local ] && [ -n "$PID_" ]; then p="/p/${PID_}"; fi
	echo "http://$(url_host)${p}/ctx/$1"
}
url_host() {
	case "$HOST" in 0.0.0.0) echo "127.0.0.1:${PORT}" ;; *:*) echo "[${HOST}]:${PORT}" ;; *) echo "${HOST}:${PORT}" ;; esac
}

mcp_cmd_json() { # JSON array: command + args
	if [ "$METHOD" = docker ]; then
		echo "[\"docker\",\"exec\",\"-i\",\"${CONTAINER}\",\"/acd-ctx\",\"mcp\"]"
	else
		echo "[\"${BIN}\",\"mcp\"]"
	fi
}
mcp_command() { if [ "$METHOD" = docker ]; then echo docker; else echo "$BIN"; fi; }
mcp_args_json() {
	if [ "$METHOD" = docker ]; then echo "[\"exec\",\"-i\",\"${CONTAINER}\",\"/acd-ctx\",\"mcp\"]"; else echo '["mcp"]'; fi
}
mcp_args_toml() {
	if [ "$METHOD" = docker ]; then echo "[\"exec\", \"-i\", \"${CONTAINER}\", \"/acd-ctx\", \"mcp\"]"; else echo '["mcp"]'; fi
}

configure_claude() { # configure_claude apply|remove
	local settings mcpfile
	if [ "$SCOPE" = local ]; then
		settings="$PROJECT/.claude/settings.local.json"
		mcpfile="$PROJECT/.mcp.json"
	else
		settings="$HOME/.claude/settings.json"
	fi
	if [ "$1" = apply ] && uses_proxy; then
		# Direct: the proxy's default upstream is Anthropic, so the bare address
		# works. Through a gateway the /ctx/ prefix names its Anthropic surface.
		local url="http://$(url_host)"
		if [ -n "$GATEWAY" ]; then url="$url/ctx/$(upstream anthropic)"; fi
		json_edit "$settings" set env/ANTHROPIC_BASE_URL "\"$url\"" \
			set env/CLAUDE_CODE_GATEWAY_HINT_HEADERS '"1"'
		if [ "$SCOPE" = local ]; then
			json_edit "$settings" set env/ANTHROPIC_CUSTOM_HEADERS "\"X-Acd-Ctx-Project-Dir: ${PROJECT}\""
		fi
	else
		json_edit "$settings" del env/ANTHROPIC_BASE_URL del env/CLAUDE_CODE_GATEWAY_HINT_HEADERS
		if [ "$SCOPE" = local ]; then json_edit "$settings" del env/ANTHROPIC_CUSTOM_HEADERS; fi
	fi
	if [ "$1" = apply ] && uses_mcp; then
		if [ "$SCOPE" = local ]; then
			json_edit "$mcpfile" set mcpServers/acd-ctx "{\"command\":\"$(mcp_command)\",\"args\":$(mcp_args_json)}"
			json_edit "$settings" append enabledMcpjsonServers '"acd-ctx"'
		elif have claude; then
			run claude mcp remove --scope user acd-ctx >/dev/null 2>&1 || true
			if [ "$METHOD" = docker ]; then
				run claude mcp add --scope user acd-ctx -- docker exec -i "$CONTAINER" /acd-ctx mcp
			else
				run claude mcp add --scope user acd-ctx -- "$BIN" mcp
			fi
		else
			warn "claude CLI not found; register the MCP server with: claude mcp add --scope user acd-ctx -- $(mcp_command) mcp"
		fi
	else
		if [ "$SCOPE" = local ]; then
			if [ -f "$mcpfile" ]; then json_edit "$mcpfile" del mcpServers/acd-ctx; fi
			json_edit "$settings" remove enabledMcpjsonServers '"acd-ctx"'
		elif have claude; then
			run claude mcp remove --scope user acd-ctx >/dev/null 2>&1 || true
		fi
	fi
}

configure_codex() {
	local global="$HOME/.codex/config.toml" mcpfile
	if [ "$SCOPE" = local ]; then mcpfile="$PROJECT/.codex/config.toml"; else mcpfile="$global"; fi
	# Codex ignores openai_base_url in a project .codex/config.toml, so the
	# proxy setting always goes into the user file.
	if [ "$1" = apply ] && uses_proxy; then
		if outside_block "$global" proxy '^[[:space:]]*openai_base_url[[:space:]]*='; then
			warn "$global already sets openai_base_url; not changed. Set it to $(base_url "$(upstream openai)")"
		else
			if [ "$SCOPE" = local ]; then warn "codex: the proxy URL goes into $global (Codex ignores it in a project file), so it applies to every project"; fi
			local saved="$SCOPE"
			SCOPE=global
			set_block "$global" proxy top "openai_base_url = \"$(base_url "$(upstream openai)")\""
			SCOPE="$saved"
		fi
	else
		set_block "$global" proxy top ""
	fi
	if [ "$1" = apply ] && uses_mcp; then
		if outside_block "$mcpfile" mcp '^\[mcp_servers\.("?)acd-ctx("?)\]'; then
			warn "$mcpfile already has [mcp_servers.acd-ctx]; not changed"
		else
			set_block "$mcpfile" mcp bottom "[mcp_servers.acd-ctx]
command = \"$(mcp_command)\"
args = $(mcp_args_toml)"
			if [ "$SCOPE" = local ]; then info "codex loads $mcpfile only when the project is trusted"; fi
		fi
	else
		set_block "$mcpfile" mcp bottom ""
	fi
}

configure_gemini() {
	local dir
	if [ "$SCOPE" = local ]; then dir="$PROJECT/.gemini"; else dir="$HOME/.gemini"; fi
	if [ "$1" = apply ] && uses_proxy && [ -z "$(upstream google)" ]; then
		warn "gemini: $GATEWAY has no documented Gemini surface; the Gemini proxy setting is not written"
		set_block "$dir/.env" proxy bottom ""
	elif [ "$1" = apply ] && uses_proxy; then
		if outside_block "$dir/.env" proxy '^[[:space:]]*(export[[:space:]]+)?GOOGLE_GEMINI_BASE_URL='; then
			warn "$dir/.env already sets GOOGLE_GEMINI_BASE_URL; not changed"
		else
			set_block "$dir/.env" proxy bottom "GOOGLE_GEMINI_BASE_URL=$(base_url "$(upstream google)")"
			info "gemini: GOOGLE_GEMINI_BASE_URL is used only with API-key auth (gemini-api-key)"
			is_loopback "$HOST" || warn "gemini accepts a plain http base URL only for localhost"
		fi
	else
		set_block "$dir/.env" proxy bottom ""
	fi
	if [ "$1" = apply ] && uses_mcp; then
		json_edit "$dir/settings.json" set mcpServers/acd-ctx "{\"command\":\"$(mcp_command)\",\"args\":$(mcp_args_json)}"
	else
		if [ -f "$dir/settings.json" ]; then json_edit "$dir/settings.json" del mcpServers/acd-ctx; fi
	fi
}

configure_opencode() {
	local f
	if [ "$SCOPE" = local ]; then f="$PROJECT/opencode.json"; else f="${XDG_CONFIG_HOME:-$HOME/.config}/opencode/opencode.json"; fi
	if [ ! -f "$f" ] && [ -f "${f%.json}.jsonc" ]; then
		warn "${f%.json}.jsonc has comments and is not edited; add the acd-ctx entries by hand"
		return 0
	fi
	if [ "$1" = apply ] && uses_proxy; then
		json_edit "$f" set provider/anthropic/options/baseURL "\"$(base_url "$(upstream anthropic-v1)")\""
	else
		if [ -f "$f" ]; then json_edit "$f" del provider/anthropic/options/baseURL; fi
	fi
	# OpenCode 2 keeps servers under mcp.servers; 1.x put them directly under mcp.
	local path="mcp/servers/acd-ctx" entry="{\"type\":\"local\",\"command\":$(mcp_cmd_json)}"
	if [ -f "$f" ] && have python3 && python3 - "$f" <<'PY' 2>/dev/null; then
import json, sys
m = json.load(open(sys.argv[1], encoding="utf-8")).get("mcp", {})
sys.exit(0 if m and "servers" not in m and any(isinstance(v, dict) and "type" in v for v in m.values()) else 1)
PY
		path="mcp/acd-ctx"
		entry="{\"type\":\"local\",\"command\":$(mcp_cmd_json),\"enabled\":true}"
	fi
	if [ "$1" = apply ] && uses_mcp; then
		json_edit "$f" set "$path" "$entry"
	else
		if [ -f "$f" ]; then json_edit "$f" del "$path"; fi
	fi
}

configure_project() {
	[ "$SCOPE" = local ] || return 0
	if [ -z "$BIN" ]; then return 0; fi
	if [ -d "$PROJECT/.acd-ctx" ]; then
		if [ "$LOCAL_STORAGE" = 1 ] && ! grep -Eq '^storage:[[:space:]]*local' "$PROJECT/.acd-ctx/config.yaml" 2>/dev/null; then
			warn "$PROJECT/.acd-ctx exists without storage: local; add 'storage: local' to its config.yaml or run: acd-ctx project migrate --to local"
		fi
		return 0
	fi
	if [ "$LOCAL_STORAGE" = 1 ]; then
		(cd "$PROJECT" && run "$BIN" project init --local-storage)
	else
		(cd "$PROJECT" && run "$BIN" project init)
	fi
}

configure_clients() { # configure_clients apply|remove
	PID_=""
	if [ "$SCOPE" = local ] && [ "$1" = apply ]; then
		configure_project
		PID_=$(project_id)
	fi
	local c
	for c in $(echo "$CLIENTS" | tr ',' ' '); do
		info "client: $c"
		"configure_$c" "$1"
	done
	if [ "$1" = apply ] && uses_proxy && [ -n "$GATEWAY" ]; then gateway_notes; fi
	if [ "$1" = apply ] && [ "$SCOPE" = global ] && uses_proxy && uses_mcp; then
		warn "global scope: the proxy files these requests under the 'default' project, while acd-ctx mcp looks up the project of the working directory, so the MCP recall tools do not see them. Use --scope local in each project, or start the clients with the launchers (acd-ctx claude)."
	fi
	if [ "$1" = apply ] && [ "$METHOD" = docker ] && uses_mcp; then
		warn "docker: acd-ctx mcp runs inside the container (working directory /data), so it does not see your project's sessions; prefer --integration proxy with docker"
	fi
	if [ "$1" = apply ] && [ "$SCOPE" = global ] && uses_proxy; then
		info "global proxy settings carry no project signal: requests go to the 'default' project. Use --scope local in a project, or the launchers (acd-ctx claude), to separate projects."
	fi
	local others
	others=$(echo ",$CLIENTS," | sed -e "s/,claude,/,/" -e "s/^,//" -e "s/,$//")
	if [ "$1" = apply ] && [ "$SCOPE" = local ] && uses_proxy && [ -n "$others" ]; then
		info "$others reach the proxy through /p/<id>/, which uses the global database; only Claude Code sends the project directory, which local storage needs."
	fi
}

# gateway_notes prints what the user still has to set up on the gateway side.
gateway_notes() {
	info "gateway $GATEWAY at $GATEWAY_URL: the clients now send the gateway's key, not the provider's"
	case "$GATEWAY" in
	bifrost)
		info "bifrost: Claude Code needs ANTHROPIC_AUTH_TOKEN=<virtual key sk-bf-...>, and anthropic-beta must be in the header AllowList (Settings, Client Settings) or beta features drop"
		;;
	omniroute)
		info "omniroute: Claude Code needs ANTHROPIC_AUTH_TOKEN=<OmniRoute key>; Gemini CLI has no OmniRoute surface"
		;;
	freellmapi)
		info "freellmapi: set the issued freellmapi-... token as the client key (ANTHROPIC_AUTH_TOKEN, OPENAI_API_KEY, GEMINI_API_KEY)"
		;;
	esac
	if [ "$METHOD" = docker ]; then
		case "$GATEWAY_URL" in *127.0.0.1*|*localhost*) warn "the container cannot reach $GATEWAY_URL; use http://host.docker.internal:<port>" ;; esac
		info "docker: the gateway must listen on an address the container reaches (not only the host's 127.0.0.1)"
	fi
	info "the chain client -> acd-ctx -> gateway is documented, not verified (docs/integrations.md)"
}

restart_mcp() {
	have pgrep || return 0
	local pids
	# The [a] keeps pgrep from matching this shell's own command line.
	pids=$(pgrep -u "$(id -u)" -f '[a]cd-ctx mcp' || true)
	[ -n "$pids" ] || { info "no acd-ctx MCP server is running"; return 0; }
	info "stopping acd-ctx MCP servers ($(echo $pids)); the clients start the new binary on the next tool call, or reconnect with /mcp"
	# shellcheck disable=SC2086
	run kill -TERM $pids || true
}

health() {
	local url="http://$(url_host)/__acd-ctx/health" i
	if [ "$DRY_RUN" = 1 ]; then return 0; fi
	have curl || return 0
	for i in 1 2 3 4 5 6 7 8 9 10; do
		if curl -fsS -m 2 "$url" >/dev/null 2>&1; then info "proxy healthy at $url"; return 0; fi
		sleep 1
	done
	warn "proxy did not answer at $url"
}

# ---------------------------------------------------------------------------
# Commands

summary() {
	cat <<EOF

${C_B}${1:-Plan}${C_0}
  method       $METHOD${VERSION:+ (version $VERSION)}${BIN_DIR:+, bin dir $BIN_DIR}
  scope        $SCOPE$([ "$SCOPE" = local ] && echo " ($PROJECT)")$([ "$LOCAL_STORAGE" = 1 ] && echo ", local storage")
  integration  $INTEGRATION
  clients      $CLIENTS
  upstream     $(if [ -n "$GATEWAY" ]; then echo "gateway $GATEWAY ($GATEWAY_URL)"; else echo "providers (direct)"; fi)
  proxy        ${HOST}:${PORT}$(if [ "$METHOD" = docker ]; then echo ", container $CONTAINER"; elif [ "$SERVICE" = 1 ]; then echo ", user service"; fi)
EOF
}

cmd_install() {
	defaults
	validate
	resolve_version
	summary
	yesno "Proceed?" y || die "cancelled"
	install_binary
	if [ -n "$BIN" ] && [ "$DRY_RUN" = 0 ]; then info "installed: $("$BIN" version 2>/dev/null || echo "$BIN")"; fi
	if [ -n "$BIN" ] && [ -n "$BIN_DIR" ]; then case ":$PATH:" in *":$BIN_DIR:"*) ;; *) warn "$BIN_DIR is not in PATH" ;; esac; fi
	if uses_proxy && [ "$SERVICE" = 1 ]; then service_install; health; fi
	[ "$INTEGRATION" = none ] || configure_clients apply
	save_state
	info "done"
}

cmd_update() {
	[ "$HAVE_STATE" = 1 ] || die "no saved installation ($STATE_FILE); run install first"
	defaults
	BIN=$S_BIN
	resolve_version
	info "updating from ${S_VERSION:-unknown} to $VERSION ($METHOD)"
	yesno "Proceed?" y || die "cancelled"
	install_binary
	if [ "$METHOD" != docker ] && [ "$SERVICE" = 1 ]; then
		service_install
	fi
	if [ "$SERVICE" = 1 ] || [ "$METHOD" = docker ]; then health; fi
	# A docker MCP entry runs `docker exec` in the container, which was recreated.
	if [ "${RESTART_MCP:-1}" = 1 ]; then restart_mcp; fi
	# The client files name the binary; rewrite them only if its path changed.
	if [ "$INTEGRATION" != none ] && [ "$BIN" != "$S_BIN" ]; then configure_clients apply; fi
	save_state
	info "updated to $VERSION"
}

cmd_configure() {
	[ "$HAVE_STATE" = 1 ] || warn "no saved installation; using defaults"
	defaults
	BIN=$S_BIN
	VERSION=$S_VERSION
	if [ -z "$BIN" ] && [ "$METHOD" != docker ]; then BIN=$(command -v acd-ctx || true); fi
	[ -n "$BIN" ] || [ "$METHOD" = docker ] || die "acd-ctx is not installed; run install first"
	validate
	summary
	yesno "Apply?" y || die "cancelled"
	if uses_proxy; then
		if [ "$METHOD" = docker ]; then docker_recreate; elif [ "$SERVICE" = 1 ]; then service_install; else service_remove; fi
		health
	fi
	remove_dropped_clients
	configure_clients apply
	save_state
}

# remove_dropped_clients removes the entries of clients that were configured
# before but are not in the new list.
remove_dropped_clients() {
	local c keep="$CLIENTS"
	for c in $(echo "$S_CLIENTS" | tr "," " "); do
		if ! has_client "$c"; then
			info "client $c is no longer selected: removing its acd-ctx entries"
			CLIENTS=$c
			"configure_$c" remove
			CLIENTS=$keep
		fi
	done
}

cmd_uninstall() {
	defaults
	BIN=$S_BIN
	summary
	yesno "Remove the service and the acd-ctx client entries?" n || die "cancelled"
	service_remove
	configure_clients remove
	if [ "$PURGE" = 1 ]; then
		if [ "$METHOD" = docker ]; then run docker volume rm "$VOLUME" || true; fi
		if [ -n "$BIN" ] && [ "$METHOD" != docker ]; then run rm -f "$BIN"; fi
		run rm -f "$STATE_FILE"
		info "the database (~/.local/share/acd-ctx and any .acd-ctx/ of a project) is kept; remove it with acd-ctx clean or by hand"
	fi
	info "uninstalled"
}

cmd_status() {
	defaults
	BIN=${S_BIN:-$(command -v acd-ctx || true)}
	echo "settings     $STATE_FILE$([ "$HAVE_STATE" = 1 ] || echo ' (none)')"
	summary Settings
	if [ "$METHOD" = docker ]; then
		echo "image        $(docker inspect -f '{{.Config.Image}}' "$CONTAINER" 2>/dev/null || echo 'no container')"
	elif [ -n "$BIN" ]; then
		echo "binary       $BIN ($("$BIN" version 2>/dev/null || echo 'not runnable'))"
	fi
	echo
	service_ctl status
	health
}

cmd_service() {
	defaults
	BIN=${S_BIN:-$(command -v acd-ctx || true)}
	case "${SUB:-status}" in
	start|stop|restart|status) service_ctl "$SUB" ;;
	install) SERVICE=1; service_install; save_state ;;
	remove) service_remove; SERVICE=0; save_state ;;
	*) die "service: use start, stop, restart, status, install or remove" ;;
	esac
}

ask_clients() {
	local d="${CLIENTS:-claude}" a
	ask a "Clients (comma list of claude,codex,gemini,opencode, or all)" "$d"
	CLIENTS=$a
	if [ "$CLIENTS" = all ]; then CLIENTS=$ALL_CLIENTS; fi
	return 0
}

ask_setup() { # questions shared by install and configure
	local defscope=global
	if git rev-parse --show-toplevel >/dev/null 2>&1; then defscope=local; fi
	choose SCOPE "Scope" "${SCOPE:-$defscope}" \
		"global|Global: user-level client configuration" \
		"local|Local: one project (files in the project directory)"
	if [ "$SCOPE" = local ]; then
		ask PROJECT "Project directory" "${PROJECT:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"
		if [ "$METHOD" != docker ]; then
			if yesno "Keep this project's database inside the project (.acd-ctx/)?" "$([ "$LOCAL_STORAGE" = 1 ] && echo y || echo n)"; then LOCAL_STORAGE=1; else LOCAL_STORAGE=0; fi
		fi
	fi
	local definteg=both
	if [ "$METHOD" = docker ]; then definteg=proxy; fi
	choose INTEGRATION "How should the clients use acd-ctx?" "${INTEGRATION:-$definteg}" \
		"both|Proxy and MCP" \
		"proxy|Proxy only (the clients send their API calls through acd-ctx)" \
		"mcp|MCP only (recall tools: retrieve, search_context, decompress, ctx_status)" \
		"none|Neither (install only)"
	if [ "$INTEGRATION" != none ]; then ask_clients; fi
	if uses_proxy; then
		choose GATEWAY "Where does acd-ctx send the traffic?" "${GATEWAY:-direct}" \
			"direct|Directly to the providers (Anthropic, OpenAI, Google)" \
			"omniroute|OmniRoute gateway" \
			"freellmapi|freellmapi gateway" \
			"bifrost|Bifrost gateway"
		if [ "$GATEWAY" = direct ]; then GATEWAY=""; GATEWAY_URL=""; else ask GATEWAY_URL "Gateway URL" "${GATEWAY_URL:-$(gateway_default_url "$GATEWAY")}"; fi
		ask HOST "Proxy listen IP" "${HOST:-127.0.0.1}"
		ask PORT "Proxy port" "${PORT:-8787}"
		if [ "$METHOD" != docker ]; then
			if yesno "Run the proxy as a user service ($([ "$OS" = darwin ] && echo launchd || echo systemd))?" y; then SERVICE=1; else SERVICE=0; fi
		fi
	fi
}

menu() {
	[ -n "$TTY" ] || { usage; exit 1; }
	local action
	choose action "acd-ctx installer — what do you want to do?" install \
		"install|Install" \
		"update|Update the application to a new version" \
		"configure|Update the configuration" \
		"service|Manage the service (start, stop, restart, status)" \
		"status|Show status" \
		"uninstall|Uninstall"
	case "$action" in
	install)
		choose METHOD "Install method" "${METHOD:-$(default_method)}" \
			"go|go install (needs Go)" \
			"release|Download the release binary from GitHub" \
			"docker|Docker image (ghcr.io)" \
			"custom|Custom: build from a checkout or copy a binary"
		if [ "$METHOD" = custom ]; then
			local src
			ask src "Checkout directory to build, or path of a binary" "$(pwd)"
			if [ -d "$src" ]; then FROM_SOURCE=$src; else FROM_BINARY=$src; fi
			ask BIN_DIR "Install directory" "${BIN_DIR:-$HOME/.local/bin}"
		fi
		ask VERSION "Version" "latest"
		ask_setup
		cmd_install
		;;
	update)
		ask VERSION "Version" "latest"
		if yesno "Stop running acd-ctx MCP servers so the clients load the new binary?" y; then RESTART_MCP=1; else RESTART_MCP=0; fi
		cmd_update
		;;
	configure)
		ask_setup
		cmd_configure
		;;
	service)
		choose SUB "Service" status "status|Status" "start|Start" "stop|Stop" "restart|Restart" "install|Install or rewrite" "remove|Remove"
		cmd_service
		;;
	status) cmd_status ;;
	uninstall)
		if yesno "Also remove the binary, the Docker volume and the saved settings?" n; then PURGE=1; fi
		cmd_uninstall
		;;
	esac
}

# ---------------------------------------------------------------------------
# Main

SUB=""
parse_args() {
	while [ $# -gt 0 ]; do
		case "$1" in
		install|update|configure|status|uninstall|menu) CMD=$1 ;;
		service) CMD=service; if [ $# -gt 1 ]; then case "$2" in -*) ;; *) SUB=$2; shift ;; esac; fi ;;
		--method) METHOD=$2; shift ;;
		--version) VERSION=$2; shift ;;
		--bin-dir) BIN_DIR=$2; shift ;;
		--from-source) FROM_SOURCE=$2; METHOD=${METHOD:-custom}; shift ;;
		--binary) FROM_BINARY=$2; METHOD=${METHOD:-custom}; shift ;;
		--scope) SCOPE=$2; shift ;;
		--project) PROJECT=$2; shift ;;
		--local-storage) LOCAL_STORAGE=1 ;;
		--integration) INTEGRATION=$2; shift ;;
		--clients) CLIENTS=$2; shift ;;
		--gateway) GATEWAY=$2; shift ;;
		--gateway-url) GATEWAY_URL=$2; shift ;;
		--host) HOST=$2; shift ;;
		--port) PORT=$2; shift ;;
		--service) SERVICE=1 ;;
		--no-service) SERVICE=0 ;;
		--restart-mcp) RESTART_MCP=1 ;;
		--no-restart-mcp) RESTART_MCP=0 ;;
		--dry-run) DRY_RUN=1 ;;
		--yes|-y) ASSUME_YES=1 ;;
		--purge) PURGE=1 ;;
		-h|--help) usage; exit 0 ;;
		*) die "unknown argument: $1 (see --help)" ;;
		esac
		shift
	done
}

main() {
	local nargs=$#
	parse_args "$@"
	detect_platform
	if [ -z "$CMD" ]; then
		if [ "$nargs" = 0 ] && [ -n "$TTY" ]; then CMD=menu; else CMD=install; fi
	fi
	# An explicit install starts from the defaults and the flags; the other
	# commands (and the menu, as question defaults) use the saved settings.
	if [ "$CMD" = install ]; then
		if [ -f "$STATE_FILE" ]; then HAVE_STATE=1; fi
	else
		load_state
	fi
	if [ "$DRY_RUN" = 1 ]; then info "dry run: nothing is changed"; fi
	case "$CMD" in
	menu) menu ;;
	install) cmd_install ;;
	update) cmd_update ;;
	configure) cmd_configure ;;
	service) cmd_service ;;
	status) cmd_status ;;
	uninstall) cmd_uninstall ;;
	esac
}

main "$@"

<#
.SYNOPSIS
  acd-ctx installer for Windows.

.DESCRIPTION
  Installs the acd-ctx binary (release download, go install, a custom build or
  copy, or the Docker image), registers it with the coding clients (Claude
  Code, Codex, Gemini CLI, OpenCode) as an MCP server, as a proxy or both,
  globally or for one project, and runs the proxy at logon as a Scheduled Task
  of the current user (no administrator rights needed).

  Run with no arguments for a menu. Settings are saved in
  %APPDATA%\acd-ctx\install.json so that update and configure reuse them.
  Every client file is backed up before it is changed.

  Works with Windows PowerShell 5.1 and PowerShell 7.

.EXAMPLE
  .\install.ps1
  .\install.ps1 install -Method go -Integration both -Clients claude -Yes
  .\install.ps1 install -Scope local -Project C:\src\app -LocalStorage -Integration both -Clients claude,gemini -Yes
  .\install.ps1 install -Method docker -Integration proxy -Port 8787 -Yes
  .\install.ps1 install -Integration proxy -Clients claude,codex -Gateway bifrost -GatewayUrl http://127.0.0.1:8080 -Yes
  .\install.ps1 update -Version 1.0.5
  .\install.ps1 configure -Port 8788 -Clients all
  .\install.ps1 service restart
#>
[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [ValidateSet('install', 'update', 'configure', 'service', 'status', 'uninstall', 'menu', 'help')]
    [string]$Command,
    [Parameter(Position = 1)]
    [ValidateSet('', 'start', 'stop', 'restart', 'status', 'install', 'remove')]
    [string]$Action = '',

    [ValidateSet('', 'release', 'go', 'docker', 'custom')]
    [string]$Method = '',
    [string]$Version = '',
    [string]$BinDir = '',
    [string]$FromSource = '',
    [string]$Binary = '',

    [ValidateSet('', 'global', 'local')]
    [string]$Scope = '',
    [string]$Project = '',
    [switch]$LocalStorage,
    [ValidateSet('', 'proxy', 'mcp', 'both', 'none')]
    [string]$Integration = '',
    [string[]]$Clients = @(),
    [ValidateSet('', 'none', 'omniroute', 'freellmapi', 'bifrost')]
    [string]$Gateway = '',
    [string]$GatewayUrl = '',
    [string]$ListenHost = '',
    [int]$Port = 0,
    [switch]$Service,
    [switch]$NoService,

    [switch]$NoRestartMcp,
    [switch]$DryRun,
    [switch]$Yes,
    [switch]$Purge
)

Set-StrictMode -Version 2
$ErrorActionPreference = 'Stop'

$Repo = 'fsvxavier/accordion-context'
$Module = "github.com/$Repo"
$Image = "ghcr.io/$Repo"
$Container = 'acd-ctx'
$Volume = 'acd-ctx-data'
$TaskName = 'acd-ctx'
$Anthropic = 'https://api.anthropic.com'
$OpenAI = 'https://api.openai.com'
$Google = 'https://generativelanguage.googleapis.com'
$AllClients = @('claude', 'codex', 'gemini', 'opencode')
$ConfigDir = Join-Path $env:APPDATA 'acd-ctx'
$StateFile = Join-Path $ConfigDir 'install.json'
$Stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$Utf8 = New-Object System.Text.UTF8Encoding $false

# The effective settings. Flags fill it, then the saved state (except for an
# explicit install), then the defaults.
$S = [ordered]@{
    Method = $Method; Version = $Version; BinDir = $BinDir; FromSource = $FromSource; Binary = $Binary
    Scope = $Scope; Project = $Project; LocalStorage = $null; Integration = $Integration
    Clients = @(); ListenHost = $ListenHost; Port = $Port; Service = $null; Bin = ''
    Gateway = $Gateway; GatewayUrl = $GatewayUrl
}
if ($LocalStorage) { $S.LocalStorage = $true }
if ($Service) { $S.Service = $true }
if ($NoService) { $S.Service = $false }
if ($Clients.Count -gt 0) { $S.Clients = @($Clients | ForEach-Object { $_ -split ',' } | Where-Object { $_ } | ForEach-Object { $_.Trim().ToLower() }) }
if ($FromSource -or $Binary) { if (-not $S.Method) { $S.Method = 'custom' } }
$Saved = $null

# ---------------------------------------------------------------------------
# Output and prompts

function Info([string]$m) { Write-Host "==> $m" -ForegroundColor Green }
function Warn([string]$m) { Write-Host "warning: $m" -ForegroundColor Yellow }
function Die([string]$m) { Write-Host "error: $m" -ForegroundColor Red; exit 1 }
function Show([string]$m) { Write-Host "  + $m" }

function Have([string]$name) { [bool](Get-Command $name -ErrorAction SilentlyContinue) }

function Ask([string]$q, [string]$def) {
    $a = Read-Host ($(if ($def) { "$q [$def]" } else { $q }))
    if ([string]::IsNullOrWhiteSpace($a)) { return $def }
    return $a.Trim()
}

function Choose([string]$q, [string]$def, [string[]]$opts) {
    Write-Host ''
    Write-Host $q -ForegroundColor White
    $d = 1
    for ($i = 0; $i -lt $opts.Count; $i++) {
        $v, $l = $opts[$i] -split '\|', 2
        if ($v -eq $def) { $d = $i + 1 }
        Write-Host ("  {0}) {1}" -f ($i + 1), $l)
    }
    while ($true) {
        $a = Read-Host "Choice [$d]"
        if (-not $a) { $a = "$d" }
        $n = 0
        if ([int]::TryParse($a, [ref]$n) -and $n -ge 1 -and $n -le $opts.Count) { return ($opts[$n - 1] -split '\|', 2)[0] }
        Write-Host 'Pick a number from the list.'
    }
}

function YesNo([string]$q, [bool]$def) {
    if ($Yes) { return $true }
    if (-not [Environment]::UserInteractive) { return $def }
    $a = Ask "$q (y/n)" $(if ($def) { 'y' } else { 'n' })
    return ($a -match '^(y|yes|s|sim)$')
}

# Run prints a command and runs it, or only prints it with -DryRun.
function Run([string]$exe, [string[]]$argv) {
    Show ("$exe " + ($argv -join ' '))
    if ($DryRun) { return }
    # Windows PowerShell 5.1 turns any stderr line of a native program into a
    # terminating error under ErrorActionPreference Stop (go install prints
    # "go: downloading" there). Judge native commands by their exit code only.
    $old = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try { & $exe @argv 2>&1 | ForEach-Object { Write-Host "    $_" } } finally { $ErrorActionPreference = $old }
    if ($LASTEXITCODE -ne 0) { throw "$exe exited with $LASTEXITCODE" }
}

# Quiet runs a read-only native command, ignoring its stderr, and returns its stdout.
function Quiet([scriptblock]$b) {
    $old = $ErrorActionPreference
    $ErrorActionPreference = 'SilentlyContinue'
    try { & $b 2>$null } catch { } finally { $ErrorActionPreference = $old }
}

function Container-Exists {
    if (-not (Have docker)) { return $false }
    Quiet { docker container inspect $Container } | Out-Null
    return ($LASTEXITCODE -eq 0)
}

# ---------------------------------------------------------------------------
# Saved settings

function Load-State {
    if (-not (Test-Path $StateFile)) { return }
    $script:Saved = Get-Content -Raw $StateFile | ConvertFrom-Json
    $sv = $script:Saved
    # Paths belong to the saved method: drop them when another method is asked for.
    $sameMethod = (-not $S.Method) -or ($S.Method -eq $sv.Method)
    if ($sameMethod) {
        $S.Method = $sv.Method
        if (-not $S.BinDir) { $S.BinDir = $sv.BinDir }
        if (-not $S.FromSource -and -not $S.Binary) { $S.FromSource = $sv.FromSource }
        $S.Bin = $sv.Bin
    }
    if (-not $S.Version) { $S.Version = '' }
    if (-not $S.Scope) { $S.Scope = $sv.Scope }
    if (-not $S.Project) { $S.Project = $sv.Project }
    if ($null -eq $S.LocalStorage) { $S.LocalStorage = [bool]$sv.LocalStorage }
    if (-not $S.Integration) { $S.Integration = $sv.Integration }
    if ($S.Clients.Count -eq 0) { $S.Clients = @($sv.Clients) }
    if (-not $S.Gateway -and $sv.PSObject.Properties.Name -contains 'Gateway') { $S.Gateway = $sv.Gateway }
    if (-not $S.GatewayUrl -and $sv.PSObject.Properties.Name -contains 'GatewayUrl') { $S.GatewayUrl = $sv.GatewayUrl }
    if (-not $S.ListenHost) { $S.ListenHost = $sv.ListenHost }
    if (-not $S.Port) { $S.Port = [int]$sv.Port }
    if ($null -eq $S.Service) { $S.Service = [bool]$sv.Service }
}

function Save-State {
    if ($DryRun) { Info "would save settings to $StateFile"; return }
    New-Item -ItemType Directory -Force -Path $ConfigDir | Out-Null
    $o = [ordered]@{
        Method = $S.Method; Version = $S.Version; BinDir = $S.BinDir; FromSource = $S.FromSource; Bin = $S.Bin
        Scope = $S.Scope; Project = $S.Project; LocalStorage = [bool]$S.LocalStorage; Integration = $S.Integration
        Clients = @($S.Clients); ListenHost = $S.ListenHost; Port = $S.Port; Service = [bool]$S.Service
        Gateway = $S.Gateway; GatewayUrl = $S.GatewayUrl
    }
    [IO.File]::WriteAllText($StateFile, (ConvertTo-Json -InputObject $o -Depth 5), $Utf8)
    Info "settings saved to $StateFile"
}

function Default-Method {
    if (Have go) { 'go' } elseif (Have docker) { 'docker' } else { 'release' }
}

function Uses-Proxy { $S.Integration -in @('proxy', 'both') }
function Uses-Mcp { $S.Integration -in @('mcp', 'both') }
function Has-Client([string]$c) { $S.Clients -contains $c }
function Is-Loopback([string]$h) { $h -match '^(127\.|localhost$|::1$|\[::1\]$)' }

function Set-Defaults {
    if (-not $S.Method) { $S.Method = Default-Method }
    # Inside a git repository the default is that project: acd-ctx mcp finds its
    # project from the working directory, and only project-scoped proxy settings
    # tell the proxy which project a request belongs to.
    if (-not $S.Scope) {
        $top = ''
        if (Have git) { $top = Quiet { git rev-parse --show-toplevel } }
        if ($top) { $S.Scope = 'local'; if (-not $S.Project) { $S.Project = $top } } else { $S.Scope = 'global' }
    }
    # The containerized MCP server runs in /data and cannot see the project.
    if (-not $S.Integration) { $S.Integration = $(if ($S.Method -eq 'docker') { 'proxy' } else { 'both' }) }
    if ($S.Clients.Count -eq 0) { $S.Clients = @('claude') }
    if ($S.Clients -contains 'all') { $S.Clients = $AllClients }
    if (-not $S.ListenHost) { $S.ListenHost = '127.0.0.1' }
    if (-not $S.Port) { $S.Port = 8787 }
    if ($S.Gateway -eq 'none') { $S.Gateway = ''; $S.GatewayUrl = '' }
    if ($S.Gateway -and -not $S.GatewayUrl) { $S.GatewayUrl = Gateway-Default-Url $S.Gateway }
    if ($S.GatewayUrl) { $S.GatewayUrl = $S.GatewayUrl.TrimEnd('/') }
    if ($null -eq $S.LocalStorage) { $S.LocalStorage = $false }
    if ($null -eq $S.Service) { $S.Service = [bool](Uses-Proxy) }
    if ($S.Scope -eq 'local') {
        if (-not $S.Project) { $S.Project = (Get-Location).Path }
        $S.Project = (Resolve-Path $S.Project -ErrorAction SilentlyContinue).Path
        if (-not $S.Project) { Die 'project directory not found' }
    } else {
        $S.LocalStorage = $false
    }
}

function Validate {
    foreach ($c in $S.Clients) { if ($c -notin $AllClients) { Die "unknown client: $c (use claude, codex, gemini, opencode)" } }
    if ($S.Method -eq 'docker' -and $S.LocalStorage) { Die '-LocalStorage does not work with -Method docker: the container cannot see the project directory' }
    if ($S.Method -eq 'custom' -and -not $S.FromSource -and -not $S.Binary) { Die '-Method custom needs -FromSource DIR or -Binary FILE' }
}

# ---------------------------------------------------------------------------
# Versions and install methods

function Get-Arch {
    switch ($env:PROCESSOR_ARCHITECTURE) {
        'AMD64' { 'amd64' }
        'ARM64' { 'arm64' }
        default { Die "unsupported architecture $env:PROCESSOR_ARCHITECTURE" }
    }
}

function Latest-Version {
    $v = ''
    if (Have go) {
        Push-Location $env:TEMP
        try { $v = Quiet { go list -m -f '{{.Version}}' "$Module@latest" } } finally { Pop-Location }
    }
    if (-not $v) {
        try { $v = (Invoke-RestMethod -UseBasicParsing "https://api.github.com/repos/$Repo/releases/latest").tag_name } catch { $v = '' }
    }
    if (-not $v) { Die 'could not find the latest version; pass -Version' }
    return ([string]$v).TrimStart('v')
}

function Resolve-Version {
    if (-not $S.Version -or $S.Version -eq 'latest') {
        if ($S.Method -eq 'custom' -and $S.FromSource) {
            Push-Location $S.FromSource
            try { $S.Version = Quiet { git describe --tags --always } } finally { Pop-Location }
            if (-not $S.Version) { $S.Version = 'dev' }
        } else {
            $S.Version = Latest-Version
        }
    }
    $S.Version = ([string]$S.Version).TrimStart('v')
}

# Stop-Acd stops running acd-ctx processes (serve and mcp): Windows cannot
# replace an .exe that is running.
function Stop-Acd([string]$kind) {
    $procs = @(Get-CimInstance Win32_Process -Filter "Name='acd-ctx.exe'" -ErrorAction SilentlyContinue |
        Where-Object { $_.CommandLine -match "\s$kind(\s|$)" })
    foreach ($p in $procs) {
        Show "stop acd-ctx $kind (pid $($p.ProcessId))"
        if (-not $DryRun) { Stop-Process -Id $p.ProcessId -Force -ErrorAction SilentlyContinue }
    }
    return $procs.Count
}

function Place-Binary([string]$src) {
    $dst = Join-Path $S.BinDir 'acd-ctx.exe'
    Show "copy $src -> $dst"
    $S.Bin = $dst
    if ($DryRun) { return }
    New-Item -ItemType Directory -Force -Path $S.BinDir | Out-Null
    if (Test-Path $dst) {
        Stop-Acd 'serve' | Out-Null
        Stop-Acd 'mcp' | Out-Null
        Start-Sleep -Milliseconds 500
        Move-Item -Force $dst "$dst.old"
    }
    Copy-Item $src $dst
    Remove-Item -Force "$dst.old" -ErrorAction SilentlyContinue
}

function Add-ToUserPath([string]$dir) {
    $p = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (($p -split ';') -contains $dir) { return }
    Show "add $dir to the user PATH"
    if ($DryRun) { return }
    [Environment]::SetEnvironmentVariable('Path', (($p.TrimEnd(';') + ";$dir").TrimStart(';')), 'User')
    $env:Path = "$env:Path;$dir"
    Info "open a new terminal to pick up the PATH change"
}

function Install-Release {
    if (-not $S.BinDir) { $S.BinDir = Join-Path $env:LOCALAPPDATA 'Programs\acd-ctx' }
    $arch = Get-Arch
    $name = "acd-ctx_$($S.Version)_windows_$arch.zip"
    $base = "https://github.com/$Repo/releases/download/v$($S.Version)"
    Info "downloading $name"
    if ($DryRun) { Show "download $base/$name and checksums.txt"; Place-Binary '<extracted acd-ctx.exe>'; Add-ToUserPath $S.BinDir; return }
    $tmp = Join-Path $env:TEMP "acd-ctx-$Stamp"
    New-Item -ItemType Directory -Force -Path $tmp | Out-Null
    try {
        try {
            Invoke-WebRequest -UseBasicParsing "$base/$name" -OutFile (Join-Path $tmp $name)
            Invoke-WebRequest -UseBasicParsing "$base/checksums.txt" -OutFile (Join-Path $tmp 'checksums.txt')
        } catch {
            Die "release v$($S.Version) has no binary for windows/$arch (or it could not be downloaded). Use -Method go or -Method docker."
        }
        $want = (Get-Content (Join-Path $tmp 'checksums.txt') | Where-Object { $_ -match "\s\*?$([regex]::Escape($name))$" } | ForEach-Object { ($_ -split '\s+')[0] }) | Select-Object -First 1
        $got = (Get-FileHash -Algorithm SHA256 (Join-Path $tmp $name)).Hash.ToLower()
        if (-not $want -or $want.ToLower() -ne $got) { Die "checksum mismatch for $name" }
        Expand-Archive -Force (Join-Path $tmp $name) $tmp
        Place-Binary (Join-Path $tmp 'acd-ctx.exe')
    } finally {
        Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
    }
    Add-ToUserPath $S.BinDir
}

function Install-Go {
    if (-not (Have go)) { Die 'go is not installed; use -Method release or -Method docker' }
    if (-not $S.BinDir) {
        $gobin = (& go env GOBIN)
        if (-not $gobin) { $gobin = Join-Path ((& go env GOPATH) -split ';')[0] 'bin' }
        $S.BinDir = $gobin
    }
    # Build into a temp dir, then place it: a running acd-ctx.exe cannot be overwritten.
    $tmp = Join-Path $env:TEMP "acd-ctx-$Stamp"
    Info "go install $Module/cmd/acd-ctx@v$($S.Version)"
    $old = $env:GOBIN; $oldFlags = $env:GOFLAGS
    try {
        $env:GOBIN = $tmp; $env:GOFLAGS = ''
        Push-Location $env:TEMP
        # Without -X main.version the binary reports "dev". Same flags as .goreleaser.yaml.
        Run 'go' @('install', '-trimpath', '-ldflags', "-s -w -X main.version=$($S.Version)", "$Module/cmd/acd-ctx@v$($S.Version)")
    } finally {
        Pop-Location
        $env:GOBIN = $old; $env:GOFLAGS = $oldFlags
    }
    Place-Binary (Join-Path $tmp 'acd-ctx.exe')
    if (-not $DryRun) { Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue }
}

function Install-Custom {
    if (-not $S.BinDir) { $S.BinDir = Join-Path $env:LOCALAPPDATA 'Programs\acd-ctx' }
    if ($S.Binary) {
        if (-not (Test-Path $S.Binary)) { Die "binary not found: $($S.Binary)" }
        Place-Binary (Resolve-Path $S.Binary).Path
    } else {
        if (-not $S.FromSource) { Die '-Method custom needs -FromSource DIR or -Binary FILE' }
        if (-not (Have go)) { Die 'go is required to build from source' }
        $src = (Resolve-Path $S.FromSource).Path
        if (-not (Test-Path (Join-Path $src 'cmd\acd-ctx\main.go'))) { Die "not an accordion-context checkout: $src" }
        $S.FromSource = $src
        $tmp = Join-Path $env:TEMP "acd-ctx-$Stamp"
        Info "building $src (version $($S.Version))"
        Push-Location $src
        $oldCgo = $env:CGO_ENABLED
        try {
            $env:CGO_ENABLED = '0'
            Run 'go' @('build', '-trimpath', '-ldflags', "-s -w -X main.version=$($S.Version)", '-o', (Join-Path $tmp 'acd-ctx.exe'), './cmd/acd-ctx')
        } finally { Pop-Location; $env:CGO_ENABLED = $oldCgo }
        Place-Binary (Join-Path $tmp 'acd-ctx.exe')
        if (-not $DryRun) { Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue }
    }
    Add-ToUserPath $S.BinDir
}

# Gateways (docs/integrations.md): the client talks to acd-ctx, and acd-ctx
# forwards to the gateway's surface for that wire format instead of the
# provider. Ports are the gateways' documented defaults.
function Gateway-Default-Url([string]$g) {
    # From inside the container, the host's loopback is host.docker.internal.
    $h = $(if ($S.Method -eq 'docker') { 'host.docker.internal' } else { '127.0.0.1' })
    switch ($g) {
        'omniroute' { "http://${h}:20128" }
        'freellmapi' { "http://${h}:3001" }
        'bifrost' { "http://${h}:8080" }
    }
}

# Gateway-Upstream KIND: where acd-ctx forwards one client's traffic. KIND is
# anthropic (Claude Code), openai (Codex), google (Gemini CLI) or opencode
# (OpenCode's Anthropic provider, with /v1). Empty when the gateway has no
# documented surface for KIND.
function Gateway-Upstream([string]$kind) {
    $g = $S.GatewayUrl
    switch ("$($S.Gateway):$kind") {
        ':anthropic' { return $Anthropic }
        ':openai' { return "$OpenAI/v1" }
        ':google' { return $Google }
        ':opencode' { return "$Anthropic/v1" }
        'omniroute:anthropic' { return $g }
        'freellmapi:anthropic' { return $g }
        'omniroute:openai' { return "$g/v1" }
        'freellmapi:openai' { return "$g/v1" }
        'omniroute:opencode' { return "$g/v1" }
        'freellmapi:opencode' { return "$g/v1" }
        'freellmapi:google' { return $g }
        'bifrost:anthropic' { return "$g/anthropic" }
        'bifrost:openai' { return "$g/openai" }
        'bifrost:google' { return "$g/genai" }
        'bifrost:opencode' { return "$g/anthropic/v1" }
    }
    return ''
}

function Origin-Of([string]$u) { if ($u -match '^(https?://[^/]+)') { $Matches[1] } else { $u } }

function Proxy-Allow {
    $a = @($Anthropic)
    if ($S.Gateway) { $a += (Origin-Of $S.GatewayUrl) }
    else {
        if (Has-Client 'codex') { $a += $OpenAI }
        if (Has-Client 'gemini') { $a += $Google }
    }
    return ($a -join ',')
}

function Allowed-Hosts([string]$h) {
    if (-not $h -or $h -in @('0.0.0.0', '::', '[::]')) { return 'localhost,127.0.0.1,[::1]' }
    return "localhost,127.0.0.1,[::1],$h"
}

function Install-Docker {
    if (-not (Have docker)) { Die 'docker is not installed' }
    $S.Bin = ''
    Info "docker image ${Image}:$($S.Version)"
    Run 'docker' @('pull', "${Image}:$($S.Version)")
    Run 'docker' @('volume', 'create', $Volume)
    Docker-Recreate
}

function Docker-Recreate {
    $hosts = 'localhost,127.0.0.1,[::1],host.docker.internal'
    if (-not (Is-Loopback $S.ListenHost)) {
        Warn "publishing the proxy on $($S.ListenHost): anyone who can reach it can use it (the Host allowlist is not authentication)"
        $hosts = (Allowed-Hosts $S.ListenHost) + ',host.docker.internal'
    }
    if ($DryRun -or (Container-Exists)) { Run 'docker' @('rm', '-f', $Container) }
    Run 'docker' @('run', '-d', '--name', $Container, '--restart', 'unless-stopped',
        '-p', "$($S.ListenHost):$($S.Port):8787", '-v', "${Volume}:/data",
        '--add-host=host.docker.internal:host-gateway',
        '--read-only', '--tmpfs', '/tmp', '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges:true',
        '-e', "ACD_CTX_ALLOW=$(Proxy-Allow)", '-e', "ACD_CTX_ALLOWED_HOSTS=$hosts",
        "${Image}:$($S.Version)")
}

function Install-Binary {
    switch ($S.Method) {
        'release' { Install-Release }
        'go' { Install-Go }
        'custom' { Install-Custom }
        'docker' { Install-Docker }
    }
}

# ---------------------------------------------------------------------------
# Service: a Scheduled Task of the current user that starts at logon.

function Serve-Args {
    $a = @('serve', '--listen', "$($S.ListenHost):$($S.Port)", '--allow', (Proxy-Allow))
    if (-not (Is-Loopback $S.ListenHost)) { $a += @('--allow-non-loopback', '--allowed-hosts', (Allowed-Hosts $S.ListenHost)) }
    return $a
}

function Service-Install {
    if ($S.Method -eq 'docker') { Info 'docker: the container restarts on its own (--restart unless-stopped)'; return }
    if (-not $S.Bin) { Die 'no binary to run as a service' }
    if (-not (Is-Loopback $S.ListenHost)) { Warn "listening on $($S.ListenHost): any peer that can reach the port gets the proxy (the Host allowlist is not authentication)" }
    $log = Join-Path $ConfigDir 'serve.log'
    # A hidden PowerShell runs the proxy so that no console window stays open.
    # Quote every argument: in PowerShell argument mode a bare comma builds an
    # array, which would split the --allow list.
    $quoted = (@(Serve-Args) | ForEach-Object { "'" + ($_ -replace "'", "''") + "'" }) -join ' '
    $inner = "& '$($S.Bin)' $quoted *>> '$log'"
    $argLine = "-NoProfile -NonInteractive -WindowStyle Hidden -Command `"$inner`""
    Show "Register-ScheduledTask $TaskName (at logon): powershell.exe $argLine"
    if ($DryRun) { return }
    New-Item -ItemType Directory -Force -Path $ConfigDir | Out-Null
    Service-Stop
    $action = New-ScheduledTaskAction -Execute 'powershell.exe' -Argument $argLine
    $trigger = New-ScheduledTaskTrigger -AtLogOn -User "$env:USERDOMAIN\$env:USERNAME"
    # ExecutionTimeLimit zero: the default 72 hours would stop the proxy.
    $settings = New-ScheduledTaskSettingsSet -ExecutionTimeLimit ([TimeSpan]::Zero) -AllowStartIfOnBatteries `
        -DontStopIfGoingOnBatteries -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1) -MultipleInstances IgnoreNew
    $principal = New-ScheduledTaskPrincipal -UserId "$env:USERDOMAIN\$env:USERNAME" -LogonType Interactive -RunLevel Limited
    Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger -Settings $settings -Principal $principal `
        -Description 'acd-ctx proxy (context compaction for coding agents)' -Force | Out-Null
    Start-ScheduledTask -TaskName $TaskName
    Info "scheduled task $TaskName registered and started; log: $log"
}

function Service-Stop {
    if ($DryRun) { Show "Stop-ScheduledTask $TaskName"; Stop-Acd 'serve' | Out-Null; return }
    if (Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue) { Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue }
    Stop-Acd 'serve' | Out-Null
}

function Service-Remove {
    if ($S.Method -eq 'docker') {
        if ($DryRun -or (Container-Exists)) { Run 'docker' @('rm', '-f', $Container) }
        return
    }
    if (-not (Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue) -and -not $DryRun) { return }
    Service-Stop
    Show "Unregister-ScheduledTask $TaskName"
    if (-not $DryRun) { Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false }
}

function Service-Ctl([string]$what) {
    if ($S.Method -eq 'docker') {
        if ($what -eq 'status') { & docker ps -a --filter "name=^/$Container$" --format '{{.Names}}  {{.Status}}  {{.Image}}'; return }
        Run 'docker' @($what, $Container); return
    }
    switch ($what) {
        'start' { Show "Start-ScheduledTask $TaskName"; if (-not $DryRun) { Start-ScheduledTask -TaskName $TaskName } }
        'stop' { Service-Stop }
        'restart' { Service-Stop; Show "Start-ScheduledTask $TaskName"; if (-not $DryRun) { Start-ScheduledTask -TaskName $TaskName } }
        'status' {
            $t = Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
            if (-not $t) { Write-Host 'service      not installed'; return }
            Write-Host "service      scheduled task $TaskName, state $($t.State)"
            $p = @(Get-CimInstance Win32_Process -Filter "Name='acd-ctx.exe'" | Where-Object { $_.CommandLine -match '\sserve(\s|$)' })
            if ($p.Count) { Write-Host "process      pid $($p[0].ProcessId)" }
        }
    }
}

# ---------------------------------------------------------------------------
# File editing: backups first, and -DryRun only prints.

function Backup-File([string]$f) {
    if (-not (Test-Path $f)) { return }
    $b = "$f.bak-acd-ctx-$Stamp"
    if ($DryRun) { Show "backup $f"; return }
    # The first backup of a run is the original; keep it.
    if (-not (Test-Path $b)) { Copy-Item $f $b }
}

function Write-TextFile([string]$f, [string]$text) {
    if ((Test-Path $f) -and ((Get-Content -Raw $f) -eq $text)) { return }
    if ($DryRun) { Show "write $f"; $text -split "`n" | ForEach-Object { Write-Host "      | $_" }; return }
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $f) | Out-Null
    Backup-File $f
    [IO.File]::WriteAllText($f, $text, $Utf8)
    Info "wrote $f"
}

# ConvertFrom-Json in PowerShell 5.1 has no -AsHashtable; convert by hand.
function To-Hash($o) {
    if ($null -eq $o) { return $null }
    if ($o -is [System.Management.Automation.PSCustomObject]) {
        $h = [ordered]@{}
        foreach ($p in $o.PSObject.Properties) { $h[$p.Name] = To-Hash $p.Value }
        return $h
    }
    if ($o -is [System.Collections.IList] -and $o -isnot [string]) { return , @($o | ForEach-Object { To-Hash $_ }) }
    return $o
}

# Format-Json indents compact JSON with two spaces (Windows PowerShell 5.1
# ConvertTo-Json output is valid but oddly aligned).
function Format-Json([string]$json) {
    $sb = New-Object System.Text.StringBuilder
    $level = 0; $inStr = $false; $esc = $false
    foreach ($ch in $json.ToCharArray()) {
        if ($inStr) {
            [void]$sb.Append($ch)
            if ($esc) { $esc = $false } elseif ($ch -eq [char]92) { $esc = $true } elseif ($ch -eq [char]34) { $inStr = $false }
            continue
        }
        switch -CaseSensitive ($ch) {
            '"' { $inStr = $true; [void]$sb.Append($ch) }
            { $_ -eq [char]123 -or $_ -eq [char]91 } { $level++; [void]$sb.Append($ch).Append("`n").Append('  ' * $level) }
            { $_ -eq [char]125 -or $_ -eq [char]93 } { $level--; [void]$sb.Append("`n").Append('  ' * $level).Append($ch) }
            ',' { [void]$sb.Append($ch).Append("`n").Append('  ' * $level) }
            ':' { [void]$sb.Append(': ') }
            default { if (-not [char]::IsWhiteSpace($ch)) { [void]$sb.Append($ch) } }
        }
    }
    # Empty objects and arrays on one line.
    return ([regex]::Replace($sb.ToString(), '([\[{])\s+([\]}])', '$1$2'))
}

# Edit-Json FILE OPS - each op is @('set'|'del'|'append'|'remove', 'a/b/c', value).
function Edit-Json([string]$f, [object[]]$ops) {
    $data = [ordered]@{}
    if ((Test-Path $f) -and (Get-Item $f).Length -gt 0) {
        try { $data = To-Hash (Get-Content -Raw $f | ConvertFrom-Json) } catch { Warn "$f is not plain JSON; edit it by hand"; return }
        if ($data -isnot [System.Collections.IDictionary]) { Warn "$f is not a JSON object; edit it by hand"; return }
    }
    $before = ConvertTo-Json -InputObject $data -Depth 20 -Compress
    foreach ($op in $ops) {
        $kind, $path, $val = $op
        $keys = $path -split '/'
        $cur = $data
        $ok = $true
        for ($i = 0; $i -lt $keys.Count - 1; $i++) {
            $k = $keys[$i]
            if (-not $cur.Contains($k)) {
                if ($kind -in @('del', 'remove')) { $ok = $false; break }
                $cur[$k] = [ordered]@{}
            }
            $cur = $cur[$k]
            if ($cur -isnot [System.Collections.IDictionary]) { $ok = $false; break }
        }
        if (-not $ok) { continue }
        $last = $keys[-1]
        switch ($kind) {
            'set' { $cur[$last] = $val }
            'del' { if ($cur.Contains($last)) { $cur.Remove($last) } }
            'append' {
                $list = @(); if ($cur.Contains($last)) { $list = @($cur[$last]) }
                if ($list -notcontains $val) { $list += $val }
                $cur[$last] = $list
            }
            'remove' { if ($cur.Contains($last)) { $cur[$last] = @(@($cur[$last]) | Where-Object { $_ -ne $val }) } }
        }
    }
    $after = ConvertTo-Json -InputObject $data -Depth 20 -Compress
    if ($after -eq $before) { return }
    if ($DryRun) { Show "edit ${f}: $(($ops | ForEach-Object { $_[0] + ' ' + $_[1] }) -join ', ')"; return }
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $f) | Out-Null
    Backup-File $f
    [IO.File]::WriteAllText($f, (Format-Json (ConvertTo-Json -InputObject $data -Depth 20 -Compress)) + "`n", $Utf8)
    Info "updated $f"
}

function Block-Markers([string]$name) {
    return @("# >>> acd-ctx:$name (managed by install.ps1) >>>", "# <<< acd-ctx:$name <<<")
}

function Outside-Lines([string]$f, [string]$name) {
    if (-not (Test-Path $f)) { return @() }
    $b, $e = Block-Markers $name
    $skip = $false
    $out = @()
    foreach ($l in (Get-Content $f)) {
        if ($l -eq $b -or $l -match '^# >>> acd-ctx:' + [regex]::Escape($name) + ' \(managed by install\.(sh|ps1)\) >>>$') { $skip = $true; continue }
        if ($l -eq $e) { $skip = $false; continue }
        if (-not $skip) { $out += $l }
    }
    return $out
}

# Set-Block FILE NAME top|bottom CONTENT - replaces the managed block (or
# removes it when CONTENT is empty). Used for TOML and .env files.
function Set-Block([string]$f, [string]$name, [string]$pos, [string]$content) {
    if (-not (Test-Path $f) -and -not $content) { return }
    $b, $e = Block-Markers $name
    $rest = @(Outside-Lines $f $name)
    if (-not $content) { $lines = $rest }
    elseif ($pos -eq 'top') { $lines = @($b) + ($content -split "`n") + @($e) + $rest }
    else { $lines = $rest + @($b) + ($content -split "`n") + @($e) }
    Write-TextFile $f (($lines -join "`n") + "`n")
}

function Outside-Match([string]$f, [string]$name, [string]$regex) {
    return [bool](@(Outside-Lines $f $name) | Where-Object { $_ -match $regex })
}

# ---------------------------------------------------------------------------
# Clients

$script:ProjectId = ''

function Url-Host {
    $h = $S.ListenHost
    if ($h -eq '0.0.0.0') { $h = '127.0.0.1' } elseif ($h -match ':' -and $h -notmatch '^\[') { $h = "[$h]" }
    return "${h}:$($S.Port)"
}

# Base-Url UPSTREAM: local scope adds /p/<id>/ for clients that cannot send the
# X-Acd-Ctx-Project-Dir header.
function Base-Url([string]$up, [bool]$noProject = $false) {
    $p = ''
    if ($S.Scope -eq 'local' -and $script:ProjectId -and -not $noProject) { $p = "/p/$script:ProjectId" }
    return "http://$(Url-Host)$p/ctx/$up"
}

function Mcp-Command { if ($S.Method -eq 'docker') { 'docker' } else { $S.Bin } }
function Mcp-Args { if ($S.Method -eq 'docker') { , @('exec', '-i', $Container, '/acd-ctx', 'mcp') } else { , @('mcp') } }
function Toml-Str([string]$s) { "'" + $s + "'" }   # TOML literal string: no backslash escapes

function Configure-Claude([string]$mode) {
    if ($S.Scope -eq 'local') { $settings = Join-Path $S.Project '.claude\settings.local.json'; $mcpfile = Join-Path $S.Project '.mcp.json' }
    else { $settings = Join-Path $HOME '.claude\settings.json'; $mcpfile = '' }
    if ($mode -eq 'apply' -and (Uses-Proxy)) {
        # Direct: the proxy's default upstream is Anthropic, so the bare address
        # works. Through a gateway the /ctx/ prefix names its Anthropic surface.
        $url = "http://$(Url-Host)"
        if ($S.Gateway) { $url += "/ctx/$(Gateway-Upstream 'anthropic')" }
        $ops = @(, @('set', 'env/ANTHROPIC_BASE_URL', $url)) + @(, @('set', 'env/CLAUDE_CODE_GATEWAY_HINT_HEADERS', '1'))
        if ($S.Scope -eq 'local') { $ops += , @('set', 'env/ANTHROPIC_CUSTOM_HEADERS', "X-Acd-Ctx-Project-Dir: $($S.Project)") }
        Edit-Json $settings $ops
    } else {
        $ops = @(, @('del', 'env/ANTHROPIC_BASE_URL', $null)) + @(, @('del', 'env/CLAUDE_CODE_GATEWAY_HINT_HEADERS', $null))
        if ($S.Scope -eq 'local') { $ops += , @('del', 'env/ANTHROPIC_CUSTOM_HEADERS', $null) }
        if (Test-Path $settings) { Edit-Json $settings $ops }
    }
    if ($mode -eq 'apply' -and (Uses-Mcp)) {
        if ($S.Scope -eq 'local') {
            Edit-Json $mcpfile @(, @('set', 'mcpServers/acd-ctx', [ordered]@{ command = (Mcp-Command); args = (Mcp-Args) }))
            Edit-Json $settings @(, @('append', 'enabledMcpjsonServers', 'acd-ctx'))
        } elseif (Have claude) {
            Show 'claude mcp remove --scope user acd-ctx'
            if (-not $DryRun) { Quiet { claude mcp remove --scope user acd-ctx } | Out-Null }
            Run 'claude' (@('mcp', 'add', '--scope', 'user', 'acd-ctx', '--', (Mcp-Command)) + (Mcp-Args))
        } else {
            Warn "claude CLI not found; register the MCP server with: claude mcp add --scope user acd-ctx -- $(Mcp-Command) $((Mcp-Args) -join ' ')"
        }
    } else {
        if ($S.Scope -eq 'local') {
            if (Test-Path $mcpfile) { Edit-Json $mcpfile @(, @('del', 'mcpServers/acd-ctx', $null)) }
            if (Test-Path $settings) { Edit-Json $settings @(, @('remove', 'enabledMcpjsonServers', 'acd-ctx')) }
        } elseif (Have claude) {
            Show 'claude mcp remove --scope user acd-ctx'
            if (-not $DryRun) { Quiet { claude mcp remove --scope user acd-ctx } | Out-Null }
        }
    }
}

function Configure-Codex([string]$mode) {
    $global = Join-Path $HOME '.codex\config.toml'
    $mcpfile = $(if ($S.Scope -eq 'local') { Join-Path $S.Project '.codex\config.toml' } else { $global })
    # Codex ignores openai_base_url in a project .codex/config.toml, so the
    # proxy setting always goes into the user file.
    if ($mode -eq 'apply' -and (Uses-Proxy)) {
        $url = Base-Url (Gateway-Upstream 'openai') $true
        if (Outside-Match $global 'proxy' '^\s*openai_base_url\s*=') {
            Warn "$global already sets openai_base_url; not changed. Set it to $url"
        } else {
            if ($S.Scope -eq 'local') { Warn "codex: the proxy URL goes into $global (Codex ignores it in a project file), so it applies to every project" }
            Set-Block $global 'proxy' 'top' "openai_base_url = $(Toml-Str $url)"
        }
    } else { Set-Block $global 'proxy' 'top' '' }
    if ($mode -eq 'apply' -and (Uses-Mcp)) {
        if (Outside-Match $mcpfile 'mcp' '^\[mcp_servers\."?acd-ctx"?\]') {
            Warn "$mcpfile already has [mcp_servers.acd-ctx]; not changed"
        } else {
            $ma = Mcp-Args
            $argsToml = '[' + (($ma | ForEach-Object { Toml-Str $_ }) -join ', ') + ']'
            Set-Block $mcpfile 'mcp' 'bottom' "[mcp_servers.acd-ctx]`ncommand = $(Toml-Str (Mcp-Command))`nargs = $argsToml"
            if ($S.Scope -eq 'local') { Info "codex loads $mcpfile only when the project is trusted" }
        }
    } else { Set-Block $mcpfile 'mcp' 'bottom' '' }
}

function Configure-Gemini([string]$mode) {
    $dir = $(if ($S.Scope -eq 'local') { Join-Path $S.Project '.gemini' } else { Join-Path $HOME '.gemini' })
    $envf = Join-Path $dir '.env'
    if ($mode -eq 'apply' -and (Uses-Proxy) -and -not (Gateway-Upstream 'google')) {
        Warn "gemini: $($S.Gateway) has no documented Gemini surface; the Gemini proxy setting is not written"
        Set-Block $envf 'proxy' 'bottom' ''
    } elseif ($mode -eq 'apply' -and (Uses-Proxy)) {
        if (Outside-Match $envf 'proxy' '^\s*(export\s+)?GOOGLE_GEMINI_BASE_URL=') {
            Warn "$envf already sets GOOGLE_GEMINI_BASE_URL; not changed"
        } else {
            Set-Block $envf 'proxy' 'bottom' "GOOGLE_GEMINI_BASE_URL=$(Base-Url (Gateway-Upstream 'google'))"
            Info 'gemini: GOOGLE_GEMINI_BASE_URL is used only with API-key auth (gemini-api-key)'
            if (-not (Is-Loopback $S.ListenHost)) { Warn 'gemini accepts a plain http base URL only for localhost' }
        }
    } else { Set-Block $envf 'proxy' 'bottom' '' }
    $sf = Join-Path $dir 'settings.json'
    if ($mode -eq 'apply' -and (Uses-Mcp)) {
        Edit-Json $sf @(, @('set', 'mcpServers/acd-ctx', [ordered]@{ command = (Mcp-Command); args = (Mcp-Args) }))
    } elseif (Test-Path $sf) { Edit-Json $sf @(, @('del', 'mcpServers/acd-ctx', $null)) }
}

function Configure-Opencode([string]$mode) {
    $f = $(if ($S.Scope -eq 'local') { Join-Path $S.Project 'opencode.json' } else { Join-Path $HOME '.config\opencode\opencode.json' })
    $jsonc = [IO.Path]::ChangeExtension($f, '.jsonc')
    if (-not (Test-Path $f) -and (Test-Path $jsonc)) { Warn "$jsonc has comments and is not edited; add the acd-ctx entries by hand"; return }
    if ($mode -eq 'apply' -and (Uses-Proxy)) {
        Edit-Json $f @(, @('set', 'provider/anthropic/options/baseURL', (Base-Url (Gateway-Upstream 'opencode'))))
    } elseif (Test-Path $f) { Edit-Json $f @(, @('del', 'provider/anthropic/options/baseURL', $null)) }
    # OpenCode 2 keeps servers under mcp.servers; 1.x put them directly under mcp.
    $path = 'mcp/servers/acd-ctx'
    $cmd = @(Mcp-Command) + (Mcp-Args)
    $entry = [ordered]@{ type = 'local'; command = $cmd }
    if (Test-Path $f) {
        try {
            $m = (Get-Content -Raw $f | ConvertFrom-Json).mcp
            if ($m -and -not ($m.PSObject.Properties.Name -contains 'servers') -and @($m.PSObject.Properties | Where-Object { $_.Value -is [PSCustomObject] -and $_.Value.PSObject.Properties.Name -contains 'type' }).Count) {
                $path = 'mcp/acd-ctx'
                $entry = [ordered]@{ type = 'local'; command = $cmd; enabled = $true }
            }
        } catch { }
    }
    if ($mode -eq 'apply' -and (Uses-Mcp)) { Edit-Json $f @(, @('set', $path, $entry)) }
    elseif (Test-Path $f) { Edit-Json $f @(, @('del', $path, $null)) }
}

function Configure-Project {
    if ($S.Scope -ne 'local' -or -not $S.Bin) { return }
    $dir = Join-Path $S.Project '.acd-ctx'
    if (Test-Path $dir) {
        $cfg = Join-Path $dir 'config.yaml'
        if ($S.LocalStorage -and -not ((Test-Path $cfg) -and (Select-String -Quiet -Pattern '^storage:\s*local' -Path $cfg))) {
            Warn "$dir exists without storage: local; add 'storage: local' to its config.yaml or run: acd-ctx project migrate --to local"
        }
        return
    }
    Push-Location $S.Project
    try {
        if ($S.LocalStorage) { Run $S.Bin @('project', 'init', '--local-storage') } else { Run $S.Bin @('project', 'init') }
    } finally { Pop-Location }
}

function Configure-Clients([string]$mode) {
    $script:ProjectId = ''
    if ($S.Scope -eq 'local' -and $mode -eq 'apply') {
        Configure-Project
        if ($S.Bin -and -not $DryRun) {
            Push-Location $S.Project
            try { $line = (Quiet { & $S.Bin project show } | Where-Object { $_ -match '^project\s' }) | Select-Object -First 1 } finally { Pop-Location }
            if ($line) { $script:ProjectId = ($line -split '\s+')[1] }
        }
    }
    foreach ($c in $S.Clients) {
        Info "client: $c"
        switch ($c) {
            'claude' { Configure-Claude $mode }
            'codex' { Configure-Codex $mode }
            'gemini' { Configure-Gemini $mode }
            'opencode' { Configure-Opencode $mode }
        }
    }
    if ($mode -eq 'apply' -and (Uses-Proxy) -and $S.Gateway) { Gateway-Notes }
    if ($mode -eq 'apply' -and $S.Scope -eq 'global' -and (Uses-Proxy) -and (Uses-Mcp)) {
        Warn "global scope: the proxy files these requests under the 'default' project, while acd-ctx mcp looks up the project of the working directory, so the MCP recall tools do not see them. Use -Scope local in each project, or start the clients with the launchers (acd-ctx claude)."
    }
    if ($mode -eq 'apply' -and $S.Method -eq 'docker' -and (Uses-Mcp)) {
        Warn "docker: acd-ctx mcp runs inside the container (working directory /data), so it does not see your project's sessions; prefer -Integration proxy with docker"
    }
    if ($mode -eq 'apply' -and $S.Scope -eq 'global' -and (Uses-Proxy)) {
        Info "global proxy settings carry no project signal: requests go to the 'default' project. Use -Scope local in a project, or the launchers (acd-ctx claude), to separate projects."
    }
    $others = @($S.Clients | Where-Object { $_ -ne 'claude' })
    if ($mode -eq 'apply' -and $S.Scope -eq 'local' -and (Uses-Proxy) -and $others.Count) {
        Info "$($others -join ',') reach the proxy through /p/<id>/, which uses the global database; only Claude Code sends the project directory, which local storage needs."
    }
}

function Remove-DroppedClients {
    if (-not $Saved) { return }
    $keep = $S.Clients
    foreach ($c in @($Saved.Clients)) {
        if ($keep -notcontains $c) {
            Info "client $c is no longer selected: removing its acd-ctx entries"
            $S.Clients = @($c)
            Configure-Clients 'remove'
        }
    }
    $S.Clients = $keep
}

# Gateway-Notes prints what the user still has to set up on the gateway side.
function Gateway-Notes {
    Info "gateway $($S.Gateway) at $($S.GatewayUrl): the clients now send the gateway's key, not the provider's"
    switch ($S.Gateway) {
        'bifrost' { Info 'bifrost: Claude Code needs ANTHROPIC_AUTH_TOKEN=<virtual key sk-bf-...>, and anthropic-beta must be in the header AllowList (Settings, Client Settings) or beta features drop' }
        'omniroute' { Info 'omniroute: Claude Code needs ANTHROPIC_AUTH_TOKEN=<OmniRoute key>; Gemini CLI has no OmniRoute surface' }
        'freellmapi' { Info 'freellmapi: set the issued freellmapi-... token as the client key (ANTHROPIC_AUTH_TOKEN, OPENAI_API_KEY, GEMINI_API_KEY)' }
    }
    if ($S.Method -eq 'docker' -and $S.GatewayUrl -match '127\.0\.0\.1|localhost') { Warn "the container cannot reach $($S.GatewayUrl); use http://host.docker.internal:<port>" }
    Info 'the chain client -> acd-ctx -> gateway is documented, not verified (docs/integrations.md)'
}

function Health {
    if ($DryRun) { return }
    $url = "http://$(Url-Host)/__acd-ctx/health"
    for ($i = 0; $i -lt 10; $i++) {
        try { Invoke-WebRequest -UseBasicParsing -TimeoutSec 2 $url | Out-Null; Info "proxy healthy at $url"; return } catch { Start-Sleep -Seconds 1 }
    }
    Warn "proxy did not answer at $url"
}

# ---------------------------------------------------------------------------
# Commands

function Summary([string]$title = 'Plan') {
    Write-Host ''
    Write-Host $title -ForegroundColor White
    $m = $S.Method; if ($S.Version) { $m += " (version $($S.Version))" }; if ($S.BinDir) { $m += ", bin dir $($S.BinDir)" }
    $sc = $S.Scope; if ($S.Scope -eq 'local') { $sc += " ($($S.Project))" }; if ($S.LocalStorage) { $sc += ', local storage' }
    $px = "$($S.ListenHost):$($S.Port)"; if ($S.Method -eq 'docker') { $px += ", container $Container" } elseif ($S.Service) { $px += ', scheduled task' }
    Write-Host "  method       $m"
    Write-Host "  scope        $sc"
    Write-Host "  integration  $($S.Integration)"
    Write-Host "  clients      $($S.Clients -join ',')"
    Write-Host "  upstream     $(if ($S.Gateway) { "gateway $($S.Gateway) ($($S.GatewayUrl))" } else { 'providers (direct)' })"
    Write-Host "  proxy        $px"
}

function Cmd-Install {
    Set-Defaults; Validate; Resolve-Version; Summary
    if (-not (YesNo 'Proceed?' $true)) { Die 'cancelled' }
    Install-Binary
    if ($S.Bin -and -not $DryRun) { Info "installed: $(& $S.Bin version)" }
    if ((Uses-Proxy) -and $S.Service) { Service-Install; Health }
    if ($S.Integration -ne 'none') { Configure-Clients 'apply' }
    Save-State
    Info 'done'
}

function Cmd-Update {
    if (-not $Saved) { Die "no saved installation ($StateFile); run install first" }
    Set-Defaults
    $oldBin = $S.Bin
    $from = $Saved.Version
    Resolve-Version
    Info "updating from $from to $($S.Version) ($($S.Method))"
    if (-not (YesNo 'Proceed?' $true)) { Die 'cancelled' }
    Install-Binary
    if ($S.Method -ne 'docker' -and $S.Service) { Service-Install }
    if ($S.Service -or $S.Method -eq 'docker') { Health }
    if (-not $NoRestartMcp) {
        $n = Stop-Acd 'mcp'
        if ($n) { Info 'the clients start the new MCP binary on the next tool call, or reconnect with /mcp' }
    }
    # The client files name the binary; rewrite them only if its path changed.
    if ($S.Integration -ne 'none' -and $S.Bin -ne $oldBin) { Configure-Clients 'apply' }
    Save-State
    Info "updated to $($S.Version)"
}

function Cmd-Configure {
    if (-not $Saved) { Warn 'no saved installation; using defaults' }
    Set-Defaults
    if ($Saved) { $S.Version = $Saved.Version }
    if (-not $S.Bin -and $S.Method -ne 'docker') { $c = Get-Command acd-ctx -ErrorAction SilentlyContinue; if ($c) { $S.Bin = $c.Source } }
    if (-not $S.Bin -and $S.Method -ne 'docker') { Die 'acd-ctx is not installed; run install first' }
    Validate; Summary
    if (-not (YesNo 'Apply?' $true)) { Die 'cancelled' }
    if (Uses-Proxy) {
        if ($S.Method -eq 'docker') { Docker-Recreate } elseif ($S.Service) { Service-Install } else { Service-Remove }
        Health
    }
    Remove-DroppedClients
    Configure-Clients 'apply'
    Save-State
}

function Cmd-Uninstall {
    Set-Defaults; Summary
    if (-not (YesNo 'Remove the service and the acd-ctx client entries?' $false)) { Die 'cancelled' }
    Service-Remove
    Configure-Clients 'remove'
    if ($Purge) {
        if ($S.Method -eq 'docker') { try { Run 'docker' @('volume', 'rm', $Volume) } catch { Warn $_ } }
        if ($S.Bin -and $S.Method -ne 'docker') {
            Stop-Acd 'mcp' | Out-Null
            Show "remove $($S.Bin)"; if (-not $DryRun) { Remove-Item -Force $S.Bin -ErrorAction SilentlyContinue }
        }
        Show "remove $StateFile"; if (-not $DryRun) { Remove-Item -Force $StateFile -ErrorAction SilentlyContinue }
        Info 'the database (~\.local\share\acd-ctx and any .acd-ctx\ of a project) is kept; remove it with acd-ctx clean or by hand'
    }
    Info 'uninstalled'
}

function Cmd-Status {
    Set-Defaults
    if (-not $S.Bin) { $c = Get-Command acd-ctx -ErrorAction SilentlyContinue; if ($c) { $S.Bin = $c.Source } }
    Write-Host "settings     $StateFile$(if (-not $Saved) { ' (none)' })"
    Summary 'Settings'
    if ($S.Method -eq 'docker') { Write-Host "image        $(Quiet { docker inspect -f '{{.Config.Image}}' $Container })" }
    elseif ($S.Bin) { Write-Host "binary       $($S.Bin) ($(Quiet { & $S.Bin version }))" }
    Service-Ctl 'status'
    Health
}

function Cmd-Service {
    Set-Defaults
    $a = $(if ($Action) { $Action } else { 'status' })
    switch ($a) {
        'install' { $S.Service = $true; Service-Install; Save-State }
        'remove' { Service-Remove; $S.Service = $false; Save-State }
        default { Service-Ctl $a }
    }
}

function Ask-Setup {
    $S.Scope = Choose 'Scope' $(if ($S.Scope) { $S.Scope } else { 'global' }) @(
        'global|Global: user-level client configuration',
        'local|Local: one project (files in the project directory)')
    if ($S.Scope -eq 'local') {
        $S.Project = Ask 'Project directory' $(if ($S.Project) { $S.Project } else { (Get-Location).Path })
        if ($S.Method -ne 'docker') { $S.LocalStorage = YesNo "Keep this project's database inside the project (.acd-ctx\)?" ([bool]$S.LocalStorage) }
    }
    $S.Integration = Choose 'How should the clients use acd-ctx?' $(if ($S.Integration) { $S.Integration } else { 'both' }) @(
        'both|Proxy and MCP',
        'proxy|Proxy only (the clients send their API calls through acd-ctx)',
        'mcp|MCP only (recall tools: retrieve, search_context, decompress, ctx_status)',
        'none|Neither (install only)')
    if ($S.Integration -ne 'none') {
        $d = $(if ($S.Clients.Count) { $S.Clients -join ',' } else { 'claude' })
        $S.Clients = @((Ask 'Clients (comma list of claude,codex,gemini,opencode, or all)' $d) -split ',' | ForEach-Object { $_.Trim().ToLower() } | Where-Object { $_ })
        if ($S.Clients -contains 'all') { $S.Clients = $AllClients }
    }
    if (Uses-Proxy) {
        $g = Choose 'Where does acd-ctx send the traffic?' $(if ($S.Gateway) { $S.Gateway } else { 'direct' }) @(
            'direct|Directly to the providers (Anthropic, OpenAI, Google)', 'omniroute|OmniRoute gateway',
            'freellmapi|freellmapi gateway', 'bifrost|Bifrost gateway')
        if ($g -eq 'direct') { $S.Gateway = ''; $S.GatewayUrl = '' }
        else { $S.Gateway = $g; $S.GatewayUrl = Ask 'Gateway URL' $(if ($S.GatewayUrl) { $S.GatewayUrl } else { Gateway-Default-Url $g }) }
    }
    if (Uses-Proxy) {
        $S.ListenHost = Ask 'Proxy listen IP' $(if ($S.ListenHost) { $S.ListenHost } else { '127.0.0.1' })
        $S.Port = [int](Ask 'Proxy port' $(if ($S.Port) { "$($S.Port)" } else { '8787' }))
        if ($S.Method -ne 'docker') { $S.Service = YesNo 'Run the proxy at logon (scheduled task)?' $true }
    }
}

function Menu {
    $a = Choose 'acd-ctx installer - what do you want to do?' 'install' @(
        'install|Install', 'update|Update the application to a new version', 'configure|Update the configuration',
        'service|Manage the service (start, stop, restart, status)', 'status|Show status', 'uninstall|Uninstall')
    switch ($a) {
        'install' {
            $S.Method = Choose 'Install method' $(if ($S.Method) { $S.Method } else { Default-Method }) @(
                'go|go install (needs Go)', 'release|Download the release binary from GitHub',
                'docker|Docker image (ghcr.io)', 'custom|Custom: build from a checkout or copy a binary')
            if ($S.Method -eq 'custom') {
                $src = Ask 'Checkout directory to build, or path of acd-ctx.exe' (Get-Location).Path
                if (Test-Path $src -PathType Container) { $S.FromSource = $src } else { $S.Binary = $src }
                $S.BinDir = Ask 'Install directory' $(if ($S.BinDir) { $S.BinDir } else { Join-Path $env:LOCALAPPDATA 'Programs\acd-ctx' })
            }
            $S.Version = Ask 'Version' 'latest'
            Ask-Setup
            Cmd-Install
        }
        'update' {
            $S.Version = Ask 'Version' 'latest'
            if (-not (YesNo 'Stop running acd-ctx MCP servers so the clients load the new binary?' $true)) { $script:NoRestartMcp = $true }
            Cmd-Update
        }
        'configure' { Ask-Setup; Cmd-Configure }
        'service' {
            $script:Action = Choose 'Service' 'status' @('status|Status', 'start|Start', 'stop|Stop', 'restart|Restart', 'install|Install or rewrite', 'remove|Remove')
            Cmd-Service
        }
        'status' { Cmd-Status }
        'uninstall' {
            if (YesNo 'Also remove the binary, the Docker volume and the saved settings?' $false) { $script:Purge = $true }
            Cmd-Uninstall
        }
    }
}

# ---------------------------------------------------------------------------
# Main

if ($Command -eq 'help') { Get-Help $PSCommandPath -Detailed; exit 0 }
if (-not $Command) { $Command = $(if ($PSBoundParameters.Count -eq 0) { 'menu' } else { 'install' }) }
if ($DryRun) { Info 'dry run: nothing is changed' }
# An explicit install starts from the defaults and the flags; the other
# commands (and the menu, as question defaults) use the saved settings.
if ($Command -ne 'install') { Load-State } elseif (Test-Path $StateFile) { $Saved = Get-Content -Raw $StateFile | ConvertFrom-Json }
switch ($Command) {
    'menu' { Menu }
    'install' { Cmd-Install }
    'update' { Cmd-Update }
    'configure' { Cmd-Configure }
    'service' { Cmd-Service }
    'status' { Cmd-Status }
    'uninstall' { Cmd-Uninstall }
}

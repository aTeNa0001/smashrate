<#
One-shot setup for the mini PC (Windows 10/11). Run in an elevated PowerShell:

    Set-ExecutionPolicy -Scope Process Bypass -Force
    irm https://github.com/aTeNa0001/smashrate/releases/latest/download/setup.ps1 | iex

or, from a checkout:  powershell -ExecutionPolicy Bypass -File deploy\setup.ps1

Build on the PC itself instead of downloading a release (no GitHub Release needed):

    & ([scriptblock]::Create((irm https://raw.githubusercontent.com/aTeNa0001/smashrate/main/deploy/setup.ps1))) -FromSource

Re-running the same line later updates to the newest source and rebuilds.

What it does:
  1. installs ffmpeg and cloudflared with winget (skipped if present); Go and Git with -FromSource
  2. downloads the latest smashrate.exe release into C:\smashrate (or clones + builds with -FromSource)
  3. writes smashrate.toml from the example if none exists
  4. registers the "smashrate" scheduled task (starts at logon, restarts on exit)
  5. prints the remaining manual steps (config values, cloudflared tunnel)
#>
param(
    [string]$Dir = "C:\smashrate",
    [string]$Repo = "aTeNa0001/smashrate",
    # Build from the git source on this PC instead of downloading a release
    # (installs Go and Git; no GitHub Release needed).
    [switch]$FromSource,
    [string]$Branch = "main",
    [switch]$NoTask
)
$ErrorActionPreference = "Stop"

function Have($cmd) { return [bool](Get-Command $cmd -ErrorAction SilentlyContinue) }
function RefreshPath {
    $env:Path = [Environment]::GetEnvironmentVariable("Path", "Machine") + ";" + [Environment]::GetEnvironmentVariable("Path", "User")
    # Freshly installed tools are not always on PATH in this session yet; add their usual homes.
    $extra = @(
        "C:\Program Files\Go\bin",
        "$env:LOCALAPPDATA\Programs\Go\bin",
        "C:\Program Files\Git\cmd",
        "$env:LOCALAPPDATA\Microsoft\WinGet\Links",
        "C:\Program Files\cloudflared"
    )
    $extra += Get-ChildItem "$env:LOCALAPPDATA\Microsoft\WinGet\Packages" -Directory -Filter "Gyan.FFmpeg*" -ErrorAction SilentlyContinue |
        ForEach-Object { Get-ChildItem $_.FullName -Directory -Recurse -Filter bin -ErrorAction SilentlyContinue | ForEach-Object FullName }
    foreach ($p in $extra) { if ($p -and (Test-Path $p) -and ($env:Path -notlike "*$p*")) { $env:Path += ";$p" } }
}
function WingetInstall($id) {
    Write-Host "   installing $id (winget; this downloads 50-150 MB, progress below)" -ForegroundColor DarkGray
    winget install --id $id -e --source winget --accept-source-agreements --accept-package-agreements --disable-interactivity
    if ($LASTEXITCODE -ne 0) { Write-Warning "winget exited with $LASTEXITCODE for $id" }
    RefreshPath
}

Write-Host "== 1/5 tools" -ForegroundColor Cyan
if (-not (Have winget)) { throw "winget not found; install 'App Installer' from the Microsoft Store first" }
foreach ($t in @(@{cmd = "ffmpeg"; id = "Gyan.FFmpeg"}, @{cmd = "cloudflared"; id = "Cloudflare.cloudflared"})) {
    if (Have $t.cmd) { Write-Host "   $($t.cmd): present" } else { WingetInstall $t.id }
}
if ($FromSource) {
    foreach ($t in @(@{cmd = "git"; id = "Git.Git"}, @{cmd = "go"; id = "GoLang.Go"})) {
        if (Have $t.cmd) { Write-Host "   $($t.cmd): present" } else { WingetInstall $t.id }
    }
}
RefreshPath
if (-not (Have ffmpeg)) { Write-Warning "ffmpeg still not on PATH; open a new terminal after setup" }
if ($FromSource) {
    foreach ($cmd in "git", "go") {
        if (-not (Have $cmd)) { throw "$cmd was installed but is not on PATH yet. Close this window, open a new admin PowerShell and run the same two lines again." }
    }
}

Write-Host "== 2/5 smashrate.exe" -ForegroundColor Cyan
New-Item -ItemType Directory -Force $Dir | Out-Null
$exe = Join-Path $Dir "smashrate.exe"
$src = Join-Path $Dir "src"
if ($FromSource) {
    if (Test-Path (Join-Path $src ".git")) {
        git -C $src fetch -q origin; git -C $src checkout -q $Branch; git -C $src pull -q --ff-only origin $Branch
    } else {
        git clone -q -b $Branch "https://github.com/$Repo.git" $src
    }
    Push-Location $src
    try { go build -trimpath -ldflags "-s -w -X main.version=$(git rev-parse --short HEAD)" -o $exe .\cmd\smashrate } finally { Pop-Location }
} else {
    $url = "https://github.com/$Repo/releases/latest/download/smashrate-windows-amd64.exe"
    Invoke-WebRequest -Uri $url -OutFile $exe -UseBasicParsing
    Unblock-File $exe
}
& $exe version

Write-Host "== 3/5 config" -ForegroundColor Cyan
$toml = Join-Path $Dir "smashrate.toml"
$fileSource = { param($name) if ($FromSource) { Copy-Item (Join-Path $src $name) (Join-Path $Dir (Split-Path $name -Leaf)) -Force } else { Invoke-WebRequest -Uri "https://github.com/$Repo/releases/latest/download/$(Split-Path $name -Leaf)" -OutFile (Join-Path $Dir (Split-Path $name -Leaf)) -UseBasicParsing } }
if (-not (Test-Path $toml)) {
    & $fileSource "smashrate.example.toml"
    Move-Item (Join-Path $Dir "smashrate.example.toml") $toml -Force
    Write-Host "wrote $toml (edit it: base_url, [server.auth] password, [slack], [vision])"
} else {
    Write-Host "keeping existing $toml"
}
foreach ($f in "deploy\install-task.ps1", "deploy\backup.ps1", "deploy\cloudflared.config.yml") { & $fileSource $f }

Write-Host "== 4/5 scheduled task" -ForegroundColor Cyan
if ($NoTask) {
    Write-Host "skipped (-NoTask)"
} else {
    # an always-on box must not sleep or hibernate while plugged in
    powercfg /change standby-timeout-ac 0 | Out-Null
    powercfg /change hibernate-timeout-ac 0 | Out-Null
    powercfg /change monitor-timeout-ac 10 | Out-Null
    & powershell -NoProfile -ExecutionPolicy Bypass -File (Join-Path $Dir "install-task.ps1") -Exe $exe -WorkDir $Dir
    schtasks /Create /F /TN smashrate-backup /SC DAILY /ST 04:00 /TR "powershell -NoProfile -ExecutionPolicy Bypass -File `"$Dir\backup.ps1`"" | Out-Null
}

Write-Host "== 5/5 doctor" -ForegroundColor Cyan
Push-Location $Dir
try { & $exe doctor } catch { } finally { Pop-Location }

Write-Host "== next steps" -ForegroundColor Cyan
@"
Installed to $Dir. Web UI: http://localhost:8080  (health: http://localhost:8080/healthz)

Remaining manual steps:
  1. Edit $toml
       [server]      addr = "127.0.0.1:8080" once behind the tunnel, base_url = https://<your host>
       [server.auth] password = "...", trust_cloudflare_access = true
       [slack]       enabled = true, bot_token / app_token / channel
       [vision]      enabled = true, input = "video=<device>"  (list devices: smashrate vision devices)
     then restart:  schtasks /End /TN smashrate ; schtasks /Run /TN smashrate
     and re-check:  cd $Dir ; .\smashrate.exe doctor
  2. Cloudflare Tunnel (see docs/DEPLOY.md):
       cloudflared tunnel login
       cloudflared tunnel create smashrate
       cloudflared tunnel route dns smashrate <host>
       copy $Dir\cloudflared.config.yml to %USERPROFILE%\.cloudflared\config.yml and fill <TUNNEL-ID>
       cloudflared service install
  3. If Windows blocks smashrate.exe ("Application Control" / Smart App Control):
       Windows Security > App & browser control > Smart App Control > Off
"@

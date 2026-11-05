<#
One-shot setup for the mini PC (Windows 10/11). Run in an elevated PowerShell:

    Set-ExecutionPolicy -Scope Process Bypass -Force
    irm https://github.com/aTeNa0001/smashrate/releases/latest/download/setup.ps1 | iex

or, from a checkout:  powershell -ExecutionPolicy Bypass -File deploy\setup.ps1

What it does:
  1. installs ffmpeg and cloudflared with winget (skipped if present)
  2. downloads the latest smashrate.exe release into C:\smashrate
  3. writes smashrate.toml from the example if none exists
  4. registers the "smashrate" scheduled task (starts at logon, restarts on exit)
  5. prints the remaining manual steps (config values, cloudflared tunnel)
#>
param(
    [string]$Dir = "C:\smashrate",
    [string]$Repo = "aTeNa0001/smashrate",
    [switch]$NoTask
)
$ErrorActionPreference = "Stop"

function Have($cmd) { return [bool](Get-Command $cmd -ErrorAction SilentlyContinue) }

Write-Host "== 1/5 tools" -ForegroundColor Cyan
if (-not (Have winget)) { throw "winget not found; install 'App Installer' from the Microsoft Store first" }
if (-not (Have ffmpeg)) { winget install --id Gyan.FFmpeg -e --accept-source-agreements --accept-package-agreements --silent | Out-Null }
if (-not (Have cloudflared)) { winget install --id Cloudflare.cloudflared -e --accept-source-agreements --accept-package-agreements --silent | Out-Null }
$env:Path = [Environment]::GetEnvironmentVariable("Path", "Machine") + ";" + [Environment]::GetEnvironmentVariable("Path", "User")
if (-not (Have ffmpeg)) { Write-Warning "ffmpeg still not on PATH; open a new terminal after setup" }

Write-Host "== 2/5 smashrate.exe" -ForegroundColor Cyan
New-Item -ItemType Directory -Force $Dir | Out-Null
$exe = Join-Path $Dir "smashrate.exe"
$url = "https://github.com/$Repo/releases/latest/download/smashrate-windows-amd64.exe"
Invoke-WebRequest -Uri $url -OutFile $exe -UseBasicParsing
Unblock-File $exe
& $exe 2>&1 | Select-Object -First 1 | Out-Null   # sanity: runs at all (prints usage)

Write-Host "== 3/5 config" -ForegroundColor Cyan
$toml = Join-Path $Dir "smashrate.toml"
if (-not (Test-Path $toml)) {
    Invoke-WebRequest -Uri "https://github.com/$Repo/releases/latest/download/smashrate.example.toml" -OutFile $toml -UseBasicParsing
    Write-Host "wrote $toml (edit it: base_url, [server.auth] password, [slack], [vision])"
} else {
    Write-Host "keeping existing $toml"
}
foreach ($f in "install-task.ps1", "backup.ps1", "cloudflared.config.yml") {
    Invoke-WebRequest -Uri "https://github.com/$Repo/releases/latest/download/$f" -OutFile (Join-Path $Dir $f) -UseBasicParsing
}

Write-Host "== 4/5 scheduled task" -ForegroundColor Cyan
if ($NoTask) {
    Write-Host "skipped (-NoTask)"
} else {
    & powershell -NoProfile -ExecutionPolicy Bypass -File (Join-Path $Dir "install-task.ps1") -Exe $exe -WorkDir $Dir
    schtasks /Create /F /TN smashrate-backup /SC DAILY /ST 04:00 /TR "powershell -NoProfile -ExecutionPolicy Bypass -File `"$Dir\backup.ps1`"" | Out-Null
}

Write-Host "== 5/5 next steps" -ForegroundColor Cyan
@"
Installed to $Dir. Web UI: http://localhost:8080  (health: http://localhost:8080/healthz)

Remaining manual steps:
  1. Edit $toml
       [server]      addr = "127.0.0.1:8080" once behind the tunnel, base_url = https://<your host>
       [server.auth] password = "...", trust_cloudflare_access = true
       [slack]       enabled = true, bot_token / app_token / channel
       [vision]      enabled = true, input = "video=<device>"  (list devices: smashrate vision devices)
     then restart:  schtasks /End /TN smashrate ; schtasks /Run /TN smashrate
  2. Cloudflare Tunnel (see docs/DEPLOY.md):
       cloudflared tunnel login
       cloudflared tunnel create smashrate
       cloudflared tunnel route dns smashrate <host>
       copy $Dir\cloudflared.config.yml to %USERPROFILE%\.cloudflared\config.yml and fill <TUNNEL-ID>
       cloudflared service install
  3. If Windows blocks smashrate.exe ("Application Control" / Smart App Control):
       Windows Security > App & browser control > Smart App Control > Off
"@

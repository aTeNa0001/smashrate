<#
Enables remote maintenance of the mini PC over SSH (PowerShell as the
login shell). Run once in an elevated PowerShell:

    powershell -ExecutionPolicy Bypass -File deploy\enable-ssh.ps1

Then publish port 22 through the Cloudflare Tunnel (see
deploy\cloudflared.config.yml: hostname ssh.<your domain>) and protect it
with Cloudflare Access. From any PC:

    cloudflared access ssh --hostname ssh.<your domain>     (one-off)
    or in ~/.ssh/config:
      Host smash-pc
        HostName ssh.<your domain>
        User <windows user>
        ProxyCommand cloudflared access ssh --hostname %h

Nothing is opened on the office network: cloudflared on the mini PC
connects outward, and Access asks for the sign-in before the SSH prompt.
#>
$ErrorActionPreference = "Stop"
$cap = Get-WindowsCapability -Online -Name "OpenSSH.Server*" | Select-Object -First 1
if ($cap.State -ne "Installed") { Add-WindowsCapability -Online -Name $cap.Name | Out-Null }
Set-Service -Name sshd -StartupType Automatic
Start-Service sshd
# PowerShell as the default shell for SSH sessions
New-Item -Path "HKLM:\SOFTWARE\OpenSSH" -Force | Out-Null
Set-ItemProperty -Path "HKLM:\SOFTWARE\OpenSSH" -Name DefaultShell -Value "C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe" -Force
# Only the tunnel talks to sshd (localhost); no firewall rule for the LAN is needed,
# but the capability installs one for port 22 — restrict it to loopback.
Get-NetFirewallRule -Name "OpenSSH-Server-In-TCP" -ErrorAction SilentlyContinue | Set-NetFirewallRule -RemoteAddress 127.0.0.1
Restart-Service sshd
Write-Host "sshd is running (PowerShell shell). Add the ssh.<domain> ingress to cloudflared and an Access policy for it."

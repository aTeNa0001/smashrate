<#
Registers smashrate as a scheduled task that starts at logon of the current
user and restarts if it exits. Run from the folder that holds smashrate.exe
and smashrate.toml, in an elevated PowerShell:

    powershell -ExecutionPolicy Bypass -File deploy\install-task.ps1

Remove with:  Unregister-ScheduledTask -TaskName smashrate -Confirm:$false
#>
param(
    [string]$Exe = (Join-Path (Get-Location) "smashrate.exe"),
    [string]$WorkDir = (Get-Location).Path,
    [string]$TaskName = "smashrate"
)
if (-not (Test-Path $Exe)) { throw "smashrate.exe not found at $Exe (go build -o smashrate.exe ./cmd/smashrate)" }

$action = New-ScheduledTaskAction -Execute $Exe -Argument "serve" -WorkingDirectory $WorkDir
$trigger = New-ScheduledTaskTrigger -AtLogOn -User $env:USERNAME
$settings = New-ScheduledTaskSettingsSet -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1) `
    -ExecutionTimeLimit ([TimeSpan]::Zero) -StartWhenAvailable -DontStopIfGoingOnBatteries -AllowStartIfOnBatteries
Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger -Settings $settings -Force | Out-Null
Start-ScheduledTask -TaskName $TaskName
Write-Host "Registered and started task '$TaskName' ($Exe serve in $WorkDir)"

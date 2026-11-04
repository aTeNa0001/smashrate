<#
Copies the SQLite database to data\backup\smashrate-YYYYMMDD.db. Uses SQLite's
online backup when sqlite3 is installed (safe while the server runs), otherwise
a plain copy of the db and its -wal file. Keeps the last 30 files.
Schedule daily:
  schtasks /Create /TN smashrate-backup /SC DAILY /ST 04:00 /TR "powershell -ExecutionPolicy Bypass -File <path>\deploy\backup.ps1"
#>
param([string]$Db = "data\smashrate.db", [string]$Dir = "data\backup")
New-Item -ItemType Directory -Force $Dir | Out-Null
$dst = Join-Path $Dir ("smashrate-" + (Get-Date -Format "yyyyMMdd") + ".db")
if (Get-Command sqlite3 -ErrorAction SilentlyContinue) {
    sqlite3 $Db ".backup '$dst'"
} else {
    Copy-Item $Db $dst -Force
    if (Test-Path "$Db-wal") { Copy-Item "$Db-wal" "$dst-wal" -Force }
}
Get-ChildItem $Dir -Filter "smashrate-*.db" | Sort-Object Name -Descending | Select-Object -Skip 30 | Remove-Item -Force
Write-Host "backup written to $dst"

# Installer Karta untuk Windows dari rilis GitHub terbaru.
# Pakai: .\install.ps1 [-Version v0.3.1] [-Silent]
# Tanpa -Version = rilis terbaru. Tanpa -Silent = wizard instalasi dibuka.
param(
	[string]$Version = $env:KARTA_VERSION,
	[switch]$Silent
)

$ErrorActionPreference = "Stop"
$Repo = "dchya24/karta"

if (-not $Version) {
	$rel = Invoke-RestMethod "https://api.github.com/repos/$Repo/releases/latest"
	$Version = $rel.tag_name
}
Write-Host "Karta $Version"

$pkg = "Karta-Setup-windows-amd64.exe"
$url = "https://github.com/$Repo/releases/download/$Version/$pkg"
$tmp = Join-Path $env:TEMP $pkg
Write-Host "Mengunduh $url …"
Invoke-WebRequest -Uri $url -OutFile $tmp -UseBasicParsing

if ($Silent) {
	# Inno Setup: /VERYSILENT tanpa wizard, /NORESTART tanpa prompt reboot.
	Start-Process -FilePath $tmp -ArgumentList "/VERYSILENT", "/NORESTART" -Wait
} else {
	Start-Process -FilePath $tmp -Wait
}

Remove-Item $tmp -ErrorAction SilentlyContinue
Write-Host "Selesai. Jalankan Karta dari Start Menu."

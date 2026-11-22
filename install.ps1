# Installs Hermes Desktop for the current user, without administrator
# rights: the installer of the latest release, for this PC's architecture,
# in %LOCALAPPDATA%\Programs\Hermes, where the app can update itself.
#
#   irm https://github.com/j4ckxyz/hermes-desktop-mygo/releases/latest/download/install.ps1 | iex
#   .\install.ps1     # the same, from a checkout
#
# It runs the install.ps1 of the latest release, which `mygo build` made:
# that script checks the installer's SHA-256 against the release's
# SHA256SUMS, closes Hermes if it runs, installs silently and opens it.
# Uninstall from Settings > Apps.
$ErrorActionPreference = 'Stop'
# Windows PowerShell 5.1 may not offer TLS 1.2 by itself.
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

$base = 'https://github.com/j4ckxyz/hermes-desktop-mygo/releases/latest/download'
if ($env:HERMES_DESKTOP_DOWNLOAD_URL) { $base = $env:HERMES_DESKTOP_DOWNLOAD_URL.TrimEnd('/') }

$work = Join-Path ([IO.Path]::GetTempPath()) ('hermes-install-' + [Guid]::NewGuid())
New-Item -ItemType Directory -Path $work | Out-Null
try {
	$script = Join-Path $work 'install.ps1'
	Invoke-WebRequest -UseBasicParsing -Uri "$base/install.ps1" -OutFile $script
	& powershell -NoProfile -ExecutionPolicy Bypass -File $script
	if ($LASTEXITCODE) { throw "the installer script failed ($LASTEXITCODE)" }
} finally {
	Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue
}

[CmdletBinding()]
param(
    [string]$Output = "svcctl.exe",
    [switch]$Console,
    [switch]$DownloadToolchain
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $root

function Find-Go {
    $toolchain = Join-Path $root "toolchain\go\bin\go.exe"
    if (Test-Path $toolchain) { return $toolchain }
    if ($env:SVCCTL_GO) { return $env:SVCCTL_GO }
    $cmd = Get-Command go -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }
    return $null
}

function Get-GoMinor($go) {
    $v = & $go env GOVERSION
    if ($v -match 'go1\.(\d+)') { return [int]$Matches[1] }
    return 999
}

function Find-Rsrc($go) {
    $cmd = Get-Command rsrc -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }
    $candidate = Join-Path (& $go env GOPATH) "bin\rsrc.exe"
    if (Test-Path $candidate) { return $candidate }
    return $null
}

$go = Find-Go

if (-not $go -and $DownloadToolchain) {
    $toolchainDir = Join-Path $root "toolchain"
    New-Item -ItemType Directory -Force -Path $toolchainDir | Out-Null
    $zip = Join-Path $toolchainDir "go1.20.14.zip"
    Write-Host "Downloading Go 1.20.14 (last version supporting Windows 7)..."
    Invoke-WebRequest -Uri "https://go.dev/dl/go1.20.14.windows-amd64.zip" -OutFile $zip
    Expand-Archive -Path $zip -DestinationPath $toolchainDir -Force
    Remove-Item $zip -Force
    $go = Join-Path $toolchainDir "go\bin\go.exe"
}

if (-not $go) {
    throw "Go not found. Install Go 1.20.x or run with -DownloadToolchain."
}

Write-Host ("Using " + (& $go version))
if ((Get-GoMinor $go) -gt 20) {
    Write-Warning "This Go version does not support Windows 7. Use Go 1.20.x to build a Win7-compatible exe."
}

& $go mod tidy
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

# walk requires the Common Controls 6.0 manifest, otherwise startup fails with TTM_ADDTOOL failed.
$rsrc = Find-Rsrc $go
if (-not $rsrc) {
    Write-Host "rsrc not found, installing it to embed the application manifest..."
    & $go install github.com/akavel/rsrc@latest
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to install rsrc. Run manually: go install github.com/akavel/rsrc@latest"
    }
    $rsrc = Join-Path (& $go env GOPATH) "bin\rsrc.exe"
}
if (-not (Test-Path $rsrc)) {
    throw "rsrc executable not found, cannot embed the application manifest."
}

$arch = (& $go env GOARCH).Trim()
if ($arch -ne "386" -and $arch -ne "amd64") { $arch = "amd64" }

Write-Host "Generating resources (manifest + icon)..."
& $rsrc -arch $arch -manifest .\svcctl.manifest -ico .\internal\ui\icon.ico -o .\cmd\svcctl\rsrc.syso
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

$ldflags = "-s -w"
if (-not $Console) { $ldflags += " -H=windowsgui" }

& $go build -trimpath -ldflags $ldflags -o $Output .\cmd\svcctl
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host ("Build finished: " + (Join-Path $root $Output))

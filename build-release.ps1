param(
    [string]$Version = "0.1.0"
)

$ErrorActionPreference = "Stop"

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    throw "Go is not installed or not on PATH. Install Go first: https://go.dev/dl/"
}

$dist = Join-Path $PSScriptRoot "dist"
if (Test-Path $dist) {
    Remove-Item -Recurse -Force $dist
}
New-Item -ItemType Directory -Path $dist | Out-Null

$targets = @(
    @{ GOOS = "windows"; GOARCH = "amd64"; Output = "nudge_windows_amd64.exe" },
    @{ GOOS = "windows"; GOARCH = "arm64"; Output = "nudge_windows_arm64.exe" },
    @{ GOOS = "linux"; GOARCH = "amd64"; Output = "nudge_linux_amd64" },
    @{ GOOS = "linux"; GOARCH = "arm64"; Output = "nudge_linux_arm64" },
    @{ GOOS = "darwin"; GOARCH = "amd64"; Output = "nudge_darwin_amd64" },
    @{ GOOS = "darwin"; GOARCH = "arm64"; Output = "nudge_darwin_arm64" }
)

$previousGoos = $env:GOOS
$previousGoarch = $env:GOARCH

try {
    foreach ($target in $targets) {
        $env:GOOS = $target.GOOS
        $env:GOARCH = $target.GOARCH
        $outputPath = Join-Path $dist $target.Output

        Write-Host "building $($target.Output)..."
        go build -trimpath -ldflags "-s -w -X main.version=$Version" -o $outputPath ./cmd/nudge
    }
}
finally {
    $env:GOOS = $previousGoos
    $env:GOARCH = $previousGoarch
}

Write-Host "release artifacts written to $dist"
# nudge installer (Windows) — usage:  irm https://raw.githubusercontent.com/eduardsjermaks/nudge/main/install.ps1 | iex
# Downloads the right binary into %LOCALAPPDATA%\Programs\nudge and adds it to the user PATH.
$ErrorActionPreference = "Stop"

# Override for testing against a different tag or release host.
$baseUrl = $env:NUDGE_RELEASE_URL
if (-not $baseUrl) { $baseUrl = "https://github.com/eduardsjermaks/nudge/releases/latest/download" }

$arch = if ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture -eq "Arm64") { "arm64" } else { "amd64" }
$dest = Join-Path $env:LOCALAPPDATA "Programs\nudge"
New-Item -ItemType Directory -Force $dest | Out-Null
$exe = Join-Path $dest "nudge.exe"

Write-Host "downloading nudge ($arch)..."
Invoke-WebRequest -UseBasicParsing "$baseUrl/nudge_windows_$arch.exe" -OutFile $exe

# Add to user PATH if missing.
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($userPath -notlike "*$dest*") {
    [Environment]::SetEnvironmentVariable("Path", "$userPath;$dest", "User")
    $env:Path = "$env:Path;$dest"
    Write-Host "added $dest to your user PATH (new terminals will pick it up)"
}

Write-Host "nudge installed: $exe"
Write-Host ""
Write-Host "next step - run the wizard:"
Write-Host ""
Write-Host "  nudge setup"
Write-Host ""
Write-Host "It configures a cloud provider (or installs Ollama via winget and pulls"
Write-Host "the model, if you prefer local), and adds the PowerShell hook - asking"
Write-Host "before every change."
Write-Host "Safe to re-run any time. Manual steps, if you prefer doing it by hand:"
Write-Host "https://github.com/eduardsjermaks/nudge#install-5-minutes"

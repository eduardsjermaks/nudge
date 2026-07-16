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
Write-Host "next steps:"
Write-Host "  1. pick a model - either one:"
Write-Host "       a. local (default, private, free per query):"
Write-Host "            install Ollama:  winget install Ollama.Ollama"
Write-Host "            pull the model:  ollama pull qwen2.5-coder:1.5b"
Write-Host "       b. cloud (needs an API key; queries leave your machine):"
Write-Host "            put  provider = `"anthropic`"  (or openai / azure / deepseek) in"
Write-Host "            $env:APPDATA\nudge\config.toml"
Write-Host "            and set the matching key, e.g."
Write-Host "            [Environment]::SetEnvironmentVariable('ANTHROPIC_API_KEY','...','User')"
Write-Host "            details: https://github.com/eduardsjermaks/nudge#choosing-a-brain"
Write-Host "  2. enable the shell hook (bare 'nudge' / 'fix'):"
Write-Host "       if (-not (Test-Path `$PROFILE)) { New-Item -ItemType File -Path `$PROFILE -Force }"
Write-Host "       Add-Content -Path `$PROFILE -Value 'Invoke-Expression (& nudge init pwsh | Out-String)'"
Write-Host "       . `$PROFILE"
Write-Host "  3. verify:"
Write-Host "       nudge doctor"

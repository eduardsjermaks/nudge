// Package shell generates the per-shell init snippets (the zoxide/starship
// pattern): a wrapper function `nudge` that shadows the binary so bare
// `nudge` can read the last command from shell history (shell state a
// standalone binary cannot see), a `fix` alias, and a command-not-found
// hook. The wrapper passes recent history via the NUDGE_HISTORY env var and
// evals whatever the binary prints on stdout — that is how shell-state
// suggestions (cd, venv activate) actually take effect.
package shell

import (
	"fmt"
	"os"
	"strings"
)

// Init returns the snippet for the given shell.
func Init(shellName string) (string, error) {
	switch strings.ToLower(shellName) {
	case "bash":
		return bashSnippet, nil
	case "zsh":
		return zshSnippet, nil
	case "fish":
		return fishSnippet, nil
	case "pwsh", "powershell":
		exe, err := os.Executable()
		if err != nil {
			exe = "nudge.exe"
		}
		return fmt.Sprintf(pwshSnippet, exe), nil
	default:
		return "", fmt.Errorf("unknown shell %q (supported: bash, zsh, fish, powershell)", shellName)
	}
}

const bashSnippet = `# nudge shell integration (bash) — add to ~/.bashrc:  eval "$(nudge init bash)"
nudge() {
  local __ec=$?
  case "$1" in
    init|doctor|setup|version|help|--version|-v|--help|-h)
      command nudge "$@"
      return ;;
  esac
  local __out
  if [ $# -gt 0 ]; then
    __out=$(command nudge --shell-eval --shell bash "$@")
  else
    __out=$(NUDGE_HISTORY="$(fc -ln -5 2>/dev/null)" command nudge --shell-eval --shell bash --last-exit "$__ec")
  fi
  if [ $? -eq 0 ] && [ -n "$__out" ]; then
    eval "$__out"
  fi
}
fix() { nudge "$@"; }
command_not_found_handle() {
  command nudge --not-found -- "$@"
}
`

const zshSnippet = `# nudge shell integration (zsh) — add to ~/.zshrc:  eval "$(nudge init zsh)"
nudge() {
  local __ec=$?
  case "$1" in
    init|doctor|setup|version|help|--version|-v|--help|-h)
      command nudge "$@"
      return ;;
  esac
  local __out
  if [ $# -gt 0 ]; then
    __out=$(command nudge --shell-eval --shell zsh "$@")
  else
    __out=$(NUDGE_HISTORY="$(fc -ln -5 2>/dev/null)" command nudge --shell-eval --shell zsh --last-exit "$__ec")
  fi
  if [ $? -eq 0 ] && [ -n "$__out" ]; then
    eval "$__out"
  fi
}
fix() { nudge "$@"; }
command_not_found_handler() {
  command nudge --not-found -- "$@"
}
`

const fishSnippet = `# nudge shell integration (fish) — add to ~/.config/fish/config.fish:  nudge init fish | source
function nudge
    set -l __ec $status
    if test (count $argv) -gt 0
        switch $argv[1]
            case init doctor setup version help --version -v --help -h
                command nudge $argv
                return
        end
        set -l __out (command nudge --shell-eval --shell fish $argv)
        if test $status -eq 0; and test -n "$__out"
            eval $__out
        end
        return
    end
    set -lx NUDGE_HISTORY (string join \n $history[1..5])
    set -l __out (command nudge --shell-eval --shell fish --last-exit $__ec)
    if test $status -eq 0; and test -n "$__out"
        eval $__out
    end
end
function fix
    nudge $argv
end
function fish_command_not_found
    # Runs in the fish process itself, so eval makes cd/env suggestions stick.
    set -l __out (command nudge --shell-eval --shell fish --not-found -- $argv)
    if test $status -eq 0; and test -n "$__out"
        eval $__out
    end
end
`

// %s is the absolute path of the nudge binary at init-generation time.
const pwshSnippet = `# nudge shell integration (PowerShell) — add to $PROFILE:
#   Invoke-Expression (& nudge init pwsh | Out-String)
$global:__nudgeBin = '%s'
function global:nudge {
    $ec = $global:LASTEXITCODE
    if ($args.Count -gt 0) {
        if ($args[0] -in @('init','doctor','setup','version','help','--version','-v','--help','-h')) {
            & $global:__nudgeBin @args
            return
        }
        $out = & $global:__nudgeBin --shell-eval --shell pwsh @args
        if ($LASTEXITCODE -eq 0 -and $out) {
            Invoke-Expression (@($out) -join "` + "`" + `n")
        }
        return
    }
    $hist = @(Get-History -Count 5 | ForEach-Object { $_.CommandLine })
    if ($hist.Count -eq 0) {
        Write-Host "nudge: no shell history yet" -ForegroundColor Yellow
        return
    }
    $env:NUDGE_HISTORY = ($hist -join "` + "`" + `n")
    try {
        $out = & $global:__nudgeBin --shell-eval --shell pwsh --last-exit "$ec"
        if ($LASTEXITCODE -eq 0 -and $out) {
            Invoke-Expression (@($out) -join "` + "`" + `n")
        }
    } finally {
        Remove-Item Env:\NUDGE_HISTORY -ErrorAction SilentlyContinue
    }
}
Set-Alias -Name fix -Value nudge -Scope Global -Force
$ExecutionContext.InvokeCommand.CommandNotFoundAction = {
    param($CommandName, $EventArgs)
    if ($EventArgs.CommandOrigin -eq 'Runspace' -and $CommandName -notmatch '^get-') {
        # The scriptblock runs in the session itself, so eval'ing the
        # confirmed command here lets cd/env suggestions take effect.
        $EventArgs.CommandScriptBlock = {
            $out = & $global:__nudgeBin --shell-eval --shell pwsh --not-found -- $CommandName @args
            if ($LASTEXITCODE -eq 0 -and $out) {
                Invoke-Expression (@($out) -join "` + "`" + `n")
            }
        }.GetNewClosure()
        $EventArgs.StopSearch = $true
    }
}
`

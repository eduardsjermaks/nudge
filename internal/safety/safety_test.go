package safety

import "testing"

func TestDestructive(t *testing.T) {
	destructive := []string{
		"rm -rf /tmp/x",
		"rm -fr node_modules",
		"git reset --hard HEAD~1",
		"git push --force origin main",
		"git push -f",
		"git clean -fd",
		"git checkout -- .",
		"docker system prune -a",
		"docker image prune",
		"kubectl delete pod mypod",
		"dd if=/dev/zero of=/dev/sda",
		"mkfs.ext4 /dev/sdb1",
		"format c:",
		"del /s /q C:\\temp",
		"rd /s /q build",
		"Remove-Item -Recurse -Force .\\build",
		"remove-item foo -force",
		"dotnet ef database drop",
		"DROP TABLE users",
		"psql -c 'truncate table logs'",
		"echo hi > existing.txt",
		":(){ :|:& };:",
		"git fetch --prune",
		"redis-cli flushall",
	}
	for _, c := range destructive {
		if v := Check(c, false); !v.Destructive {
			t.Errorf("Check(%q) should be destructive", c)
		}
	}

	safe := []string{
		"git push",
		"git push origin main",
		"git reset --soft HEAD~1",
		"dotnet ef migrations add AddOrders",
		"docker rmi alpine",
		"docker ps -a",
		"rm notes.txt", // plain rm of one file: not flagged (no -r/-f)
		"echo hi >> log.txt",
		"kubectl get pods 2> err.log",
		"npm install --force", // npm --force is annoying but not data loss; not in rules
		"ls -la",
		"git status",
	}
	for _, c := range safe {
		if v := Check(c, false); v.Destructive {
			t.Errorf("Check(%q) flagged destructive (%s), should be safe", c, v.Reason)
		}
	}

	// the model's hint escalates, never de-escalates
	if v := Check("some-weird-tool --wipe", true); !v.Destructive {
		t.Error("model hint should escalate")
	}
	if v := Check("rm -rf /", false); !v.Destructive {
		t.Error("local detector must win even when the model says safe")
	}
}

func TestChangesShellState(t *testing.T) {
	stateful := []string{
		"cd ..",
		"export PATH=$PATH:/opt/bin",
		"source .venv/bin/activate",
		". .venv/bin/activate",
		".venv\\Scripts\\Activate.ps1",
		"conda activate myenv",
		"$env:FOO = 'bar'",
		"pushd /tmp",
	}
	knownExe := func(w string) bool {
		switch w {
		case "git", "dotnet", "docker":
			return true
		}
		return false
	}
	for _, c := range stateful {
		if !ChangesShellState(c, false, knownExe) {
			t.Errorf("ChangesShellState(%q) should be true", c)
		}
	}
	stateless := []string{
		"git push",
		"dotnet build",
		"docker ps",
	}
	for _, c := range stateless {
		if ChangesShellState(c, false, knownExe) {
			t.Errorf("ChangesShellState(%q) should be false", c)
		}
	}
	// the model hint is only trusted when we have no local evidence
	if !ChangesShellState("mystery-tool xyz", true, knownExe) {
		t.Error("model shell_state hint should be honored for unknown commands")
	}
	if ChangesShellState("git push", true, knownExe) {
		t.Error("model shell_state hint must not override a known executable")
	}
}

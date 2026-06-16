package cmd

import (
	"path/filepath"
	"testing"
)

func resetCLIState(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("COPILOT_HOME", filepath.Join(home, ".copilot"))
	t.Setenv("KIMI_CODE_HOME", filepath.Join(home, ".kimi-code"))
	rootCmd.SetArgs(nil)
	doctorAgent = ""
	initDryRun = false
	installAgent = ""
	installAll = false
	installDryRun = false
	uninstallAgent = ""
	uninstallAll = false
	uninstallDryRun = false
	serveHTTP = ""
	serveHTTPToken = ""
	serveHTTPMaxBodyMB = 10
	t.Cleanup(resetCLIStateNoTest)
}

func resetCLIStateNoTest() {
	rootCmd.SetArgs(nil)
	doctorAgent = ""
	initDryRun = false
	installAgent = ""
	installAll = false
	installDryRun = false
	uninstallAgent = ""
	uninstallAll = false
	uninstallDryRun = false
	serveHTTP = ""
	serveHTTPToken = ""
	serveHTTPMaxBodyMB = 10
}

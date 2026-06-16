package cmd

import "testing"

func resetCLIState(t *testing.T) {
	t.Helper()
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

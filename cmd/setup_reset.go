/*
Copyright © 2026 OpenMANET

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
package cmd

import (
	"fmt"
	"os"

	"github.com/openmanet/openmanetd/internal/config"
	"github.com/openmanet/openmanetd/internal/network"
	"github.com/spf13/cobra"
)

// setupResetCmd unbricks a device whose setup wizard ran but ended
// up in an unreachable state. Flipping setup.complete back to false
// lets the wizard re-run from a console / serial / recovery shell,
// and flipping auth.enable to false makes the wizard reachable
// without a session.
//
// This is the recovery path documented in
// docs/setup-wizard-recovery.md: when a device is reachable via
// console but not over the network after a wizard run, the operator
// runs `openmanetd setup-reset` to re-open the wizard for a second
// attempt.
var setupResetCmd = &cobra.Command{ //nolint:gochecknoglobals
	Use:   "setup-reset",
	Short: "Reset the setup wizard so it can be re-run",
	Long: `Reset the setup wizard's completion flag so the wizard can be re-run.

This is a recovery command for devices whose first wizard run left them in an
unreachable state (e.g. a reload failed and the device cannot be reached on
its new SSID/IP). It flips two flags in /etc/openmanetd/config.yml and one UCI flag:

  setup.complete   = false   # the wizard becomes reachable again
  auth.enable      = false   # session auth is disabled so the wizard can run
                             #   without a login
  luci.wizard.used = 0       # both the LuCI and the Go wizard set this to 1
                             #   on completion and refuse to re-run while set

After running this command, restart openmanetd (typically via
'/etc/init.d/openmanetd restart') and reconnect to the wizard URL.

Use only via console/serial/recovery — running this on a working device
disables auth and reopens the wizard, which would reset all UCI state on
the next wizard run.`,
	Run: runSetupReset,
}

func init() {
	rootCmd.AddCommand(setupResetCmd)
}

func runSetupReset(cmd *cobra.Command, _ []string) {
	cfg := config.New(nil)

	// Flip both flags atomically in a single yaml read-modify-write,
	// matching the wizard's PersistSetupAndAuth path. Both go to
	// false here; the wizard's completion path flips them both to
	// true.
	if err := cfg.PersistSetupAndAuth(false, false); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "setup-reset failed: %v\n", err)
		os.Exit(1)
	}

	if err := network.ClearLuciWizardUsedWithReader(network.NewUCINetworkConfigReader()); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "setup-reset: cleared config.yml flags but failed to clear luci.wizard.used: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintln(cmd.OutOrStdout(),
		"setup-reset: setup.complete=false, auth.enable=false, luci.wizard.used=0")
	fmt.Fprintln(cmd.OutOrStdout(),
		"Restart openmanetd to reload the configuration:")
	fmt.Fprintln(cmd.OutOrStdout(),
		"  /etc/init.d/openmanetd restart")
}

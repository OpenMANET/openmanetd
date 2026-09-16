package network

import (
	"fmt"

	"github.com/digineo/go-uci/v2"
)

const (
	luciConfigName       = "luci"
	luciWizardSection    = "wizard"
	luciWizardUsedOption = "used"
)

// ClearLuciWizardUsedWithReader sets luci.wizard.used=0 and commits,
// re-arming both the LuCI and the Go setup wizard (each writes the
// flag to 1 on completion and refuses to run while it is set). Images
// without /etc/config/luci, or without the flag, are left untouched —
// nothing to re-arm.
func ClearLuciWizardUsedWithReader(reader ConfigReader) error {
	if _, ok := reader.Get(luciConfigName, luciWizardSection, luciWizardUsedOption); !ok {
		return nil
	}

	if err := reader.SetType(luciConfigName, luciWizardSection, luciWizardUsedOption, uci.TypeOption, "0"); err != nil {
		return fmt.Errorf("set luci.wizard.used=0: %w", err)
	}

	if err := reader.Commit(); err != nil {
		return fmt.Errorf("commit luci: %w", err)
	}

	return nil
}

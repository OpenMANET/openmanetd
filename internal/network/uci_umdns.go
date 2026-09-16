package network

import (
	"errors"
	"fmt"
	"os"

	"github.com/digineo/go-uci/v2"
)

const umdnsConfigName = "umdns"

// StageUmdnsNetworksWithReader writes the interface list umdns
// advertises on (umdns.@umdns[0].network), creating the section when
// absent. Without this, umdns never announces the device and
// <hostname>.local never resolves, even though the setup wizard's
// terminal event promises exactly that URL.
//
// /etc/config/umdns arrives in three shapes: a section from a prior
// run, the zero-byte file factory images ship, or no file at all
// (images without the umdns package). go-uci's GetSections surfaces
// the last one as os.ErrNotExist; it is treated as "no sections" so
// AddSection creates the config in memory and the wizard's phase-12
// commit materializes the file. Any other load failure (parse error,
// permissions) still propagates — go-uci's AddSection nil-derefs on
// those, and they are real faults an operator must see.
//
// The section is named (wizard_umdns): go-uci's AddSection("")
// collides with leading anonymous sections. Does not commit; umdns is
// in wizardConfigs so the atomic commit and rollback both cover it.
func StageUmdnsNetworksWithReader(reader ConfigReader, networks []string) error {
	if len(networks) == 0 {
		return fmt.Errorf("networks are required")
	}

	sections, err := reader.GetSections(umdnsConfigName, umdnsConfigName)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("listing umdns sections: %w", err)
	}

	if len(sections) == 0 {
		const section = "wizard_umdns"
		if err := reader.AddSection(umdnsConfigName, section, umdnsConfigName); err != nil {
			return fmt.Errorf("creating umdns section: %w", err)
		}

		sections = []string{section}
	}

	if err := reader.SetType(umdnsConfigName, sections[0], "network", uci.TypeList, networks...); err != nil {
		return fmt.Errorf("setting umdns network list: %w", err)
	}

	return nil
}

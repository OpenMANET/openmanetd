package board

// newBoardConfigInfoFn is the function used to retrieve board configuration. It can be
// overridden in tests to inject a fake board configuration.
var newBoardConfigInfoFn = NewBoardConfigInfo //nolint:gochecknoglobals // test injection point

// GNSSsupoorted returns true if the current board supports GNSS (Global Navigation Satellite System).
// It retrieves the board configuration information and checks the board model ID against
// a list of known supported and unsupported models.
//
// The following board models support GNSS:
//   - BCM2711_MM6108_SPI
//   - BCM2711_MM6108_SDIO
//   - BCM2711_MM8108_USB
//   - BCM2710_MM6108_SPI
//   - BCM2710_MM6108_SDIO
//   - GW7100_2
//   - GW7200_2
//   - GW7300_2
//   - GW7400
//   - GW7500_2
//   - GW7904
//   - GW7905_2
//   - BCM2711_RAVEN_USB
//
// Returns false if the board configuration cannot be retrieved, if the board model
// is known to not support GNSS (e.g. HalowLink2, HeltecHD01V2, GW7500_0, GW7905_0),
// or if the board model is unknown.
func GNSSsupoorted() bool {
	boardConfigInfo, err := newBoardConfigInfoFn()
	if err != nil {
		return false
	}

	switch boardConfigInfo.Model.ID {
	case BCM2711_MM6108_SPI,
		BCM2711_MM6108_SDIO,
		BCM2711_MM8108_USB:
		return true
	case BCM2710_MM6108_SPI, BCM2710_MM6108_SDIO:
		return true
	case HalowLink2, HeltecHD01V2:
		return false
	case GW7100_2, GW7200_2, GW7300_2, GW7400,
		GW7500_2, GW7904, GW7905_2:
		return true
	case GW7500_0, GW7905_0:
		return false
	case BCM2711_RAVEN_USB:
		return true
	default:
		return false
	}
}

// BLOSsupported returns true if the current board supports BLOS (Beyond Line of Sight).
// It retrieves the board configuration information and checks the board model ID against
// a list of known supported and unsupported models.
//
// The following board models support BLOS:
//   - BCM2711_MM6108_SPI
//   - BCM2711_MM6108_SDIO
//   - BCM2711_MM8108_USB
//   - GW7100_2
//   - GW7200_2
//   - GW7300_2
//   - GW7400
//   - GW7904
//   - GW7905_2
//   - BCM2711_RAVEN_USB
//
// Returns false if the board configuration cannot be retrieved, if the board model
// is known to not support BLOS (e.g. BCM2710_MM6108_SPI, BCM2710_MM6108_SDIO, HeltecHD01V2),
// or if the board model is unknown.
func BLOSsupported() bool {
	boardConfigInfo, err := newBoardConfigInfoFn()
	if err != nil {
		return false
	}

	switch boardConfigInfo.Model.ID {
	case BCM2711_MM6108_SPI,
		BCM2711_MM6108_SDIO,
		BCM2711_MM8108_USB:
		return true
	case BCM2710_MM6108_SPI, BCM2710_MM6108_SDIO:
		return false
	case HalowLink2:
		return true
	case HeltecHD01V2:
		return false
	case GW7100_2, GW7200_2, GW7300_2, GW7400,
		GW7904, GW7905_2:
		return true
	case BCM2711_RAVEN_USB:
		return true
	default:
		return false
	}
}

// CommsSupported returns true if the current board supports Comms (voice/radio communications).
// It retrieves the board configuration information and checks the board model ID against
// a list of known supported and unsupported models.
//
// The following board models support Comms:
//   - BCM2711_MM6108_SPI
//   - BCM2711_MM6108_SDIO
//   - BCM2711_MM8108_USB
//   - BCM2710_MM6108_SPI
//   - BCM2710_MM6108_SDIO
//   - HalowLink2
//   - GW7100_2
//   - GW7200_2
//   - GW7300_2
//   - GW7400
//   - GW7500_0
//   - GW7500_2
//   - GW7904
//   - GW7905_0
//   - GW7905_2
//   - BCM2711_RAVEN_USB
//
// Returns false if the board configuration cannot be retrieved, if the board model
// is known to not support Comms (e.g. HeltecHD01V2), or if the board model is unknown.
func CommsSupported() bool {
	boardConfigInfo, err := newBoardConfigInfoFn()
	if err != nil {
		return false
	}

	switch boardConfigInfo.Model.ID {
	case BCM2711_MM6108_SPI,
		BCM2711_MM6108_SDIO,
		BCM2711_MM8108_USB,
		BCM2710_MM6108_SPI,
		BCM2710_MM6108_SDIO,
		HalowLink2,
		GW7100_2, GW7200_2, GW7300_2, GW7400,
		GW7500_0, GW7500_2, GW7904, GW7905_0, GW7905_2,
		BCM2711_RAVEN_USB:
		return true

	case HeltecHD01V2:
		return false
	default:
		return false
	}
}

// GPIOSelectorSupported returns true if the current board wires the
// 5-position talk group selector to host GPIO lines.
//
// The following board models support the GPIO selector:
//   - BCM2711_RAVEN_USB
//
// Returns false if the board configuration cannot be retrieved or for
// every other board model.
func GPIOSelectorSupported() bool {
	boardConfigInfo, err := newBoardConfigInfoFn()
	if err != nil {
		return false
	}

	switch boardConfigInfo.Model.ID {
	case BCM2711_RAVEN_USB:
		return true
	default:
		return false
	}
}

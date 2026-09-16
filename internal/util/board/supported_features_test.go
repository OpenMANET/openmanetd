package board

import (
	"errors"
	"testing"
)

func TestGNSSsupoorted(t *testing.T) {
	tests := []struct {
		name    string
		modelID string
		want    bool
	}{
		// Supported models
		{name: "BCM2711_MM6108_SPI", modelID: BCM2711_MM6108_SPI, want: true},
		{name: "BCM2711_MM6108_SDIO", modelID: BCM2711_MM6108_SDIO, want: true},
		{name: "BCM2711_MM8108_USB", modelID: BCM2711_MM8108_USB, want: true},
		{name: "BCM2710_MM6108_SPI", modelID: BCM2710_MM6108_SPI, want: true},
		{name: "BCM2710_MM6108_SDIO", modelID: BCM2710_MM6108_SDIO, want: true},
		{name: "GW7100_2", modelID: GW7100_2, want: true},
		{name: "GW7200_2", modelID: GW7200_2, want: true},
		{name: "GW7300_2", modelID: GW7300_2, want: true},
		{name: "GW7400", modelID: GW7400, want: true},
		{name: "GW7500_2", modelID: GW7500_2, want: true},
		{name: "GW7904", modelID: GW7904, want: true},
		{name: "GW7905_2", modelID: GW7905_2, want: true},
		{name: "BCM2711_RAVEN_USB", modelID: BCM2711_RAVEN_USB, want: true},
		// Unsupported models
		{name: "HalowLink2", modelID: HalowLink2, want: false},
		{name: "HeltecHD01V2", modelID: HeltecHD01V2, want: false},
		{name: "GW7500_0", modelID: GW7500_0, want: false},
		{name: "GW7905_0", modelID: GW7905_0, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origNewBoardConfigInfo := newBoardConfigInfoFn

			defer func() { newBoardConfigInfoFn = origNewBoardConfigInfo }()

			newBoardConfigInfoFn = func() (*Board, error) {
				return &Board{
					Model: Model{ID: tt.modelID},
				}, nil
			}

			got := GNSSsupoorted()
			if got != tt.want {
				t.Errorf("GNSSsupoorted() for model %v = %v, want %v", tt.modelID, got, tt.want)
			}
		})
	}
}

func TestGNSSsupoorted_BoardConfigError(t *testing.T) {
	origNewBoardConfigInfo := newBoardConfigInfoFn

	defer func() { newBoardConfigInfoFn = origNewBoardConfigInfo }()

	newBoardConfigInfoFn = func() (*Board, error) {
		return nil, errors.New("board config not available")
	}

	got := GNSSsupoorted()
	if got != false {
		t.Errorf("GNSSsupoorted() with config error = %v, want false", got)
	}
}

func TestGNSSsupoorted_UnknownModel(t *testing.T) {
	origNewBoardConfigInfo := newBoardConfigInfoFn

	defer func() { newBoardConfigInfoFn = origNewBoardConfigInfo }()

	newBoardConfigInfoFn = func() (*Board, error) {
		return &Board{
			Model: Model{ID: "unknown-board-xyz"},
		}, nil
	}

	got := GNSSsupoorted()
	if got != false {
		t.Errorf("GNSSsupoorted() with unknown model = %v, want false", got)
	}
}

func TestBLOSsupported(t *testing.T) {
	tests := []struct {
		name    string
		modelID string
		want    bool
	}{
		// Supported models
		{name: "BCM2711_MM6108_SPI", modelID: BCM2711_MM6108_SPI, want: true},
		{name: "BCM2711_MM6108_SDIO", modelID: BCM2711_MM6108_SDIO, want: true},
		{name: "BCM2711_MM8108_USB", modelID: BCM2711_MM8108_USB, want: true},
		{name: "GW7100_2", modelID: GW7100_2, want: true},
		{name: "GW7200_2", modelID: GW7200_2, want: true},
		{name: "GW7300_2", modelID: GW7300_2, want: true},
		{name: "GW7400", modelID: GW7400, want: true},
		{name: "GW7904", modelID: GW7904, want: true},
		{name: "GW7905_2", modelID: GW7905_2, want: true},
		{name: "HalowLink2", modelID: HalowLink2, want: true},
		{name: "BCM2711_RAVEN_USB", modelID: BCM2711_RAVEN_USB, want: true},
		// Unsupported models
		{name: "BCM2710_MM6108_SPI", modelID: BCM2710_MM6108_SPI, want: false},
		{name: "BCM2710_MM6108_SDIO", modelID: BCM2710_MM6108_SDIO, want: false},
		{name: "HeltecHD01V2", modelID: HeltecHD01V2, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origNewBoardConfigInfo := newBoardConfigInfoFn

			defer func() { newBoardConfigInfoFn = origNewBoardConfigInfo }()

			newBoardConfigInfoFn = func() (*Board, error) {
				return &Board{
					Model: Model{ID: tt.modelID},
				}, nil
			}

			got := BLOSsupported()
			if got != tt.want {
				t.Errorf("BLOSsupported() for model %v = %v, want %v", tt.modelID, got, tt.want)
			}
		})
	}
}

func TestBLOSsupported_BoardConfigError(t *testing.T) {
	origNewBoardConfigInfo := newBoardConfigInfoFn

	defer func() { newBoardConfigInfoFn = origNewBoardConfigInfo }()

	newBoardConfigInfoFn = func() (*Board, error) {
		return nil, errors.New("board config not available")
	}

	got := BLOSsupported()
	if got != false {
		t.Errorf("BLOSsupported() with config error = %v, want false", got)
	}
}

func TestBLOSsupported_UnknownModel(t *testing.T) {
	origNewBoardConfigInfo := newBoardConfigInfoFn

	defer func() { newBoardConfigInfoFn = origNewBoardConfigInfo }()

	newBoardConfigInfoFn = func() (*Board, error) {
		return &Board{
			Model: Model{ID: "unknown-board-xyz"},
		}, nil
	}

	got := BLOSsupported()
	if got != false {
		t.Errorf("BLOSsupported() with unknown model = %v, want false", got)
	}
}

func TestCommsSupported(t *testing.T) {
	tests := []struct {
		name    string
		modelID string
		want    bool
	}{
		// Supported models
		{name: "BCM2711_MM6108_SPI", modelID: BCM2711_MM6108_SPI, want: true},
		{name: "BCM2711_MM6108_SDIO", modelID: BCM2711_MM6108_SDIO, want: true},
		{name: "BCM2711_MM8108_USB", modelID: BCM2711_MM8108_USB, want: true},
		{name: "BCM2710_MM6108_SPI", modelID: BCM2710_MM6108_SPI, want: true},
		{name: "BCM2710_MM6108_SDIO", modelID: BCM2710_MM6108_SDIO, want: true},
		{name: "HalowLink2", modelID: HalowLink2, want: true},
		{name: "GW7100_2", modelID: GW7100_2, want: true},
		{name: "GW7200_2", modelID: GW7200_2, want: true},
		{name: "GW7300_2", modelID: GW7300_2, want: true},
		{name: "GW7400", modelID: GW7400, want: true},
		{name: "GW7500_0", modelID: GW7500_0, want: true},
		{name: "GW7500_2", modelID: GW7500_2, want: true},
		{name: "GW7904", modelID: GW7904, want: true},
		{name: "GW7905_0", modelID: GW7905_0, want: true},
		{name: "GW7905_2", modelID: GW7905_2, want: true},
		{name: "BCM2711_RAVEN_USB", modelID: BCM2711_RAVEN_USB, want: true},
		// Unsupported models
		{name: "HeltecHD01V2", modelID: HeltecHD01V2, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origNewBoardConfigInfo := newBoardConfigInfoFn

			defer func() { newBoardConfigInfoFn = origNewBoardConfigInfo }()

			newBoardConfigInfoFn = func() (*Board, error) {
				return &Board{
					Model: Model{ID: tt.modelID},
				}, nil
			}

			got := CommsSupported()
			if got != tt.want {
				t.Errorf("CommsSupported() for model %v = %v, want %v", tt.modelID, got, tt.want)
			}
		})
	}
}

func TestCommsSupported_BoardConfigError(t *testing.T) {
	origNewBoardConfigInfo := newBoardConfigInfoFn

	defer func() { newBoardConfigInfoFn = origNewBoardConfigInfo }()

	newBoardConfigInfoFn = func() (*Board, error) {
		return nil, errors.New("board config not available")
	}

	got := CommsSupported()
	if got != false {
		t.Errorf("CommsSupported() with config error = %v, want false", got)
	}
}

func TestCommsSupported_UnknownModel(t *testing.T) {
	origNewBoardConfigInfo := newBoardConfigInfoFn

	defer func() { newBoardConfigInfoFn = origNewBoardConfigInfo }()

	newBoardConfigInfoFn = func() (*Board, error) {
		return &Board{
			Model: Model{ID: "unknown-board-xyz"},
		}, nil
	}

	got := CommsSupported()
	if got != false {
		t.Errorf("CommsSupported() with unknown model = %v, want false", got)
	}
}

func TestGPIOSelectorSupported(t *testing.T) {
	tests := []struct {
		name    string
		modelID string
		want    bool
	}{
		{name: "raven", modelID: BCM2711_RAVEN_USB, want: true},
		{name: "mm6108 spi", modelID: BCM2711_MM6108_SPI, want: false},
		{name: "halowlink2", modelID: HalowLink2, want: false},
		{name: "unknown", modelID: "vendor,unknown", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origNewBoardConfigInfo := newBoardConfigInfoFn

			defer func() { newBoardConfigInfoFn = origNewBoardConfigInfo }()

			newBoardConfigInfoFn = func() (*Board, error) {
				return &Board{
					Model: Model{ID: tt.modelID},
				}, nil
			}

			got := GPIOSelectorSupported()
			if got != tt.want {
				t.Errorf("GPIOSelectorSupported() for model %v = %v, want %v", tt.modelID, got, tt.want)
			}
		})
	}
}

func TestGPIOSelectorSupported_BoardConfigError(t *testing.T) {
	origNewBoardConfigInfo := newBoardConfigInfoFn

	defer func() { newBoardConfigInfoFn = origNewBoardConfigInfo }()

	newBoardConfigInfoFn = func() (*Board, error) {
		return nil, errors.New("board config not available")
	}

	got := GPIOSelectorSupported()
	if got != false {
		t.Errorf("GPIOSelectorSupported() with config error = %v, want false", got)
	}
}

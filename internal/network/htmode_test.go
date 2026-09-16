package network

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHTModeBandwidthMHz(t *testing.T) {
	tests := []struct {
		htmode string
		want   uint32
		ok     bool
	}{
		{"1 MHz", 1, true},
		{"2 MHz", 2, true},
		{"4 MHz", 4, true},
		{"8 MHz", 8, true},
		{"8 mhz", 8, true},
		{"NOHT", 20, true},
		{"HT20", 20, true},
		{"VHT20", 20, true},
		{"HE20", 20, true},
		{"HT40", 40, true},
		{"HT40-", 40, true},
		{"HT40+", 40, true},
		{"VHT40", 40, true},
		{"HE40", 40, true},
		{"VHT80", 80, true},
		{"HE80", 80, true},
		{"VHT160", 160, true},
		{"HE160", 160, true},
		{"", 0, false},
		{"EHT320", 0, false},
	}

	for _, tc := range tests {
		t.Run(tc.htmode, func(t *testing.T) {
			got, ok := HTModeBandwidthMHz(tc.htmode)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSecondaryMeshHTMode(t *testing.T) {
	assert.Equal(t, "HE40", SecondaryMeshHTMode2G, "the secondary link defaults to 40 MHz HE")
	assert.Equal(t, SecondaryMeshHTMode2G, SecondaryMeshHTMode(0), "zero keeps the fixed default")
	assert.Equal(t, "HE20", SecondaryMeshHTMode(20), "20 MHz stays selectable for congested spectrum")
	assert.Equal(t, "HE40", SecondaryMeshHTMode(40))
	assert.Empty(t, SecondaryMeshHTMode(80), "only 20 and 40 MHz are 2.4 GHz widths")
	assert.Empty(t, SecondaryMeshHTMode(8))
}

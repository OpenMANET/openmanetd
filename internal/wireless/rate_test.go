package wireless_test

import (
	"math"
	"testing"

	"github.com/mdlayher/wifi"
	"github.com/openmanet/openmanetd/internal/wireless"
	"github.com/stretchr/testify/assert"
)

func heRate(bitrate int, width wifi.ChannelWidth, mcs, nss int) wifi.RateInfo {
	return wifi.RateInfo{
		Bitrate:        bitrate,
		ModulationType: wifi.RateModulationInfoTypeHE,
		Modulation:     wifi.HEModulationInfo{BaseModulationInfo: wifi.BaseModulationInfo{MCS: mcs, NSS: nss}},
		ChannelWidth:   width,
	}
}

func TestSummarizeRate(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		ri       wifi.RateInfo
		fallback int
		want     wireless.RateSummary
	}{
		"he40 mcs7 2ss": {
			ri:   heRate(86_700_000, wifi.ChannelWidth40, 7, 2),
			want: wireless.RateSummary{BitrateKbps: 86700, WidthMHz: 40, MCS: 7, NSS: 2, PHY: wireless.PHYHE},
		},
		"ht20 mcs7": {
			ri: wifi.RateInfo{
				Bitrate:        72_200_000,
				ModulationType: wifi.RateModulationInfoTypeHT,
				Modulation:     wifi.HTModulationInfo{BaseModulationInfo: wifi.BaseModulationInfo{MCS: 7, NSS: 1}, HTMCS: 7, ShortGI: true},
				ChannelWidth:   wifi.ChannelWidth20NoHT,
			},
			want: wireless.RateSummary{BitrateKbps: 72200, WidthMHz: 20, MCS: 7, NSS: 1, PHY: wireless.PHYHT},
		},
		"vht80 mcs9 2ss": {
			ri: wifi.RateInfo{
				Bitrate:        866_700_000,
				ModulationType: wifi.RateModulationInfoTypeVHT,
				Modulation:     wifi.VHTModulationInfo{BaseModulationInfo: wifi.BaseModulationInfo{MCS: 9, NSS: 2}},
				ChannelWidth:   wifi.ChannelWidth80,
			},
			want: wireless.RateSummary{BitrateKbps: 866700, WidthMHz: 80, MCS: 9, NSS: 2, PHY: wireless.PHYVHT},
		},
		"eht160 mcs13 1ss": {
			// Bitrate kept within a 32-bit int (5_764_700_000 overflows
			// int on mipsle/other 32-bit targets); EHT160 MCS13 1SS is
			// a real rate that still exercises the EHT branch.
			ri: wifi.RateInfo{
				Bitrate:        1_441_100_000,
				ModulationType: wifi.RateModulationInfoTypeEHT,
				Modulation:     wifi.EHTModulationInfo{BaseModulationInfo: wifi.BaseModulationInfo{MCS: 13, NSS: 1}},
				ChannelWidth:   wifi.ChannelWidth160,
			},
			want: wireless.RateSummary{BitrateKbps: 1_441_100, WidthMHz: 160, MCS: 13, NSS: 1, PHY: wireless.PHYEHT},
		},
		"legacy 54m has no mcs": {
			ri: wifi.RateInfo{
				Bitrate:        54_000_000,
				ModulationType: wifi.RateModulationInfoTypeLegacy,
				Modulation:     nil,
				ChannelWidth:   wifi.ChannelWidth20NoHT,
			},
			want: wireless.RateSummary{BitrateKbps: 54000, WidthMHz: 20, MCS: -1, NSS: -1, PHY: wireless.PHYLegacy},
		},
		"s1g 2mhz reports as ht mcs": {
			ri: wifi.RateInfo{
				Bitrate:        1_950_000,
				ModulationType: wifi.RateModulationInfoTypeHT,
				Modulation:     wifi.HTModulationInfo{BaseModulationInfo: wifi.BaseModulationInfo{MCS: 2, NSS: 1}, HTMCS: 2},
				ChannelWidth:   wifi.ChannelWidth2,
			},
			want: wireless.RateSummary{BitrateKbps: 1950, WidthMHz: 2, MCS: 2, NSS: 1, PHY: wireless.PHYHT},
		},
		"vht nss not reported stays -1": {
			ri: wifi.RateInfo{
				Bitrate:        433_300_000,
				ModulationType: wifi.RateModulationInfoTypeVHT,
				Modulation:     wifi.VHTModulationInfo{BaseModulationInfo: wifi.BaseModulationInfo{MCS: 9, NSS: -1}},
				ChannelWidth:   wifi.ChannelWidth80,
			},
			want: wireless.RateSummary{BitrateKbps: 433300, WidthMHz: 80, MCS: 9, NSS: -1, PHY: wireless.PHYVHT},
		},
		"unknown modulation type keeps bitrate and width": {
			ri: wifi.RateInfo{
				Bitrate:        6_000_000,
				ModulationType: wifi.RateModulationInfoTypeUNKNOWN,
				ChannelWidth:   wifi.ChannelWidth20NoHT,
			},
			want: wireless.RateSummary{BitrateKbps: 6000, WidthMHz: 20, MCS: -1, NSS: -1, PHY: wireless.PHYUnknown},
		},
		"no rate attributes falls back to the station bitrate": {
			ri:       wifi.RateInfo{},
			fallback: 54_000,
			want:     wireless.RateSummary{BitrateKbps: 54, WidthMHz: 0, MCS: -1, NSS: -1, PHY: wireless.PHYUnknown},
		},
		"no rate attributes and no fallback": {
			ri:   wifi.RateInfo{},
			want: wireless.RateSummary{BitrateKbps: 0, WidthMHz: 0, MCS: -1, NSS: -1, PHY: wireless.PHYUnknown},
		},
		"fallback saturates at int32": {
			ri:       wifi.RateInfo{},
			fallback: math.MaxInt,
			// On 64-bit, MaxInt/1000 exceeds MaxInt32 and kbps() clamps
			// to MaxInt32. On 32-bit, MaxInt == MaxInt32 and MaxInt/1000
			// never reaches the clamp, so the expectation must be
			// computed the same way kbps() computes it rather than
			// hardcoded to MaxInt32.
			want: wireless.RateSummary{BitrateKbps: min(math.MaxInt/1000, math.MaxInt32), WidthMHz: 0, MCS: -1, NSS: -1, PHY: wireless.PHYUnknown},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, wireless.SummarizeRate(tc.ri, tc.fallback))
		})
	}
}

func TestWidthMHz(t *testing.T) {
	t.Parallel()

	tests := map[wifi.ChannelWidth]int32{
		wifi.ChannelWidth20NoHT: 20,
		wifi.ChannelWidth20:     20,
		wifi.ChannelWidth40:     40,
		wifi.ChannelWidth80:     80,
		wifi.ChannelWidth80P80:  160,
		wifi.ChannelWidth160:    160,
		wifi.ChannelWidth320:    320,
		wifi.ChannelWidth5:      5,
		wifi.ChannelWidth10:     10,
		wifi.ChannelWidth1:      1,
		wifi.ChannelWidth2:      2,
		wifi.ChannelWidth4:      4,
		wifi.ChannelWidth8:      8,
		wifi.ChannelWidth16:     16,
		wifi.ChannelWidth(99):   0,
	}

	for w, want := range tests {
		t.Run(w.String(), func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, want, wireless.WidthMHz(w))
		})
	}
}

func TestPHY_String(t *testing.T) {
	t.Parallel()

	tests := map[wireless.PHY]string{
		wireless.PHYUnknown: "",
		wireless.PHYLegacy:  "legacy",
		wireless.PHYHT:      "ht",
		wireless.PHYVHT:     "vht",
		wireless.PHYHE:      "he",
		wireless.PHYEHT:     "eht",
		wireless.PHY(42):    "",
	}

	for p, want := range tests {
		assert.Equal(t, want, p.String())
	}
}

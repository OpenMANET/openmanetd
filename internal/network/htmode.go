package network

import "strings"

const (
	htModeHE20 = "HE20"
	htModeHE80 = "HE80"
)

// HTModeBandwidthMHz maps a UCI htmode string to its channel width in
// MHz. It is the inverse of the wizard's bandwidthToHTMode: S1G widths
// are LuCI's "1 MHz".."8 MHz" literals; HT/VHT/HE modes map to their
// numeric width. ok is false for an unknown or empty htmode.
func HTModeBandwidthMHz(htmode string) (uint32, bool) {
	switch strings.ToUpper(strings.TrimSpace(htmode)) {
	case "1 MHZ":
		return 1, true
	case "2 MHZ":
		return 2, true
	case "4 MHZ":
		return 4, true
	case "8 MHZ":
		return 8, true
	case "NOHT", "HT20", "VHT20", htModeHE20:
		return 20, true
	case "HT40", "HT40-", "HT40+", "VHT40", "HE40":
		return 40, true
	case "VHT80", htModeHE80:
		return 80, true
	case "VHT160", "HE160":
		return 160, true
	default:
		return 0, false
	}
}

// SecondaryMeshHTMode returns the htmode written to the 2.4 GHz radio
// that carries the secondary batman-adv link for the given width. 0
// keeps the fixed default (SecondaryMeshHTMode2G, 40 MHz); 20 and 40
// select the HE mode of that width, matching the MT7915/MT7916 chipsets
// the link is gated on. Any other width returns "".
func SecondaryMeshHTMode(bandwidthMHz uint32) string {
	switch bandwidthMHz {
	case 0, 40:
		return SecondaryMeshHTMode2G
	case 20:
		return htModeHE20
	default:
		return ""
	}
}

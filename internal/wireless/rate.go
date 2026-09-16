// Package wireless summarizes nl80211 station rate information from
// github.com/mdlayher/wifi and publishes it to the instrumentation
// snapshot registry as the "wireless" section.
package wireless

import (
	"math"

	"github.com/mdlayher/wifi"
)

// PHY is the modulation family of an nl80211 rate.
type PHY uint8

// PHY values, in the order nl80211 introduced the families. PHYUnknown
// means the driver sent no rate attributes (or an unrecognized type).
const (
	PHYUnknown PHY = iota
	PHYLegacy
	PHYHT
	PHYVHT
	PHYHE
	PHYEHT
)

// String returns the lowercase label used in the instrumentation
// snapshot; "" for PHYUnknown. It never allocates.
func (p PHY) String() string {
	switch p {
	case PHYLegacy:
		return "legacy"
	case PHYHT:
		return "ht"
	case PHYVHT:
		return "vht"
	case PHYHE:
		return "he"
	case PHYEHT:
		return "eht"
	case PHYUnknown:
		return ""
	default:
		return ""
	}
}

// unknownIndex is the MCS / NSS value when the driver reported none,
// matching mdlayher/wifi's own "not seen" sentinel.
const unknownIndex = -1

// RateSummary is one direction of a station rate in the units the API
// and the snapshot expose.
type RateSummary struct {
	BitrateKbps int32
	WidthMHz    int32
	MCS         int32 // unknownIndex when not reported
	NSS         int32 // unknownIndex when not reported
	PHY         PHY
}

// SummarizeRate maps a wifi.RateInfo. mdlayher/wifi's Linux parser
// scales RateInfo.Bitrate to bit/s (client_linux.go, parseRateInfo)
// and its zero ModulationType is HT, so a zero Bitrate is the only
// reliable sign that the driver sent no rate attributes; in that case
// fallbackBitrateBps (the plain StationInfo bitrate, bit/s) is used,
// the PHY is unknown and the width is 0.
func SummarizeRate(ri wifi.RateInfo, fallbackBitrateBps int) RateSummary {
	if ri.Bitrate == 0 {
		return RateSummary{
			BitrateKbps: kbps(fallbackBitrateBps),
			MCS:         unknownIndex,
			NSS:         unknownIndex,
			PHY:         PHYUnknown,
		}
	}

	sum := RateSummary{
		BitrateKbps: kbps(ri.Bitrate),
		WidthMHz:    WidthMHz(ri.ChannelWidth),
		MCS:         unknownIndex,
		NSS:         unknownIndex,
		PHY:         phyOf(ri.ModulationType),
	}

	if ri.Modulation != nil {
		sum.MCS = int32(ri.Modulation.GetMCS())
		sum.NSS = int32(ri.Modulation.GetNSS())
	}

	return sum
}

// kbps converts bit/s to kbit/s, saturating at math.MaxInt32 and
// clamping negatives to 0.
func kbps(bps int) int32 {
	if bps <= 0 {
		return 0
	}

	v := bps / 1000
	if v > math.MaxInt32 {
		return math.MaxInt32
	}

	return int32(v)
}

// phyOf maps mdlayher/wifi's modulation type to a PHY.
func phyOf(t wifi.RateModulationInfoType) PHY {
	switch t {
	case wifi.RateModulationInfoTypeLegacy:
		return PHYLegacy
	case wifi.RateModulationInfoTypeHT:
		return PHYHT
	case wifi.RateModulationInfoTypeVHT:
		return PHYVHT
	case wifi.RateModulationInfoTypeHE:
		return PHYHE
	case wifi.RateModulationInfoTypeEHT:
		return PHYEHT
	case wifi.RateModulationInfoTypeUNKNOWN:
		return PHYUnknown
	default:
		return PHYUnknown
	}
}

// WidthMHz maps wifi.ChannelWidth to MHz. 80+80 reports 160 (the
// occupied spectrum); unknown values report 0.
func WidthMHz(w wifi.ChannelWidth) int32 {
	switch w {
	case wifi.ChannelWidth20NoHT, wifi.ChannelWidth20:
		return 20
	case wifi.ChannelWidth40:
		return 40
	case wifi.ChannelWidth80:
		return 80
	case wifi.ChannelWidth80P80, wifi.ChannelWidth160:
		return 160
	case wifi.ChannelWidth320:
		return 320
	case wifi.ChannelWidth5:
		return 5
	case wifi.ChannelWidth10:
		return 10
	case wifi.ChannelWidth1:
		return 1
	case wifi.ChannelWidth2:
		return 2
	case wifi.ChannelWidth4:
		return 4
	case wifi.ChannelWidth8:
		return 8
	case wifi.ChannelWidth16:
		return 16
	default:
		return 0
	}
}

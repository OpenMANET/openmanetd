package network

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openmanet/openmanetd/internal/iwinfo"
)

// WirelessRadioStatus holds the runtime state for a single UCI radio device.
type WirelessRadioStatus struct {
	Interfaces []WirelessRadioInterface `json:"interfaces"`
	Up         bool                     `json:"up"`
	Disabled   bool                     `json:"disabled"`
}

// WirelessRadioInterface represents one interface running on a radio.
type WirelessRadioInterface struct {
	Section string                    `json:"section"`
	Ifname  string                    `json:"ifname"`
	Config  WirelessIfaceStatusConfig `json:"config"`
}

// ResolveWirelessRadioHardwareName correlates a UCI radio section with its
// runtime interfaces and returns the first hardware name reported by iwinfo.
func ResolveWirelessRadioHardwareName(
	radioName string,
	status map[string]*WirelessRadioStatus,
	iwinfoData map[string]*iwinfo.InterfaceInfo,
) string {
	radio, ok := status[radioName]
	if !ok || radio == nil {
		return ""
	}

	for _, iface := range radio.Interfaces {
		info, ok := iwinfoData[iface.Ifname]
		if !ok || info == nil {
			continue
		}

		if hardwareName := info.GetHardwareName(); hardwareName != "" {
			return hardwareName
		}
	}

	return ""
}

// WirelessIfaceStatusConfig is the nested config block inside the ubus response.
type WirelessIfaceStatusConfig struct {
	Mode string `json:"mode"`
}

// WirelessStatusProvider retrieves the runtime mapping from UCI radio names
// to Linux interface names via ubus.
type WirelessStatusProvider interface {
	GetWirelessStatus(ctx context.Context) (map[string]*WirelessRadioStatus, error)
}

// UbusWirelessStatusProvider implements WirelessStatusProvider by calling
// `ubus call network.wireless status`.
type UbusWirelessStatusProvider struct {
	exec iwinfo.UbusExecutor
}

// NewUbusWirelessStatusProvider returns a provider backed by the given executor.
func NewUbusWirelessStatusProvider(exec iwinfo.UbusExecutor) *UbusWirelessStatusProvider {
	return &UbusWirelessStatusProvider{exec: exec}
}

// NewDefaultWirelessStatusProvider returns a provider using the real ubus binary.
func NewDefaultWirelessStatusProvider() *UbusWirelessStatusProvider {
	return &UbusWirelessStatusProvider{exec: &iwinfo.DefaultUbusExecutor{}}
}

// GetWirelessStatus calls `ubus call network.wireless status` and returns the
// result keyed by UCI radio device name (e.g. "radio0", "radio2").
func (p *UbusWirelessStatusProvider) GetWirelessStatus(ctx context.Context) (map[string]*WirelessRadioStatus, error) {
	out, err := p.exec.Execute(ctx, "call", "network.wireless", "status")
	if err != nil {
		return nil, fmt.Errorf("network.wireless status: %w", err)
	}

	var result map[string]*WirelessRadioStatus
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("parse network.wireless status: %w", err)
	}

	return result, nil
}

// SupportsSecondaryMeshLink reports whether an iwinfo hardware name
// names a 2.4 GHz chipset the daemon runs a secondary batman-adv mesh
// link on. The daemon's boot-time fallback and the wizard's capability
// flag must agree, so both call this.
func SupportsSecondaryMeshLink(hardwareName string) bool {
	return strings.Contains(hardwareName, "MT7915") || strings.Contains(hardwareName, "MT7916")
}

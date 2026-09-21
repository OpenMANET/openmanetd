// This investigation probe writes only temporary files. It demonstrates the
// preservation of external edits, with and without an explicit caller refresh.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/digineo/go-uci/v2"
	wificonfigv1 "github.com/openmanet/openmanetd/internal/api/openmanet/wifi_config/v1"
	"github.com/openmanet/openmanetd/internal/openmanet/server/handlers"
	"github.com/rs/zerolog"
)

type reader struct{ uci.Tree }

func (r reader) ReloadConfig() error { return r.LoadConfig("wireless", true) }

func run(refresh bool) error {
	dir, err := os.MkdirTemp("", "wireless-cache-probe-")
	if err != nil {
		return fmt.Errorf("wireless cache probe: %w", err)
	}
	defer os.RemoveAll(dir)

	initial := `config wifi-device 'radio0'
 option type 'mac80211'
 option band '5g'
 option channel '36'
config wifi-iface 'default_radio0'
 option device 'radio0'
 option mode 'ap'
 option network 'ahwlan'
 option ssid 'test-ap'
config wifi-device 'radio1'
 option type 'morse'
 option band 's1g'
config wifi-iface 'default_radio1'
 option device 'radio1'
 option mode 'mesh'
 option network 'ahwlan'
 option mesh_id 'test-mesh'
`
	if err = os.WriteFile(filepath.Join(dir, "wireless"), []byte(initial), 0600); err != nil {
		return fmt.Errorf("wireless cache probe: %w", err)
	}

	cached := reader{uci.NewTree(dir)}
	if err = cached.LoadConfig("wireless", false); err != nil {
		return fmt.Errorf("wireless cache probe: %w", err)
	}
	// A separate writer represents the LuCI wizard or an operator's UCI repair.
	external := uci.NewTree(dir)
	if err = external.SetType("wireless", "default_radio1", "network", uci.TypeOption, "batmesh0"); err != nil {
		return fmt.Errorf("wireless cache probe: %w", err)
	}

	if err = external.Commit(); err != nil {
		return fmt.Errorf("wireless cache probe: %w", err)
	}

	if refresh {
		if err = cached.ReloadConfig(); err != nil {
			return fmt.Errorf("wireless cache probe: %w", err)
		}
	}

	svc := handlers.WifiConfigService{Log: zerolog.Nop(), ConfigReader: cached, ReloadServices: func(context.Context) error { return nil }}

	response, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{
		RadioName: "radio0", Settings: &wificonfigv1.RadioSettings{Ssid: "test-ap", Channel: "6", Mode: wificonfigv1.WifiMode_WIFI_MODE_AP},
	})
	if err != nil {
		return fmt.Errorf("wireless cache probe: %w", err)
	}

	if !response.GetSuccess() {
		return fmt.Errorf("update: %s", response.GetMessage())
	}

	disk := uci.NewTree(dir)
	got, _ := disk.Get("wireless", "default_radio1", "network")

	want := "batmesh0"

	if len(got) != 1 || got[0] != want {
		return fmt.Errorf("refresh=%v: got %v, expected %s", refresh, got, want)
	}

	fmt.Printf("refresh-before-write=%v: AP-only RPC leaves HaLow network=%s\n", refresh, got[0])

	return nil
}
func main() {
	for _, refresh := range []bool{false, true} {
		if err := run(refresh); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}

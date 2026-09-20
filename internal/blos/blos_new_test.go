package blos

import (
	"errors"
	"testing"
	"time"

	batmanadv "github.com/openmanet/openmanetd/internal/batman-adv"
	"github.com/openmanet/openmanetd/internal/config"
	"github.com/rs/zerolog"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func meshConfigReturning(gwMode string) meshConfigFunc {
	return func(_ string) (*batmanadv.MeshConfig, error) {
		return &batmanadv.MeshConfig{GwMode: gwMode}, nil
	}
}

// TestNewBLOS_gatewayModeSharesOneLocalAPIClient pins the fd-leak fix at the
// construction site: the prefs client and the status worker must share the
// same LocalTailscaleClient so the daemon holds one connection to tailscaled.
func TestNewBLOS_gatewayModeSharesOneLocalAPIClient(t *testing.T) {
	v := viper.New()
	v.Set("BLOS.statusWorkerInterval", 7)
	cfg := config.NewWithoutWatch(v)

	b, err := newBLOS(cfg, zerolog.Nop(), meshConfigReturning(batmanadv.GwModeServer))
	require.NoError(t, err)
	require.NotNil(t, b)
	require.NotNil(t, b.statusWorker)

	tsClient, ok := b.tsClient.(*LocalTailscaleClient)
	require.True(t, ok, "tsClient should be the production LocalTailscaleClient")

	assert.Same(t, tsClient, b.statusWorker.client, "status worker must reuse the prefs client")
	assert.Equal(t, 7*time.Second, b.statusWorker.interval)
	assert.Same(t, cfg, b.cfg)
	assert.NotNil(t, b.uciOpenManetConfig)
	assert.NotNil(t, b.uciNetworkConfig)
	assert.NotNil(t, b.uciFirewallConfig)
	assert.IsType(t, &RealInterfaceManager{}, b.interfaceManager)
}

func TestNewBLOS_notGatewayMode(t *testing.T) {
	for _, mode := range []string{batmanadv.GwModeClient, batmanadv.GwModeOff, ""} {
		t.Run("gw_mode="+mode, func(t *testing.T) {
			b, err := newBLOS(config.NewWithoutWatch(viper.New()), zerolog.Nop(), meshConfigReturning(mode))
			require.NoError(t, err)
			assert.Nil(t, b, "BLOS must not be created outside gateway mode")
		})
	}
}

func TestNewBLOS_meshConfigError(t *testing.T) {
	wantErr := errors.New("batctl mj: exec: not found")

	var gotIface string

	b, err := newBLOS(config.NewWithoutWatch(viper.New()), zerolog.Nop(), func(iface string) (*batmanadv.MeshConfig, error) {
		gotIface = iface

		return nil, wantErr
	})
	require.ErrorIs(t, err, wantErr)
	assert.Nil(t, b)
	assert.Equal(t, config.NewWithoutWatch(viper.New()).GetAlfredBatInterface(), gotIface, "mesh config must be read for the configured bat interface")
}

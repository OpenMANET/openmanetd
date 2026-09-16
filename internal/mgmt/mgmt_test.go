package mgmt

import (
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewManager_CopiesForceflood pins ledger finding F5: NewManager used
// to drop BatmanMulticastForceflood on the floor, so
// configureBatmanForceflood always saw the zero value no matter what
// batman.multicastForceflood said. NewManager tolerates a missing
// /etc/board.json and an unavailable nl80211 socket (both are logged, not
// returned), so it runs in CI.
func TestNewManager_CopiesForceflood(t *testing.T) {
	tests := []struct {
		name       string
		forceflood bool
	}{
		{name: "true", forceflood: true},
		{name: "false", forceflood: false},
	}

	for _, tc := range tests {
		t.Run("forceflood="+tc.name, func(t *testing.T) {
			m, err := NewManager(ManagementConfig{
				Log:                       zerolog.Nop(),
				BatInterface:              "bat0",
				BatmanMulticastForceflood: tc.forceflood,
			})
			require.NoError(t, err)

			if m.WirelessConfig != nil {
				t.Cleanup(func() { _ = m.WirelessConfig.Close() })
			}

			assert.Equal(t, tc.forceflood, m.BatmanMulticastForceflood,
				"NewManager must copy BatmanMulticastForceflood")
		})
	}
}

// TestNewManager_CopiesNodeExpiry guards against the ledger-F5 class of
// bug (a field passed in but dropped by NewManager).
func TestNewManager_CopiesNodeExpiry(t *testing.T) {
	m, err := NewManager(ManagementConfig{
		Log:          zerolog.Nop(),
		BatInterface: "bat0",
		NodeExpiry:   36 * time.Hour,
	})
	require.NoError(t, err)

	if m.WirelessConfig != nil {
		t.Cleanup(func() { _ = m.WirelessConfig.Close() })
	}

	assert.Equal(t, 36*time.Hour, m.NodeExpiry, "NewManager must copy NodeExpiry")
}

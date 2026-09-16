package openmanet

import (
	"testing"

	"github.com/openmanet/openmanetd/internal/mgmt"
	"github.com/openmanet/openmanetd/internal/openmanet/server/handlers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildWifiProvider_NilManager(t *testing.T) {
	t.Parallel()

	assert.Nil(t, buildWifiProvider(nil))
}

func TestBuildWifiProvider_NilWirelessConfig(t *testing.T) {
	t.Parallel()

	// mgmt.NewManager keeps running when nl80211 init fails, leaving
	// WirelessConfig nil; wrapping that nil would panic on first use.
	assert.Nil(t, buildWifiProvider(&mgmt.ManagementConfig{}))
}

func TestBuildWifiProvider_WrapsWirelessConfig(t *testing.T) {
	t.Parallel()

	p := buildWifiProvider(&mgmt.ManagementConfig{WirelessConfig: &mgmt.WirelessConfig{}})
	require.NotNil(t, p)
	assert.Same(t, p, handlers.EnsureCachedWireless(p), "the API server must reuse the same cache")
}

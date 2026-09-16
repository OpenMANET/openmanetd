package network

import (
	"testing"

	"github.com/digineo/go-uci/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newBatmanMock returns a mockConfigReader with an optional bat0
// batman-adv device and no hardif interfaces.
func newBatmanMock(t *testing.T, withDevice bool) *mockConfigReader {
	t.Helper()

	m := &mockConfigReader{
		data:         map[string]map[string]map[string][]string{},
		sectionTypes: map[string]map[string]string{},
		anonSections: map[string][]string{},
	}

	if withDevice {
		require.NoError(t, m.AddSection("network", "bat0", "interface"))
		require.NoError(t, m.SetType("network", "bat0", "proto", uci.TypeOption, "batadv"))
	}

	m.commitCount = 0

	return m
}

func TestBatmanDeviceExists(t *testing.T) {
	assert.True(t, BatmanDeviceExists(newBatmanMock(t, true), "bat0"))
	assert.False(t, BatmanDeviceExists(newBatmanMock(t, false), "bat0"))

	// A section with the right name but the wrong proto is not a batman device.
	m := newBatmanMock(t, true)
	require.NoError(t, m.SetType("network", "bat0", "proto", uci.TypeOption, "static"))
	assert.False(t, BatmanDeviceExists(m, "bat0"))
}

func TestEnsureBatmanHardifInterface_CreatesWhenMissing(t *testing.T) {
	m := newBatmanMock(t, true)

	created, err := EnsureBatmanHardifInterface(m, "batmesh1", "bat0")
	require.NoError(t, err)
	assert.True(t, created)

	proto, _ := m.Get("network", "batmesh1", "proto")
	assert.Equal(t, []string{"batadv_hardif"}, proto)

	master, _ := m.Get("network", "batmesh1", "master")
	assert.Equal(t, []string{"bat0"}, master)

	assert.Equal(t, 0, m.commitCount, "helper must not commit; the caller batches")
}

func TestEnsureBatmanHardifInterface_LeavesExistingAlone(t *testing.T) {
	m := newBatmanMock(t, true)
	require.NoError(t, m.AddSection("network", "batmesh1", "interface"))
	require.NoError(t, m.SetType("network", "batmesh1", "proto", uci.TypeOption, "batadv_hardif"))
	require.NoError(t, m.SetType("network", "batmesh1", "master", uci.TypeOption, "bat0"))
	require.NoError(t, m.SetType("network", "batmesh1", "mtu", uci.TypeOption, "1536"))

	created, err := EnsureBatmanHardifInterface(m, "batmesh1", "bat0")
	require.NoError(t, err)
	assert.False(t, created)

	mtu, _ := m.Get("network", "batmesh1", "mtu")
	assert.Equal(t, []string{"1536"}, mtu, "existing options must survive untouched")
}

func TestEnsureBatmanHardifInterface_PropagatesSetError(t *testing.T) {
	m := newBatmanMock(t, true)
	m.setTypeError = assert.AnError

	_, err := EnsureBatmanHardifInterface(m, "batmesh1", "bat0")
	require.Error(t, err)
	assert.ErrorIs(t, err, assert.AnError)
}

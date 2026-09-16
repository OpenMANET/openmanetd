package network

import (
	"testing"

	"github.com/digineo/go-uci/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClearLuciWizardUsedWithReader_ClearsAndCommits(t *testing.T) {
	m := newMockReader()
	require.NoError(t, m.AddSection("luci", "wizard", "wizard"))
	require.NoError(t, m.SetType("luci", "wizard", "used", uci.TypeOption, "1"))

	require.NoError(t, ClearLuciWizardUsedWithReader(m))

	v, ok := m.Get("luci", "wizard", "used")
	require.True(t, ok)
	assert.Equal(t, []string{"0"}, v)
	assert.True(t, m.commitCalled)
}

func TestClearLuciWizardUsedWithReader_NoLuciIsNoop(t *testing.T) {
	m := newMockReader()

	require.NoError(t, ClearLuciWizardUsedWithReader(m))

	_, ok := m.Get("luci", "wizard", "used")
	assert.False(t, ok, "must not create the flag on images without luci")
	assert.False(t, m.commitCalled)
}

func TestClearLuciWizardUsedWithReader_CommitErrorWrapped(t *testing.T) {
	m := newMockReader()
	require.NoError(t, m.AddSection("luci", "wizard", "wizard"))
	require.NoError(t, m.SetType("luci", "wizard", "used", uci.TypeOption, "1"))
	m.commitError = assert.AnError

	err := ClearLuciWizardUsedWithReader(m)
	require.ErrorIs(t, err, assert.AnError)
}

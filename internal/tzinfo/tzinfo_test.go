package tzinfo_test

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openmanet/openmanetd/internal/tzinfo"
)

func TestPosixTZ_FixturePair(t *testing.T) {
	got, ok := tzinfo.PosixTZ("America/Denver")
	require.True(t, ok)
	assert.Equal(t, "MST7MDT,M3.2.0,M11.1.0", got,
		"must match the captured device fixture (after/mesh-gate-router-eth/system)")
}

func TestNames_SortedNonEmpty(t *testing.T) {
	names := tzinfo.Names()
	require.NotEmpty(t, names)
	assert.True(t, sort.StringsAreSorted(names))
	assert.False(t, tzinfo.Known("Not/AZone"))
	assert.True(t, tzinfo.Known("UTC"))
}

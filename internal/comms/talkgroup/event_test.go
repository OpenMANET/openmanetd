package talkgroup_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/openmanet/openmanetd/internal/comms/talkgroup"
)

func TestSourceString(t *testing.T) {
	t.Parallel()

	cases := map[talkgroup.Source]string{
		talkgroup.SourceRPC:  "rpc",
		talkgroup.SourceGPIO: "gpio",
		talkgroup.SourceInit: "init",
		talkgroup.Source(0):  "unknown",
		talkgroup.Source(99): "unknown",
	}

	for src, want := range cases {
		assert.Equal(t, want, src.String())
	}
}

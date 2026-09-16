package announce

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"

	"github.com/rs/zerolog"

	"github.com/openmanet/openmanetd/internal/comms/audiopool"
	"github.com/openmanet/openmanetd/internal/comms/codec"
)

//go:embed clips/*.opus
var clipFS embed.FS

// maxClipChannel bounds the clip scan; matches the 32 reserved channels.
const maxClipChannel = 32

// decodeClips decodes every embedded tg_NN.opus into FrameSize int16
// frames. Runs once at comms start. A fresh decoder per clip keeps
// libopus prediction state from bleeding between clips. Missing files
// simply end that channel's entry (log and skip at Announce time).
func decodeClips(log zerolog.Logger) (map[int][][]int16, error) {
	clips := make(map[int][][]int16, 5)

	for ch := 1; ch <= maxClipChannel; ch++ {
		data, err := clipFS.ReadFile(fmt.Sprintf("clips/tg_%02d.opus", ch))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}

			return nil, fmt.Errorf("announce: read clip %d: %w", ch, err)
		}

		pkts, err := oggPackets(data)
		if err != nil {
			return nil, fmt.Errorf("announce: clip %d: %w", ch, err)
		}

		dec, err := codec.NewOpusDecoder(audiopool.SampleRate, audiopool.Channels)
		if err != nil {
			return nil, fmt.Errorf("announce: decoder: %w", err)
		}

		frames := make([][]int16, 0, len(pkts))

		for i, p := range pkts {
			buf := make([]int16, audiopool.FrameSize)

			n, err := dec.DecodeS16(p, buf)
			if err != nil {
				return nil, fmt.Errorf("announce: clip %d packet %d: %w", ch, i, err)
			}

			if n != audiopool.FrameSize {
				return nil, fmt.Errorf("announce: clip %d packet %d: %d samples, want %d (re-encode with --framesize 20)",
					ch, i, n, audiopool.FrameSize)
			}

			frames = append(frames, buf)
		}

		_ = dec.Close()

		clips[ch] = frames

		log.Debug().Int("channel", ch).Int("frames", len(frames)).Msg("announce: clip decoded")
	}

	if len(clips) == 0 {
		return nil, errors.New("announce: no clips embedded")
	}

	return clips, nil
}

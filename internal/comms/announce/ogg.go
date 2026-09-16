package announce

import "errors"

// oggPackets walks the Ogg pages in b and returns the raw Opus packets,
// dropping the two mandatory header packets (OpusHead, OpusTags). The
// codec package decodes raw packets, not Ogg containers, so this minimal
// read-only parser bridges the gap without a new dependency. Startup-
// path only; allocation cost is irrelevant here.
func oggPackets(b []byte) ([][]byte, error) {
	const headerLen = 27

	var (
		packets [][]byte
		cur     []byte
	)

	for len(b) > 0 {
		if len(b) < headerLen || string(b[:4]) != "OggS" {
			return nil, errors.New("announce: bad ogg page header")
		}

		segCount := int(b[26])
		if len(b) < headerLen+segCount {
			return nil, errors.New("announce: truncated ogg segment table")
		}

		segTable := b[headerLen : headerLen+segCount]
		body := b[headerLen+segCount:]

		bodyLen := 0
		for _, s := range segTable {
			bodyLen += int(s)
		}

		if len(body) < bodyLen {
			return nil, errors.New("announce: truncated ogg page body")
		}

		off := 0

		for _, s := range segTable {
			cur = append(cur, body[off:off+int(s)]...)
			off += int(s)

			if s < 255 { // lacing value < 255 terminates a packet
				// Skip zero-length packets: valid Ogg, but meaningless
				// for Opus — and keeping one would both feed the decoder
				// an empty buffer and shift the header-packet skip below.
				if len(cur) > 0 {
					packets = append(packets, cur)
				}

				cur = nil
			}
		}

		b = body[bodyLen:]
	}

	if len(packets) < 3 {
		return nil, errors.New("announce: too few ogg packets for an opus stream")
	}

	return packets[2:], nil
}

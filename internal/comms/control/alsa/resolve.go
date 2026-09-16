package alsa

import (
	"errors"
	"fmt"
	"os"
	"strconv"
)

// Sentinel errors for the absolute-control API (Volume). The button
// Controller keeps its log-and-swallow behavior; these exist so the RPC
// layer can map failures to response codes with errors.Is.
var (
	// ErrNoCard indicates ALSA_CARD is unset or invalid and card
	// auto-detection did not produce a card.
	ErrNoCard = errors.New("alsa: no card available")
	// ErrControlNotFound indicates none of the candidate control names
	// resolved on the card.
	ErrControlNotFound = errors.New("alsa: control not found")
)

// Candidate control names per role, tried in order; first exact match
// wins. "Master" stays first on playback so the deployed VOL+/VOL− button
// behavior is unchanged on cards where it exists. gen2brain/alsa matches
// raw kernel element names exactly, which differ from amixer's simple
// names — both spellings are listed where cards disagree.
var (
	PlaybackVolumeNames = []string{"Master", "Speaker Playback Volume", "PCM Playback Volume", "Headphone Playback Volume"} //nolint:gochecknoglobals
	CaptureVolumeNames  = []string{"Mic Capture Volume", "Capture Volume", "Mic"}                                           //nolint:gochecknoglobals
	AGCNames            = []string{"Auto Gain Control"}                                                                     //nolint:gochecknoglobals
	PlaybackSwitchNames = []string{"Master Playback Switch", "Speaker Playback Switch", "PCM Playback Switch"}              //nolint:gochecknoglobals
	CaptureSwitchNames  = []string{"Mic Capture Switch", "Capture Switch"}                                                  //nolint:gochecknoglobals
)

// CardFromEnv parses ALSA_CARD into a card index. It returns an error
// wrapping ErrNoCard when the variable is unset or not a non-negative
// integer.
func CardFromEnv() (uint, error) {
	cardStr := os.Getenv("ALSA_CARD")
	if cardStr == "" {
		return 0, fmt.Errorf("ALSA_CARD unset: %w", ErrNoCard)
	}

	cardNum, err := strconv.Atoi(cardStr)
	if err != nil || cardNum < 0 {
		return 0, fmt.Errorf("ALSA_CARD=%q is not a non-negative integer: %w", cardStr, ErrNoCard)
	}

	return uint(cardNum), nil
}

// ResolveCtl returns the first control on m whose name exactly matches an
// entry of names, along with the matched name. It returns an error
// wrapping ErrControlNotFound when none match.
func ResolveCtl(m Mixer, names []string) (Ctl, string, error) {
	for _, name := range names {
		ctl, err := m.CtlByName(name)
		if err != nil || ctl == nil {
			continue
		}

		return ctl, name, nil
	}

	return nil, "", fmt.Errorf("no control among %v: %w", names, ErrControlNotFound)
}

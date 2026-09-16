package handlers

import (
	"context"
	"slices"
	"time"

	setupv1 "github.com/openmanet/openmanetd/internal/api/openmanet/setup/v1"
)

// ApplySetupStream is the test-exported alias of the package-private
// applySetupStream interface used by the SetupService phase helpers.
// External test packages can implement Send to drive ApplySetup
// without standing up a connect.ServerStream.
type ApplySetupStream interface {
	Send(*setupv1.ApplySetupResponse) error
}

// ApplySetupForTest is the testable entry point that bypasses the
// connect.Request / connect.ServerStream wrapping and lets unit tests
// exercise the full ApplySetup pipeline through a fake stream.
func (s *SetupService) ApplySetupForTest(
	ctx context.Context,
	profile *setupv1.MeshNodeProfile,
	stream ApplySetupStream,
) error {
	return s.applySetup(ctx, profile, stream)
}

// WizardConfigsForTest exposes a copy of the package-private
// wizardConfigs slice so external tests can assert coverage (e.g. that
// a new UCI config a phase writes to is also captured by the
// snapshot/rollback phase) without duplicating the list. A copy, so a
// test can never mutate the order the wizard relies on.
func WizardConfigsForTest() []string { return slices.Clone(wizardConfigs) }

// ReloadServicesForTest exposes a copy of the package-private
// reloadServices slice so external tests can assert coverage (e.g. that
// a new UCI config a phase writes to is also reloaded) without
// duplicating the list. A copy, for the same reason as above.
func ReloadServicesForTest() []string { return slices.Clone(reloadServices) }

// SetNowFnForTest overrides the unexported nowFn field used by the
// SET_TIMEZONE phase's clock-drift check. Lets tests inject a fixed
// reference time instead of racing the wall clock.
func (s *SetupService) SetNowFnForTest(fn func() time.Time) {
	s.nowFn = fn
}

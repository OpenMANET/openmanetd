# Bluetooth setup implementation goal

This replaces the previous BS-22 bench bring-up objective at the user's request.
Build on the existing codex/mm8108-bluetooth-native work, using GPT-6 Astra
with medium reasoning.

## Required behavior

- Bluetooth controllers stay powered off at boot by default. No discovery,
  advertising, pairing, or automatic connection attempts before explicit setup
  or connection. Saved bonds do not imply permission to enable Bluetooth.
- Detect missing controllers on CM4 hardware and USB adapter insertion without
  powering an adapter on. Explain unavailable hardware in the UI.
- New UI Bluetooth setup powers on the controller only after user initiation,
  runs a bounded scan, shows accessory pairing instructions, and lets the user
  choose a supported accessory.
- openmanetd handles bonding, Classic HFP audio and BLE PTT setup together.
  Report ready only after verifying audio endpoints, streams, and PTT monitoring.
- Setup offers a test tone and PTT indicator. Setup PTT must not transmit mesh
  voice. Use this microphone explicitly activates the accessory for comms.
- An unchecked opt-in permits automatic Bluetooth enable/reconnect at boot.
  Otherwise, reboot leaves Bluetooth off and retains the saved bond.
- Reconnect after temporary loss only while the user-enabled session is active.
  Gate existing BLE and HFP workers by this lifecycle.
- Disconnect stops connection attempts. Bluetooth off stops all Bluetooth work
  and powers down the controller. Forget removes the saved pairing/settings.
  Disconnect, removal, and shutdown must safely terminate active transmission.

## Delivery and verification

Implement daemon lifecycle and persistence, API and live status, and the new UI
flow. Preserve existing OpenVLM and web comms. Add meaningful unit, integration,
UI and race tests. Review the recorded failures in bs22-bench-review.md.

Finish with deployment to authorized test node manet02.local and physical BS-22
verification: guided pairing, audible tone, PTT press/release, microphone and
speaker audio, remote voice delivery when a receiving node is available,
reconnection, reboot defaults, explicit boot opt-in, and safe shutdown. Confirm
setup never transmits mesh voice and disabled Bluetooth causes no radio activity.
Test absent-controller and USB insertion behavior with hardware where available;
document simulated coverage and any outstanding physical checks distinctly.

Do not declare this goal complete until the required device checks pass.

## Completion — November 9, 2026

Implemented and deployed on manet02 (10.41.179.77). Final guided Forget,
Classic discovery, fresh bonding, automatic trust, audio/PTT readiness, setup
tone, and activation passed with the physical BS-22. The user confirmed the
tone and clear voice after fresh pairing, and clear voice in both directions
between the BS-22 and manet01's web Comms page.

Physical checks also passed for setup PTT with zero mesh voice packets,
held-PTT Bluetooth-off with transmission stopped before completion, saved
reconnect, default-off behavior, and the previously recorded reboot/explicit
boot-opt-in checks. Missing-controller and USB insertion behavior have automated
coverage; physical CM4-without-Bluetooth and USB hotplug were not available for
bench verification. These are not claimed as physical passes.

Final node state: user-activated and ready, PTT released, auto-enable false,
not discoverable/pairable/scanning. Temporary debug logging and periodic
instrumentation are disabled. Bond and automatic trust are retained. Changes
remain on codex/mm8108-bluetooth-native; no publication or merge is implied.

See bluetooth-handoff.md and bs22-bench-review.md for evidence, fixes, test
artifacts, and the distinction between automated and physical coverage.

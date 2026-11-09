# BS-22 bench review

## Observed failures and disposition

- BLE notifications reached the PTT handler, but transmission failed with a nil broadcast stream. This proves button delivery, not audio or end-to-end transmission. Release handling still needs an explicit bench check.
- Initially there was no Classic bond or HFP PCM. BR/EDR discovery followed by pairing established a stored bond. A direct Device1.ConnectProfile call with remote Hands-Free UUID 0000111e-0000-1000-8000-00805f9b34fb created both BlueALSA HFP-AG endpoints. Automatic profile connection remains to be integrated; the temporary utility is not a durable fix.
- Generic Device1.Connected cannot distinguish the two transports. The BLE connector no longer falls back to generic Device1.Connect. Cached GATT objects and aggregate connection state still need reconnect regression coverage.
- The first playback endpoint opened, but the second talkgroup failed with EBUSY. BlueALSA needs one physical playback client. The new shared playback path mixes receiver samples with saturation and gates individual ports independently. Its physical stream stays running while comms is enabled.
- Audio retry exists, but multiple failed initializations must be checked for stale stream references and repeated cleanup. HFP reconnection, daemon restart, and headset power-cycle recovery are not yet verified.
- A missing beep was previously attributed categorically to absent audio. Logs established unavailable audio, but did not establish whether the expected tone was headset-generated or application-generated. Audible beep and actual voice transport remain bench acceptance criteria.
- The initial build failure was caused by passing USE_SOURCE_DIR globally, which redirected the libnl-tiny build directory to daemon sources. Cleaning and rebuilding libnl-tiny repaired this. Local source overrides must be scoped to the daemon package and restored afterward.
- Node tools differ from desktop tools: busctl, gdbus, curl, and GNU grep options are absent; this bluetoothctl cannot accept a profile argument. Check available tools before invoking them. No more repeated pairing is needed while the Classic bond remains valid.

## Remaining acceptance checks

Run focused audio/control tests and race checks with host native dependencies, compile the target package, then verify one shared output with multiple talkgroups, independent receive toggles, PTT press/release, audible tones, microphone capture, remote RTP reception, and automatic recovery after device and daemon restarts. Do not call this reliable until these pass.

## Shared output change validation

ARM64 OpenWrt package compilation passed. Focused `go test -race` and `go vet` passed for `internal/comms/audio` and `internal/comms/control`. The new mixer regression checks clipping, independent channel disable, and silence when all ports are disabled. Host build dependencies were installed to make these checks runnable.

## Reboot recovery finding and fix

The next button test occurred after the node rebooted: its stored Classic bond survived, but HFP did not reconnect and the PCM endpoints were missing. Added a separate, cancellable HFP monitor that checks the paired BS-22's BlueALSA HFP endpoint and explicitly connects the remote Hands-Free profile when absent. Calls have a 10-second deadline and retry every 15 seconds. Focused race tests, vet, and ARM64 compilation passed. After deployment, the daemon connected HFP automatically at 19:31:58 and recovered hardware audio at 19:32:05. Both BlueALSA PCM directions reported Running=true and Muted=false. Audible output and remote voice reception still require the bench check.

## Guided setup hardware findings (2026-11-08)

- **Disconnect crash:** A named `cleanup` return captured by its own wrapper
  became the wrapper at return, recursively invoking itself until stack overflow.
  The live stack trace identified `startHardwareAudio.func4`. Cleanup now captures
  the underlying closer in a separate helper; regression checks readiness
  revocation and stream detachment before closing. Race tests and the real
  successful-stream Disconnect test passed after deployment.
- **Pairing lost on controller reset:** Controller `Pairable=false` maps to the
  kernel's non-bondable mode. Linux permits local pairing but forces non-bonding
  authentication, so `Paired=true` did not mean keys were saved. Temporarily
  enabling bonding produced `Bonded=true` and a stored Classic `[LinkKey]`.
  The daemon now opens that window only around scoped pairing, closes it on
  success/failure/cancellation, verifies `Bonded`, and requires it before saving
  the accessory or connecting automatically at boot. Saved-device connection,
  Disconnect, controller reset, and reconnect passed on the deployed build.
- **Unexpected reboot investigation:** The later full-node reboot followed the
  recorded network address-reservation update to `10.41.169.1`. That management
  path deliberately reboots after applying network settings. This is separate
  from the cleanup stack overflow, which restarted only the daemon. The earlier
  full reboot was not captured and its cause remains unproven.
- **Physical audio evidence:** User heard the local setup tone. PTT down/up were
  observed, and 228 valid Opus packets were emitted during the approximately
  five-second held interval, with no voice packets after release. Decoded audio
  was very quiet (peak28, RMS10.36 on signed16-bit samples), so intelligible
  microphone audio is still unverified. The requested close-microphone retest
  has not produced a sample yet. No receiving node is available for remote
  listening; packet capture is not equivalent to that acceptance check.

Full reboot checks also passed: explicit boot opt-in automatically restored
active audio/HFP/PTT readiness; resetting the opt-in then rebooting left the
controller off with its persistent bond retained. Bluetooth Off returned
successfully after the automatic connection.

Outstanding hardware acceptance: intelligible close-mic voice, setup-mode PTT
with zero voice packets, guided Forget/re-pair with the deployed bonding change,
and shutdown during a held PTT. Absent-controller and USB insertion coverage is
simulated until suitable physical hardware is available.

## Nov 9 verified audio and safety findings

- Native capture with the normal HM setup (including command15/payload1) was
  almost silent: peak1/RMS0.04 in a PTT-triggered sample. The matched native
  test with ReadSettings only produced peak16262/RMS1375.28 while BLE button
  events still worked. Removed SET_BLE_AUDIO from the normal setup flow.
- The deployed daemon then captured peak24308 before gain, with no dropped
  capture frames or encoding errors. User confirmed clear voice both from
  BS-22 to manet01's website and from the website to the BS-22.
- Earlier timed recordings could miss delayed user actions. Replaced the
  comparisons with PTT-triggered captures; unconfirmed older recordings are
  not evidence for choosing between mmap and readi capture.
- Bluetooth SCO periods were interpreted at the negotiated8k rate; specifying
  a20ms duration fixed the120ms capture bursts and improved speaker playback.
- Package reinstalls failed to preserve /etc/config/openmanetd, resetting the
  DHCP reservation flag and triggering address changes/reboots. Added the
  file to package conffiles and preserved the existing state on first upgrade.
- Reconnect could remain on cached GATT entries after LE disconnected while
  Classic stayed connected. NotConnected now clears that binding and requests
  LE reconnection with the existing retry limit.
- Physical setup-mode PTT produced zero RTP packets, with press/release observed.
  Held-PTT BluetoothOff stopped voice before action completion, powered down
  the controller, and retained the bond. Clock-offset bounds were checked;
  SSH capture-file growth alone was insufficient to establish packet timing.
- Temporary comms debugging and periodic diagnostic snapshots are disabled.

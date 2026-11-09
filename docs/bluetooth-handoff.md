# Bluetooth implementation handoff

The active goal is docs/bluetooth-setup-goal.md. User cleared the old goal and
explicitly started this new one. Use GPT-6 Astra, medium reasoning. No subagents
unless explicitly authorized. User requests compaction; no compaction tool is
exposed in this session.

## Current baseline

Daemon worktree: /home/jmcgeeal/Desktop/openmanetd-mm8108-bluetooth, branch
codex/mm8108-bluetooth-native. Commits b43d2f2, 885e76d, 49ca6b6 provide native
BS-22 sources, settings enums, and protobuf schema; proto gitlink 4db01b9.
Current uncommitted source changes MUST be preserved:
- audio/init.go and shared_playback.go/test: one physical BlueALSA playback
  stream mixes receiver ports with saturation; atomic per-port gates preserve
  independent receive/beep controls. Physical stream runs while comms runs.
- control/bs22_event_source.go: remove generic Device1.Connect fallback from BLE
  reconnection; reconnect LE when HM binding absent despite aggregate Connected.
- control/bs22_hfp.go/test: separate worker every 15 s, 10 s D-Bus deadline,
  connects paired BS-22's remote Hands-Free UUID 111e when its HFP PCM is absent.
  Currently enabled automatically for BS22 source; NEW GOAL must gate this and
  BLE work by explicit Bluetooth lifecycle authorization.
- docs/bs22-bench-review.md records failures and outstanding acceptance checks.

Read AGENTS.md, full CLAUDE.md and relevant .claude/rules before edits. Generated
API/frontend files must come from proto source. Race checks required.

## Bench state and evidence

Authorized lab node: manet02.local, SSH root (credential already supplied in
conversation; never persist it in repository). Resolve fresh IPv4 each time with
getent ahostsv4; address changes on reboot. Device BS-22 public MAC
41:42:86:99:1D:61; controller 2C:CF:67:A3:D2:31. Node is RPi4 MM8108 USB,
kernel 6.6.138, BlueZ 5.72, BlueALSA 4.3.1 local HFP-AG/CVSD.

BS-22 is Classic bonded. Scanning BR/EDR then interactive bluetoothctl Pair
succeeded. Remote SDP advertises Handsfree 111e, RFCOMM channel 2. The old
bluetoothctl accepts only one connect argument, so profile-specific connection
uses Device1.ConnectProfile with 0000111e-0000-1000-8000-00805f9b34fb.
Adapter1.ConnectDevice AddressType=public/random explicitly selects LE on 5.72;
there is no Bearer property accepted there and no Bearer.LE1 API on 5.72.

Latest package is installed and includes shared playback plus HFP reconnect.
After a subsequent real node reboot, HFP reconnected automatically at 19:34:15,
audio recovered at 19:34:24, and BLE notifications/priming at 19:34:25.
Both BlueALSA PCM source and sink reported Running=true, Muted=false.
User then confirmed hearing the tone. Remote voice delivery and release ending
TX are NOT verified. Ordinary TX logs are DEBUG and current logLevel is info;
absence of errors is not proof of PTT/voice delivery.

Config /etc/openmanetd/config.yml currently comms.enable=true, controlSource=bs22.
BlueALSA PCM paths /org/bluealsa/hci0/dev_41_42_86_99_1D_61/hfpag/{source,sink}.
An irrelevant OpenVLM mixer warning still appears for missing USB ALSA_CARD.
Node has bluetoothctl, sdptool, bluealsa-cli, aplay/arecord; no busctl/gdbus,
dbus-send/curl/sqlite3/python3. BusyBox grep has no --line-buffered. wget can POST
but API at :8087 returns 401 without auth. Do not keep repeating absent tools.

## Build and checks

Firmware /home/jmcgeeal/openmanet/firmware, same feature branch, based on MM8108
bump/newer kernel. Feed /home/jmcgeeal/openmanet/firmware/feeds/openmanet has
openmanetd/Makefile pinned to 49ca6b6 and untracked bluez-alsa/rpi-bluez-firmware
packages. Existing firmware changes must be preserved.

For local package builds temporarily insert package-local USE_SOURCE_DIR pointing
to daemon worktree into feed openmanetd/Makefile; run make package/openmanetd/compile
V=s from firmware, restoring exact Makefile in finally. NEVER pass USE_SOURCE_DIR
globally: it redirected libnl-tiny to daemon sources earlier. That damage was
repaired with package/libs/libnl-tiny/clean and rebuild.

IPK: firmware/bin/packages/aarch64_cortex-a72/openmanet/openmanetd_main-r5_aarch64_cortex-a72.ipk.
Deploy via scp to node /tmp then opkg install --force-reinstall and restart
/etc/init.d/openmanetd. Preserve modified node config; opkg leaves sample in
config.yml-opkg. Service start/recovery may take ~15 s; inspect CURRENT PID logs.

Go binary/gofmt: firmware/staging_dir/hostpkg/lib/go-1.26/bin/.
Host native deps were installed successfully using apt (pkg-config, opus,
opusfile, udev, ALSA, evdev dev packages).
Use PKG_CONFIG=/usr/bin/pkg-config and
GOMODCACHE=/home/jmcgeeal/openmanet/firmware/dl/go-mod-cache.
Focused go test -race ./internal/comms/audio ./internal/comms/control and go vet
on those packages passed, as did ARM64 package compilation.

Worktree has generated OpenWrt artifacts (.go_work, .pkgdir, .built*, .prepared,
.quilt_checked, .source_dir, .configured_*, ipkg-aarch64_cortex-a72). These are
not source changes; don't commit them. Do not discard actual uncommitted code.

## Next work

Implement daemon Bluetooth lifecycle/persistence/API and guided new UI exactly
as goal document states. Saved bond must NOT enable radio. Explicit boot opt-in
is unchecked by default. Controller absent detection must avoid powering on.
Setup test PTT must never transmit mesh voice. Gate workers, controller power,
scans, activation, disconnect/off/forget and fail-safe TX teardown consistently.
Finish with tests and physical BS-22/node validation; don't claim reliable voice
or completion merely because endpoints exist or streams report running.

## Guided setup progress

Added internal/bluetooth/session.go with tested consent/readiness/generation
policy, and bluez.go with passive inventory and bounded power/discovery/pair/
disconnect/forget transport operations. Race tests and vet pass for this package.
These components are not yet wired into daemon startup, UI, or the existing
comms workers, so they do NOT yet enforce radio-off behavior on the node.
Next: lifecycle owner must serialize operations, cancel/join workers on revocation,
register the pairing agent, persist settings, apply power policy to every
controller/hotplug, and gate comms activation/TX. Then API and guided UI.
Firmware feeds/packages/utils/bluez/files/bluetoothd.init starts bluetoothd -n -E;
build_dir/target-aarch64_cortex-a72_musl/bluez*/src/main.conf documents default
AutoEnable=true. Ship explicit Policy AutoEnable=false before claiming boot or
hotplug radio silence. Existing patch 0008 only enables experimental D-Bus APIs.

Pairing-agent progress: agent.go and agent_test.go now implement a temporary
Agent1 scoped to the selected device; BlueZ.Pair registers it for the operation,
rejects overlapping pairing, cancels failed pairing, and unregisters/unexports
on exit. It does not request default-agent status. Bluetooth package race tests
pass (rechecked at handoff). Integration and hardware validation remain pending.
Review discovery cleanup when StartDiscovery times out after server acceptance,
and validate selected adapter/accessory in the lifecycle owner. No guided-setup
code has been deployed. Goal remains active; user requested compaction now.

Lifecycle implementation progress:
- manager.go adds serialized controller ownership, explicit Begin, Off across
  all controllers, and cancellable Discover/Pair. Off revokes policy first,
  cancels and joins the in-flight operation (including its BlueZ cleanup), then
  powers down. Pair validates supported accessory and selected adapter.
- The manager has NOT been wired into startup/comms/API. It still needs saved
  settings, selected accessory, readiness/activation, disconnect/forget,
  hotplug reconciliation, runtime cancellation/TX hooks and boot opt-in.
- Discovery now attempts StopDiscovery even after a StartDiscovery timeout and
  clears its filter with an independent cleanup deadline. Tests cover lost
  start replies and cancellation cleanup.
- Race tests and vet pass for internal/bluetooth; diff whitespace check passes.
- Firmware patch patches/ekh-bcm2711/0009-bluez-explicit-radio-consent.patch
  modifies bluez-daemon installation to set Policy AutoEnable=false and
  ReconnectAttempts=0. Applied to local feeds/packages/utils/bluez/Makefile.
  Patch dry-run and transformation against actual BlueZ 5.72 main.conf passed.
  Firmware package has NOT yet been rebuilt/deployed with this policy, and
  existing /etc/bluetooth/main.conf may be preserved by opkg (must explicitly
  migrate/verify during final deployment). Node behavior remains bench baseline.

Comms integration progress:
- CommsConfig now accepts BluetoothSession (ConnectionAllowed, CanTransmit,
  ObservePTT) and BluetoothDevicePath. CommsManager.Enable and Start fail closed
  for Bluetooth sources without explicit session consent. These fields are
  copied by NewComms, but the Bluetooth manager does NOT yet start this runtime.
- TX authorization checked before PTT start, after tone settle, and for every
  encoded RTP send. Setup button events are observed without opening TX. Run
  clears TX on exit and checks revocation every 100ms for quiet capture teardown.
- Session.Lease scopes runtime callbacks to the current generation, rejecting
  stale presses/permission from a previous setup. State includes ButtonPressed.
- BS22 factory requires selected BluetoothDevicePath; BLE and HFP selection now
  stay on that device. Selected fallback filters the exact BlueALSA RFCOMM path.
  Legacy direct constructors remain for unit tests/compatibility, but production
  factory uses the selected constructor.
- BS22 event output closes after transport workers join, and Bluetooth Run
  cancels/drains its source before teardown. BLE D-Bus calls, AddMatch and
  BlueALSA inventory calls now have 5s deadlines (not yet all directly tied to
  parent context). Review fallback early-return join paths before finalizing.
- Tests found pre-existing unknown-source validation bug: Validate used a
  normalizer that silently defaults unknown names. Validate now checks trimmed
  names against registry, Start validates before defaults, and stale manager
  test uses genuinely unsupported name rather than now-registered bluealsa_xevent.
- Race tests and vet passed for bluetooth, comms and comms/control after changes.
- IMPORTANT remaining: audio applyDefaults still selects generic bt_sco for BS22;
  use selected device MAC in explicit bluealsa:DEV=<MAC>,PROFILE=sco before real
  pairing integration. Add readiness callbacks from actual streams/HFP/BLE,
  test tone API, runtime start/stop hooks, persistence/API/UI, hotplug, and final
  device verification. No new code has been deployed; boot bench still unchanged.

Runtime bridge and readiness progress:
- Bluetooth lease now reports AudioReady, ButtonsReady and HFPReady separately;
  aggregate readiness requires all three. Losing one immediately gates RTP.
- Selected BS22 audio now resolves to bluealsa:DEV=<selected MAC>,PROFILE=sco.
  Invalid/missing selection is rejected by Start before hardware access.
- Audio startup/cleanup reports readiness. BLE readiness follows HM notify+prime;
  enumeration errors now clear binding/readiness. HFP readiness requires matching
  device with BOTH source and sink PCM Running=true. Verified property meanings
  in local BlueALSA 4.3.1 doc/org.bluealsa.PCM1.7.rst. Monitor checks every 2s;
  profile connection attempts remain throttled to 15s.
- bluetooth.Manager constructor is now NewManager(hardware,runtime). Runtime
  interface is implemented by CommsManager.StartBluetooth/StopBluetooth.
  Connect bonds, revalidates session generation and accessory, starts setup
  runtime with a generation-scoped lease; Activate requires readiness.
  Disconnect/Off/Forget revoke lease, cancel/join runtime, then touch hardware.
- CommsManager temporarily replaces local audio during setup without modifying
  persisted comms settings. Stop restores a previously running non-Bluetooth
  source. Tests cover this restoration and setup->ready->activate->off ordering.
- This bridge is NOT yet instantiated in openmanet.Start. Next: startup/hotplug
  reconciliation and saved settings, API schema/handlers, test-tone dispatch,
  UI flow, firmware packaging and physical verification. No deployment yet.

Daemon/API/persistence progress:
- Bluetooth settings now live beside config.yml in bluetooth.json (0600, atomic
  rename): saved adapter/device addresses, display name, explicit AutoEnable.
  firmware openmanetd conffiles already preserves /etc/openmanetd/ directory.
- NewManager now takes (hardware,runtime,store). Initialize powers off first,
  loads settings, matches saved bond by controller MAC (not hci index), and
  connects only for saved opt-in. Boot activation still requires all readiness
  gates before TX. New device selection resets boot opt-in.
- Status, SetAutoEnable, Reconcile added. Reconcile keeps unselected controllers
  off and revokes runtime on selected-controller removal or inventory failure.
  Selected controller identity compares address as well as object path.
- openmanet.Start invokes startBluetooth before ordinary comms startup. Helper
  creates dedicated system bus, initializes lifecycle, runs 2s reconciliation,
  and joins/stops/powers off on daemon shutdown. Nil manager uses nil interface
  in API configuration, avoiding typed-nil handler panics.
- BluetoothService proto, generated Go/JS, handlers and registration added:
  GetBluetoothStatus, ExecuteBluetoothAction, UpdateBluetoothPreferences. Actions
  include begin, bounded 20s scan, connect, activate, disconnect, off, forget,
  test tone. Existing authenticated API middleware covers these endpoints.
- Test tone requests are queued into comms Run, never open TX; require running
  audio/HFP and reject during actual transmission. One pending tone maximum.
- Tests cover settings default/roundtrip, boot opt-in versus saved bond, hotplug
  off, lifecycle ordering, restoration, no-TX tone, handler dispatch/error codes
  and proto validation. Race tests passed for bluetooth, comms, comms/control,
  handlers and openmanet; vet passed for changed daemon packages.
- Host handler compilation needed libpam0g-dev (installed) and
  CGO_CFLAGS=-D_GNU_SOURCE. Use this env for broader native checks.
- Codegen tools installed per .devcontainer versions in /tmp/openmanet-bt-tools:
  buf1.66.0, protoc-gen-go1.36.11, protoc-gen-connect-go1.19.1.
  make proto-lint and full make buf passed; no generated files edited manually.
- No UI page yet and no new deployment. Next: read full .claude/rules/frontend.md
  (last read output truncated), invoke local react-best-practices SKILL.md per
  repository requirements, implement Bluetooth page/client and navigation using
  Lattice + useVisibleInterval, then UI/integration/build/device checks.
- Remaining review items: Forget when saved controller is unavailable, pending
  tone request cancellation, BLE fallback early-return joining, errors during
  boot initialization/settings load; don't claim physical readiness or RF-off
  behavior until deployed and tested. Existing bench node remains untouched.

Guided UI progress:
- Added lazy /settings/bluetooth route, Settings tab, and Comms toolbar link.
  SettingsBluetooth.jsx + bluetoothApi.js use the generated authenticated RPC
  client. Page handles absent hardware, adapter selection, explicit setup/20s
  scan, supported accessory selection, test tone, PTT state, readiness-gated
  activation, disconnect/off/forget and boot opt-in toggle (off by default).
- Bluetooth off can cancel an in-flight scan/pair request. Polling uses
  useVisibleInterval (500ms enabled, 5s off), only one read in flight, cleanup
  aborts reads/actions; status failure is separate from action errors.
- Consulted local react-best-practices skill. No animations added. BTECH pairing
  instructions verified at https://baofengtech.com/product/bs-22/ and linked
  in UI: power on then fast double-press of power/side key.
- Fixed mobile settings tab alignment so adding Bluetooth doesn't push the
  first tabs out of scroll reach. Comms tests now wrap MemoryRouter for new Link.
- All 852 frontend tests (69 files) pass. Frontend lint exits0 (6 existing
  react-refresh warnings); focused changed-file lint clean. Production build
  and CSS target guard pass. Static bundle rebuilt (tracked old hashes removed,
  new assets untracked until staging; keep all output for final commit).
- Browser mocked-RPC QA via Playwright: 360x640 and 1280x900, zero page errors,
  no page horizontal overflow, all Bluetooth action targets44px, no mutation
  on initial page load. Screenshots /tmp/bluetooth-{mobile-off,mobile-ready,
  desktop-ready}.png. Physical Bluetooth not covered by these browser mocks.
- Node24 installed /tmp/node-v24.0.0-linux-x64/bin; pnpm11.1.2 installed
  /tmp/openmanet-web-tools/node_modules/.bin. Dependencies installed from frozen
  lockfile. Include both paths for frontend commands; Go path as before.
- Browser tools /tmp/openmanet-browser/node_modules (Playwright1.51.1), script
  /tmp/openmanet-browser/check.cjs. Vite local test server started on5174,
  exec session28925; check live handle before reuse. No external deployment.
- ARM64 firmware package build succeeded with package-local USE_SOURCE_DIR,
  original feed Makefile restored in finally. Log /tmp/openmanet-bt-firmware-build.log.
  Latest IPK is ready but NOT deployed. manet02.local currently resolves
  10.41.102.229 (resolve fresh at deployment).
- Next: fix fallback send-failure branch so forwardBlueALSAFallback always
  drains/joins child events on cancellation (currently only ctx.Done branch
  joins); review stored/absent-controller Forget and test-tone cancellation.
  Rebuild if source changes; apply/migrate BlueZ AutoEnable=false and
  ReconnectAttempts=0 on node before daemon deployment. Then real authenticated
  UI/API/device tests, including off-default reboot and opt-in. Goal not complete.

Live guided setup test, 2026-11-08 (supersedes predeployment notes above):
- Fixed fallback child joining on every exit; control race tests and ARM64
  package rebuild passed. Deployed rebuilt package to manet02 after migrating
  BlueZ AutoEnable=false / ReconnectAttempts=0; old main.conf backed up.
  BlueZ firmware package also builds successfully.
- Actual frontend is HTTPS8081 (HTTP8080); authenticated UI off state passed
  without page errors, saved boot opt-in false. Screenshot:
  /tmp/manet02-bluetooth-off.png.
- UI discovery/pairing selected BS-22 41:42:86:99:1D:61. During first audio
  connection attempt node unexpectedly rebooted. User confirms power stayed on.
  Cause unresolved: no pstore crash record or decisive kernel log yet. Previous
  IP10.41.102.229 changed to10.41.70.34; always resolve mDNS fresh.
- Saved Bluetooth settings survived reboot, controller powered/discoverable
  false and no autoactivation. This verifies off-default with an existing bond,
  but unexpected reboot remains a reliability failure requiring investigation.
- Started continuous SSH logread capture /tmp/manet02-guided-bt-live.log.
  Explicit BEGIN_SETUP + saved-device CONNECT at21:03:26 succeeded. Initial
  audio attempts preceded HFP availability; recovery opened playback and mic
  at21:03:37. HFP and BLE control both connected.
- At21:05-21:06 authenticated status confirms ready/audioReady/hfpReady/
  buttonsReady true, active false. TEST_TONE RPC accepted. Asked user whether
  audible and to hold PTT two seconds then release; physical reply pending.
- Do not mark goal complete: physical tone/PTT/voice/reconnect/safe off and
  explicit boot opt-in still need acceptance. Keep setup inactive until ready
  for the explicitly announced transmission test.
- User confirmed audible tone ("Yes"). Activated microphone at21:07:19 for
  announced voice test. Live status then active/ready/audio/HFP/buttons true.
  Running 120s status-edge monitor and mesh capture (exec session59057), outputs
  /tmp/manet02-ptt-observations.jsonl and /tmp/manet02-voice-test.pcap. Asked user
  to hold PTT and speak for five seconds and report receiver availability/audio.
- Current firmware get_throttled=0x0, temperature47.7C; no evidence of current
  undervoltage/throttling, but this does not explain/exclude the earlier reboot.
- Inspected built bluez-daemon_5.72-r2 ARM64 IPK itself: main.conf contains
  AutoEnable=false and ReconnectAttempts=0, confirming packaged default policy.
- Voice retry succeeded: observed PTT down at1794197350.866 and release at
  1794197356.174, with228 channel1 RTP packets over4.443s and no further voice
  packets after release. User spoke for five seconds; no receiving node exists
  nearby, so remote listening remains unverified. Artifacts:
  /tmp/manet02-voice-retry.pcap and /tmp/manet02-ptt-retry.jsonl.
- Disconnect at21:09:48 exposed deterministic fatal stack overflow. Full trace
  saved in /tmp/manet02-guided-bt-live.log: startHardwareAudio.func4 recursively
  calls itself at lifecycle.go:110. Named cleanup return gets reassigned to the
  wrapper while its closure captures that same variable. Daemon restarted
  (PID13222); node boot ID/uptime unchanged. This is distinct from earlier full
  node reboot. New daemon initialization powers Bluetooth off.
- Fixed by unnamed return plus separate hardwareAudioCleanup capturing the
  underlying closer. Added regression verifying readiness false and stream
  detachment before exactly one underlying close. Race tests running session
  30815; rebuild/deploy and real Disconnect/Off/reconnect retest still required.
- Status/pcap observer session79440 may remain live until its300s deadline;
  sessions invalid after daemon crash, so API observations may fail. Preserve
  capture; do not treat transient HTTP TLS timeout as node reboot.
- Cleanup fix race tests (comms/bluetooth) and vet pass; ARM64 build exit0
  (/tmp/openmanet-bt-cleanup-build.log). Deployed cleanup-fixed IPK successfully,
  daemonPID16614. Before deployment controller verified Powered/Discoverable/
  Pairable/Discovering=no, ActiveInstances0 after crash restart.
- Opus decoding of voice retry:228 valid packets,4.56s PCM, no decode errors,
  peak28 and RMS10.36 of32768. Signal extremely quiet; usable microphone audio
  NOT proven. /tmp/manet02-voice-retry.wav. Inspect HFP mic gain/settings and
  retest speech near mic. Saved BlueALSA source/sink Volume=0; SoftVolume=false;
  Mute=false (not enough to attribute low signal without live PCM inspection).
- Real cleanup retest session90223 attempts connect, Disconnect, reconnect.
  First connection currently cannot reach headset (Host is down/Resource busy),
  so has not exercised successful-stream shutdown yet. Asked user to power
  BS-22 off/on normally and keep beside node. Runtime retries remain enabled
  in setup (not active). No repeated pairing/bond removal requested.
- User restarted headset. Existing workers reconnected BLE at21:13:56, HFP
  at21:14:18, audio at21:14:21. Cleanup-fixed Disconnect returned{} at21:14:24,
  daemonPID16614 survived and logged subsystem stopped/disabled. Regression
  hardware success: successful-stream cleanup no longer recurses/crashes.
- Immediate BEGIN_SETUP then CONNECT failed "select a supported accessory on
  the active controller". bluetoothctl info says device not available afterward;
  /var/lib/bluetooth/2C:CF:67:A3:D2:31 lacks device directory. No RemoveDevice
  action was issued. Need investigate Paired versus Bonded: BlueZ5.72's local
  doc/org.bluez.Device.rst explicitly distinguishes temporary pairing from
  persisted bonding. Current implementation only checks Paired. Do not declare
  saved bond persistence reliable until real Bonded and stored key verified.
- Network management triggered another reboot at21:15:03, preceded by recorded
  "Static IP 10.41.169.1 and DHCP configured via address reservation". Source
  internal/mgmt/address_reservation.go:267-282 deliberately reboots after that
  operation. New nodeIP10.41.169.1, bootIDfd615093-66b2-433d-9a9e-7ba6be101dfb.
  This supplies evidence for this reboot's network trigger (not proof of the
  earlier reboot's cause). mDNS may still cache10.41.70.34. Do not modify network
  addressing blindly as part of Bluetooth work.
- Node /var/lib/bluetooth symlinks /etc/bluetooth/keys; no device info files
  found after latest reboot. Saved daemon selection may outlive actual bond.
- Continuous old logstream51460 exited255 on IP change; complete local log
  /tmp/manet02-guided-bt-live.log preserved. Capture79440 ended normally.
  All test/build/deploy script handles from this turn are terminal. Need new
  logstream on current nodeIP before more tests. Goal remains active.

Persistent bonding fix, next goal turn:
- Confirmed root cause from deployed kernel source: hci_event.c forces
  HCI_AT_NO_BONDING when HCI_BONDABLE is clear even for local pairing.
  BlueZ adapter Pairable property maps to that flag; prior bench left it false.
- User entered pairing mode. Using normal guided API Begin/Discover then
  temporarily bluetoothctl pairable on around CONNECT produced Bonded:yes and
  /etc/bluetooth/keys/2C:CF:67:A3:D2:31/41:42:86:99:1D:61/info exists.
  Immediately set pairable off. No bond secrets printed. This was a manual
  diagnostic workaround, not yet the updated daemon implementation.
- Implemented scoped Pairable=true in BlueZ.Pair after agent registration,
  deferred Pairable=false with fresh cleanup context (also on lost enable
  reply), explicit Bonded verification after Pair. Added Accessory.Bonded;
  Manager connect/skip-pair and Initialize require Bonded rather than Paired.
  Regression covers temporary pairing rejection, saved selection withheld,
  successful bond, failure/timeout cleanup. Race tests pass, vet pass.
- Bonding-fixed ARM64 build running session65777, log
  /tmp/openmanet-bt-bond-build.log. Not yet deployed. Firmware feed Makefile
  override restored by build wrapper finally. Latest running node still
  cleanup-fixed build.
- New logstream83219 runs against10.41.169.1, file
  /tmp/manet02-bond-retest-live.log. Existing bonded HFP source/sink now Running
  true, CVSD8k mono, Volume15, Mutedfalse, SoftVolumefalse.
- Activated and started voice capture session78469 (180s) with status edges
  /tmp/manet02-bonded-ptt.jsonl and /tmp/manet02-bonded-voice.pcap. User asked to
  speak close to microphone for5s with PTT; awaiting answer. Reusable capture
  decoder /tmp/decode-manet-capture.py outputs count/PCM peak/RMS and WAV.
- Bonding fix build65777 completed0; daemon/API race tests pass after
  Bonded changes. Package copied to10.41.169.1:/tmp/openmanetd-bond-fix.ipk
  (copy2861 exit0), awaiting install after current microphone capture window.
  Captured bond file section names confirm [LinkKey] (Classic bond). User has
  not answered latest close-mic speech request; no PTT/voice frames seen yet.
- Close-mic capture78469 finished normally with zero packets and no PTT
  edges; user has not answered this specific request. Therefore microphone
  level retest still pending, not a failed microphone measurement.
- Installed bonding-fixed build from /tmp/openmanetd-bond-fix.ipk, daemon11654.
  Actual status after restart shows saved selected/paired BS22 retained with
  enabled/powered/active false. Started saved-connect -> Disconnect -> reconnect
  script71673, currently running. Helper /tmp/manet_bt.py provides authenticated
  status/actions/ready checks without printing credentials; password passed
  separately to constructor. Latest host tests for daemon/API pass.
- Deployed bonding-fixed build passed saved connection -> Disconnect ->
  reconnect (script71673 exit0), all audio/HFP/button readiness true after each
  connection. Bond no longer disappears when Begin resets the controller.
- Actual full reboot with explicit autoEnable=true passed: active/ready/audio/
  HFP/buttons true automatically after boot, Bondedyes. Script46872 exit0 reset
  autoEnable=false afterward. Then OFF returned{}, controller off and bond
  retained. Second full reboot default-off verification running session2391.
- Old logstream83219 ended on intentional reboot; /tmp/manet02-bond-retest-live.log
  preserved. Need start a new stream if subsequent tests require continuous logs.
- Second full reboot default-off check2391 passed: enabled/active/autoEnable
  false, controller powered false, saved paired/selected device retained. Node
  left Bluetooth OFF, opt-in false, at10.41.169.1. Both clean reboot cases now
  verified on bonding-fixed build. Added unit regression that cached Paired
  without Bonded cannot activate at boot even with an old saved opt-in.
- Physical checks still pending user: close-mic voice level/intelligibility,
  setup PTT with zero voice packets, held-PTT shutdown, deployed Forget/re-pair.
  Do not silently assume the earlier "Done" covers this later speech request.
  No live capture currently. User has not answered latest microphone question.

Physical acceptance blocked by operator availability:
- User explicitly answered "Not available right now" to readiness request.
  Do not start more captures or ask for repeated button presses until available.
- Last live check:10.41.169.1, daemon4099, enabled/active/autoEnable false,
  controller powered false, BS22 paired and saved. mDNS intermittently fails;
  known IP remains reachable. No test capture is running.
- Resume with close-mic voice capture (earlier sample extremely quiet), then
  setup-mode PTT zero-voice-packet proof, held-PTT shutdown, and guided
  Forget/re-pair to validate the deployed Pairable/Bonded fix end to end.
- Clean default-off reboot, explicit opt-in reboot (reset to false), persistent
  Classic bond retention, saved reconnect, Disconnect, Off, audible setup tone,
  and ordinary PTT press/release with packet gating already passed.
- Goal remains incomplete. Do not replace physical acceptance with mocks or
  repeated automated tests. Remote listening unavailable (no receiving node);
  absent-controller/USB insertion coverage is simulated. All source changes
  remain in the existing codex/mm8108-bluetooth-native worktree; preserve them.

Nov9 two-node audio debugging:
- User now available; manet02 is10.41.53.45 (mDNS resolved), manet01 is10.41.0.1.
  New logstream21868 writes /tmp/manet02-nov9-live.log.
- Saved device entry disappeared during reconnect, and bond info file vanished.
  Re-paired through deployed guided API after user entered pairing mode. Verified
  Bondedyes, stored info file, HFP source Runningtrue Volume15 Mutedfalse.
- User close-mic test capture /tmp/manet02-nov9-voice.pcap only12 valid Opus
  packets/0.24s, peak9 RMS5.05; no sampled PTT state edge. Not good voice evidence.
- User is on https://manet01.local:8081/comms (also10.41.0.1). Live manet01 was
  comms disabled/controlSourceOpenVLM. Enabled comms and Web mode via API;
  both nodes TG1 TX/RX enabled. Official docs checked:
  https://openmanet.github.io/docs/comms/setup and /web-ui confirm HTTPS8081,
  enabled comms/Web control source/browser mic permission.
- User now hears BT->browser distorted/"aliens", browser->BT silent.
  Dual capture80207 /tmp/manet01-duplex.pcap and /tmp/manet02-duplex.pcap:
  browser test produced99 packets atmanet01 and98 atmanet02,1.96s audio,
  decode zero errors, peak21823 RMS2528.57. Thus healthy-level browser audio
  reaches manet02. No BT-origin packets during this particular dual test.
  Split /tmp/manet02-from-10.41.0.1.pcap and .wav available for replay.
- Temporarily enabled comms.debug and instrumentation5s onmanet02. Original
  config backed up /tmp/config.before-bt-diagnostics.yml (0600). MUST RESTORE
  this config when diagnostics finish! /tmp/bt-diagnostics directory mode700.
  Daemon restartedPID27626; first audio recovery at07:54:43, BLE prime failed
  NotConnected; BlueALSA BT read aborted07:55:08. Device entry again vanished.
  Readiness audioReadytrue but HFP/buttonsfalse so activation correctly refused.
- Read BlueZ5.72 device.c: device_bonding_complete failure marks device temporary
  if that bearer is unpaired and device untrusted, even if other bearer bonded.
  Could explain Classic bond deletion after LE authentication failure; hypothesis
  not yet proven. All observed BS22 records Trusted:no. Next test explicitly
  trust selected bonded device before/while starting BLE/HFP and verify stability.
  User asked to enter pairing mode again; answer pending. No trust code changes
  yet. Do not call disappearance fixed based only on earlier reboot tests.
- Session69686 readiness timed out; no automatic activation. Capture80207 terminal.
  Goal still incomplete; goal tool status remains blocked from prior availability
  wait, user has resumed work conversationally (do not create a replacement goal).
- User re-entered pairing mode; guided Pair succeeded, then explicitly set
  bluetoothctl trust41:42:86:99:1D:61. Trustedyes/Bondedyes and all readiness
  returned; activated. Trust remains MANUAL diagnostic change, not code yet.
- Replayed98 healthy browser Opus packets by UDP unicast to manet02:38801 after
  test tone. User hears voice distorted. Snapshot97received/30pushed/67rejected,
  zero parse errors/overflows: playback cursor overruns arriving packets.
- Capture telemetry repeatedly shows ~120ms gaps with6 Opus frames per callback.
  In malgo/miniaudio ALSA backend, explicit PeriodSizeInFrames is retained at
  negotiated internal sample rate. BS22 CVSD8k makes960frames120ms (rather than
  intended20ms at app48k); playback2880frames becomes360ms. ALSA bluealsa config
  is typeplug, but miniaudio disables ALSA resampling before rate_near.
- Implemented applyBluetoothTiming: for bluealsa named devices only, set
  PeriodSizeInFrames0 and PeriodSizeInMilliseconds20 for capture/playback; retain
  app48k format. Bluetooth requested-period logging/latency now uses960 app
  frames rather than2880. Other audio devices untouched. Added regression tests.
  Initial test passed; typed-int assignment followup fixed withuint32 casts.
  Final race tests65334 and ARM64 build14538 running; log
  /tmp/openmanet-bt-timing-build.log. Need verify successful build then deploy,
  reconnect trusted bond and repeat browser replay and physical audio capture.
- Temporary instrumentation config STILL ACTIVE; restore original node config
  /tmp/config.before-bt-diagnostics.yml when done. Do not overwrite user settings
  onmanet01: it was intentionally changed to enabled/Web for browser test.

Nov 9 follow-up bench results (after timing build):
- Address manager rebooted manet02 again, changing it to 10.41.116.163.
  Saved trusted bond survived; explicit reconnect/activate succeeded.
- Timing replay: 98 RX / 81 queued / 17 rejected, versus previous 97/30/67.
  User confirmed playback "Clearer now". Still not full audio acceptance.
- Native arecord S16_LE/8k capture, daemon stopped, captured strong speech:
  /tmp/bs22-direct-mic.wav peak29793, speech RMS roughly2100–2700.
- Standalone same-version miniaudio capture probe produced 60s at48k, native8k
  period160. /tmp/bs22-malgo-mic.raw peak2503 RMS62.69, but user never confirmed
  speaking during that window. Do NOT treat it as a matched speech comparison.
- User later reconnected and keyed normally:140 encoded frames/no errors, website
  silent. Healthy browser voice replayed directly to manet01 was audible, user
  confirmed. Receiver is TG1 enabled with browser connection.
- Temporary HM command15 payload0 did not help: /tmp/bs22-mode-off.pcap 276
  valid packets,5.52s,peak12,RMS7.13. Restored command15 payload1 and Pairableoff.
  This does not establish whether command15 itself affects microphone operation.
- Added capture_peak/pre_encode_peak per-PTT instrumentation (no stored audio),
  schema1.7.0, to distinguish input from gain/encoding failures. Tests/race/vet
  pass; package build underway at /tmp/openmanet-bt-levels-build.log.
- IMPORTANT: original /tmp/config.before-bt-diagnostics.yml backup was lost on
  node reboot. Temporary comms.debug=true and instrumentation remain enabled;
  disable these diagnostic overrides at bench completion without changing other
  user settings. /tmp/bt-diagnostics recreated after reboot, directory0700.

Latest Nov9 bench state:
- Level instrumentation1.7.0 built/deployed successfully; user speech test showed
  capture_peak10, pre_encode_peak20. After another reboot (node now10.41.179.77),
  reconnect/activate and fresh PTT test showed capture_peak8/pre_encode_peak16,
  163 encoded frames in latest cycle, no encode errors/drops; buttonPressedfalse
  on release. Weak signal is upstream of gain/Opus in live app path.
- /tmp/bs22-mmap.raw and /tmp/bs22-readi.raw each20s, peak2273/1566,RMS69/67.4.
  User's late response suggests speech may have occurred after those recordings;
  NOT a valid matched speech comparison. Do not conclude MMAP vs readi resolved.
- Healthy voice replay to manet01 was audible (confirmed); browser playback works.
- Repeated temporary diagnostic restarts leave Bluetooth OFF by design until
  explicit reconnect. Avoid timed capture prompts that expire before user acts;
  continuous level counters avoid that issue.
- Current node10.41.179.77 is connected/active, buttons/audio/HFPready. Original
  debug/instrumentation overrides remain active, /tmp/bt-diagnostics recreated.
  Need diagnose repeated node/address-manager reboots and upstream capture
  suppression; end-to-end voice acceptance remains UNRESOLVED.

Resumed goal Nov9 ~08:50 node time:
- Found concrete package reinstall problem: feeds/openmanet/openmanetd/Makefile
  installs /etc/config/openmanetd with dhcpconfigured0 but did not list it as a
  conffile. Added it to conffiles in feed and firmware board patch0010.
  Build /tmp/openmanet-preserve-state-build.log passed; inspected IPK control
  conffiles contains /etc/config/openmanetd. NOT installed yet. First upgrade
  must back up and restore live UCI state before daemon runs, because old
  installed package lacks protection. Current node dhcpconfigured1, stable.
- Built temporary event-driven probe /tmp/bs22-ptt-probe.go -> ARM64 executable,
  godbus-only. It connects HFP+LE, subscribes HM PTT, primes samemode1, starts
  arecord8k first, then miniaudio48k next. Each stage records ONLY while PTT
  held, waiting up to30min for user, bounded30s stored sample. No meshTX.
- /tmp/bs22-capture-probe.c updated for stdout mode and long lifetime, capture
  child terminated by event harness on release. Tools copied to node /tmp.
- /tmp/run-bs22-ptt-probe.py is RUNNING host session62199. Stopsdaemon then
  runsprobe; finally restartsdaemon and savedconnect/activate automatically.
  Output READY native confirmed. User asked for ONE five-second PTT press.
  On reply poll62199; expect COMPLETE native then READY malgo. Ask secondpress
  only after readiness confirmed. After secondrelease copies raw files host:
  /tmp/bs22-ptt-native.raw (8k), /tmp/bs22-ptt-malgo.raw (48k), then restoresapp.
  This holds same BLE mode for both, unlike previous uncontrolledtimedtest.
- Temporary debug/instrumentation config still enabled on node. Do not forget
  cleanup after bench. Packagepreservationfix not yet installed, audiofault not
  fixed, final hardwareacceptance pending.

Event-driven comparison complete: native captured PTT down/up46000bytes (2.875s)
peak1 RMS0.04; malgo247680bytes (2.58s) peak12 RMS6.58. BOTH quiet with same
BLE mode1 setup; thus not solely malgo. Hostfiles /tmp/bs22-ptt-{native,malgo}.raw.
Automatic normal daemon restore session62199 timed out readiness90s.
Built /tmp/bs22-passive-probe.go/binary: same procedure but omit HMcommand15,
readsettingsonly, one nativecapture namedpassive. Copiedbinary to node.
Started /tmp/run-bs22-passive-probe.py hostsession58843; probe failed
Authentication Failed beforecapture, finally restorednormaldaemon (PID23096)
and attemptingreconnect, currentlyBondedyes Trustedyes Connectedno. Logs HFP
SDP Hostdown, LE Resourcebusy. Userasked to powercycle BS22 normally andkeepPTT
released. MUST waitforresponse beforedependentphysicaltest. No new sample yet.

BREAKTHROUGH Nov9 ~09:00: passive probe (ReadSettings only, NO command15)
completed actual PTT-triggered nativecapture54000bytes/3.375s, peak16262 RMS1375.28.
Matched earlier primed native sample peak1 RMS0.04; malgo12 RMS6.58. Omitting
SET_BLE_AUDIO command15 produced usable-level voice while BLE PTT events work.
Removed command15 from production primeBLE; keeps ReadSettings and notifications.
Added regression test requiring only ReadSettings and failurepropagation test;
control+bluetooth race tests pass. Packagebuildsession14524, log
/tmp/openmanet-bs22-mic-fix-build.log. Needs deploy and fullapp speechtest.
Probe session12715 still finishing normaldaemon restore (oldcode may reprime);
wait/finish it before deploying. Node remains10.41.179.77.
Network-state preservation patch included in newbuild. On firstinstall preserve
/etc/config/openmanetd explicitly around opkg, oldpackage lackedconffilemetadata.
Further fix: primeBLE NotConnected now invalidates cached GATTbinding and requests
explicit LE reconnect, respecting5s connect retry; otherwise aggregateClassic
Connected and cached GATT could leavebuttonreadinessfalse indefinitely. Tests
caught blueZErrorName missing*dbus.Error; now supportsbothpointerandvalue.
Finalcontrol+bluetoothrace andvet passed. Finalfreshbuildsession93271 logfile
/tmp/openmanet-bs22-final-mic-build.log. /tmp/deploy-bs22-mic-fix.py prepared,
NOT RUN YET: validatesconffiles,copyIPK,backsup /etc/config/openmanetd beforeopkg,
restoresstatebeforedaemonstart,connect+ready+activate. Runafterbuildsuccess.

Final microphone+LErecovery package built and deployed via
/tmp/deploy-bs22-mic-fix.py, hostsession43875 still ready-polling. NewdaemonPID3230,
node10.41.179.77, UCI dhcpconfigured1 retained and uptime30min afterinstall(no
reboot). Newpackageconffileprotection included. Tests/race/vet passed after
pointerdbuserrorfix. Headset currently notanswering, HFP SDPHostdown, LEbusy,
noBlueALSAPCM. Userasked powercycle thenpairingmode. Waitforuserphysicalreply,
thencheck/finishsavedconnect+Activate andrequestnormalwebspeaktest. Continuous
capture_peak/pre_encode_peak diagnostics remainenabled, no shortcapturedeadline.
Strong passive nativecapture (/tmp/bs22-ptt-passive.raw peak16262 RMS1375) is
rootcauseevidence; full app voice afterfix STILL UNVERIFIED.

HARDWARE PASS: user confirms BS22 -> normal manet01 website voice "sounds fucking
 great" after command15 removal. Snapshot2026-11-09T17:28:37Z capture_peak24308,
pre_encode_peak30685,107encoded,0dropped,0encodeerrors,0capturelate. PTT released,
TXfalse/broadcastfalse, allreadiness true andactive. Also68remoteRXqueued with
0rejected (reverse audibility still needs explicituserconfirmation). Do not
markwholegoalcompleteyet: reverseaudio/safety/reconnectacceptance andtemporary
instrumentationcleanup remain. Node10.41.179.77 remainsconnected.

Hardware acceptance progress: user explicitly confirms website -> BS22 "Clear
voice" too, so BOTH directions passed normal app path. Disabled temporary
comms.debug and instrumentation.enable (configbackup
/etc/openmanetd/config.yml.before-diagnostic-cleanup). OFF confirmedadapteroff;
daemonrestart retainedsavedbond andkeptadapteroff. Explicit savedconnect then
Activate passedallreadiness withoutpairing (session78568). Thus diagnostics now
OFF, do not expect new /tmp/bt-diagnostics snapshots. Oldsnapshotsstillthere.
Now running setup-only reconnect session44441 (noActivate) to test physicalPTT
withoutmeshtransmission; capture notyetstarted. Need runtime safetychecks and
newuser trustintegration (manualTrustedyes stillnodeonly) beforegoalcomplete.

Physical safety acceptance:
- Setup-only test19989 observed PTT press/release, zero source-nodeRTP packets.
- Held-PTT OFF firsttest used hostfilegrowth and was inconclusive due SSHdelivery.
- Timestamprepeat49234:123packets, last1794245870.026513, OFFrequest5869.747146,
  completion5870.524941. Fileunchanged18416bytes after1sdrainand3sobservation.
  Precise nodeclockprobe bounds node-host[-0.029187,0.213533] seconds, so last
  packet latesthosttime5870.055700 < completion by0.469s. HeldshutdownPASS.
  /tmp/bs22-held-off-result.json records corrected result. Usertoldrelease.
- Radio currently OFF, retainedbond, diagnosticloggingOFF.
- Added Hardware.Trust/BlueZ.Trust called onlyafterselectedbondverified andbefore
  runtime inManager.Connect. Preventsmanualtruststep innewuserflow. Testsfailure
  preventsstartup;selectedproperty bounded5s;orderingverified. Bluetoothrace,
  commsrace,handler race,andvetpassed.
- Building trustpackage session56999 log/tmp/openmanet-bs22-trust-build.log.
  NOT DEPLOYED YET. Manually untrusted selecteddevice whileOFF toprove newcode
  restoresTrustedyes:liveBondedyes Trustedno Connectedno. Mustdeploynewbuild and
  explicitlyconnect thenverifyTrustedyes. Existing deployscript
  /tmp/deploy-bs22-mic-fix.py caninstalllatestIPK safelypreservingUCI; includes
  ready+Activate, may requireaccessoryawakening afterlongtransfer.
- Remaining: fullguidedForget/re-pair withautomatictrust onfinalbuild (needsuser
  pairingmode), finalnormalvoicecheck afterpair, review/record finalreadiness.

Trustbuild deployed successfully (session41861 /tmp/deploy-bs22-mic-fix.py),
nodeunchanged10.41.179.77 uptime2h06; dhcpconfigured1. Hardwareproof automatic
Trust: beforedeployBondedyes Trustedno; afternewdaemonConnect Trustedyes with
samebond. No manualtrustcommand sinceuntrust. CurrentlyConnectedno, buildcopy
likelyoutlastedaccessoryawakewindow; deployscriptstillready-polling90s and may
Activateifitrecovers. Userasked toenterpairingmodeforfinalguidedForget/re-pair.
Onreplyfirstpoll41861 tofinishpriorconnect; then guidedFORGET ->BEGIN_SETUP ->
DISCOVER ->CONNECT selectedMAC; verifyBondedyes Trustedyes withoutmanualchange,
ready and tone/activate asappropriate. Needfinalvoicecheck afterfreshpairing.
DiagnosticsremainOFF. Bothvoice directions/setupzeroTX/heldshutdown/savedreconnect
alreadyphysicalPASS; freshpairing onfinalautomatictrustbuild isremainingbenchcheck.

FINAL ACCEPTANCE Nov9:
- Firstfreshpairattempt foundunpairedaccessory but lostHTTPresponse; checked
  actualstate (no bond/device) ratherthanassumingfailure/success. Re-entered
  pairingmode afterForget, then DISCOVER->CONNECT succeeded.
- FinalBlueZ Pairedyes Bondedyes Trustedyes Connectedyes, storedkeyfileexists;
  adapterDiscoverableno Pairableno Discoveringno. No manualtrust used after
  untrust/freshForget. TEST_TONE andACTIVATE succeeded. Userconfirmed
  "Heard tone; voice is clear" afterfreshpair.
- Finalstatusactive=true ready=true audio/buttons/HFP=true buttonPressed=false.
  auto_enable=false, instrumentation.enable=false, comms.debug=false.
  Node10.41.179.77 uptime2h10; network-state preserved acrosspackageupdates.
- Required availablehardwareacceptance complete. Absent-controller/USBhotplug
  are automatedcoverageonly, hardwareunavailable. Earlierrebootdefault/bootoptin
  passes recordedabove. Finalchangesuncommitted inexistingworkbranches; not
  published/merged. Goal may now be markedcomplete.

## November 9: idle noise, auxiliary buttons, and revised boot preference

The user requested a new goal: eliminate BS-22 idle speaker static, support
volume buttons, and map appropriate top/side buttons to talkgroup up/down with
UI synchronization. Hardware acceptance is outstanding. Do not treat the prior
pairing/audio goal's successful tests as acceptance for these new behaviors.

The user clarified startup policy: a fresh installation must keep Bluetooth
off, but completing accessory setup should remember enablement and reconnect
after reboot. Explicit Bluetooth off should persist. Local changes now save
AutoEnable on successful Activate and clear it on Off; Shutdown preserves it
for process exit, startup reconciliation, and failed startup cleanup. Setup
alone still cannot authorize transmission. This revision is not yet deployed.

Read-only investigation: receive.go emits zero PCM after bounded concealment;
BlueALSA's SCO connection remains open while either PCM client is open. That
makes open-link/headset noise a hypothesis, not a proven cause. The user reports
less static after restarting manet02, without confirmation of silent idle.

manet02 rebooted and returned at 10.41.179.77. Its bond and trust survived, and
Bluetooth stayed off under the old preference. Explicit saved-device reconnect
and activation succeeded with audio, HFP, and buttons ready. Passive D-Bus
monitor /tmp/bs22-button-watch is running through host exec session 13209. It
has not reported volume button packets or volume property changes despite user
presses. Speaker volume currently reports 15. A brief PTT press was requested
as a positive control for this monitor; do not infer button mappings until the
monitor is validated. No auxiliary-button mapping or idle-mute fix is deployed.

Update: the PTT control check succeeded (HM 100a01011010 press,
100a01011011 release). Volume presses produced no D-Bus packets. A passive
HCI ACL monitor (/tmp/bs22-hci-watch, host session 44111) also saw no traffic
from the subsequent reported volume sequence, but did capture reconnect ATT
traffic and ReadSettings response 100a800d005000, validating its operation.
Physical button identification remains pending; requested one short P1 press.
Do not infer the button mapping from the user's earlier "volume" description.

Persistence package built successfully and was installed on manet02 without
reboot/network reset. Saved-device reconnect and Activate completed; status
reports ready, active, audioReady, buttonsReady, autoEnable all true. The new
behavior is now deployed. Reboot acceptance is still outstanding. Validation:
Bluetooth race tests passed, go vet Bluetooth/openmanet passed, all 852 frontend
tests passed, frontend lint had six existing unrelated warnings and zero
errors, targeted SettingsBluetooth lint passed, production frontend build passed.

## November 9: idle speaker muting implemented and deployed

The user confirmed that setting hardware speaker gain to zero silences the
idle static while SCO remains connected. Passive HCI confirmed +VGS:0, rather
than merely a software PCM mute. Both P1 and P2 short presses produced no ACL
control packets or volume changes; PTT remains a validated positive control.
Auxiliary controls are unresolved; do not keep assuming button labels/mappings.

Added internal/comms/audio/bluetooth_speaker.go and regression tests. For the
selected BlueALSA HFP-AG sink, the shared playback mix feeds a bounded 50-frame
onset queue. A dedicated cancellable worker restores the captured speaker gain,
waits 80 ms for the headset to apply it, then releases buffered PCM. Silence
holds gain long enough to drain playback, then requests gain zero. Commands
have 500 ms deadlines; retries do not block the audio thread. Short pauses,
onset ordering, failed-command backlog bounds, settling, cancellation and
coalesced transitions are tested with fake time. Stream teardown joins the
worker and restores the captured gain for the next session. The TX tone settle
budget includes control latency to prevent delayed tones entering the mic.

Race tests ./internal/comms/audio ./internal/comms and vet passed. Firmware
package build succeeded and deployed via /tmp/deploy-bs22-idle.py. The previous
manual zero-volume experiment was restored to 15 before installation. Saved
startup consent automatically reconnected with ready/active/audio/buttons true.
Idle volume reports zero. TEST_TONE produced +VGS:15 at 18:37:32.839 UTC and
+VGS:0 at 18:37:33.239 UTC. User tone-then-silence confirmation is pending;
first-word clarity and two-way voice still require physical acceptance. A full
reboot test of the revised persistence behavior is also still outstanding.

Protocol reference found via GitHub search:
https://github.com/SarahRoseLives/VGC_VR-N76_Firmware/blob/master/APP/base/smali/N0/k.smali
It names HM READ_SETTINGS=13, WRITE_SETTINGS=14, SET_BLE_AUDIO=15,
KEY_EVENT_IND=257. No settings writes or speculative commands were issued.
Further auxiliary-button investigation may follow this source; do not re-enable
SET_BLE_AUDIO=1, which the previous controlled microphone tests proved harmful.

User confirmed the repeated test tone was audible and followed by silence.
Both nodes are still configured for channel 1 TX/RX; BS-22 readiness and saved
startup consent remain true. Live website-to-BS-22 first-word clarity and return
to silence test has been requested. Tone acceptance alone does not complete the
voice/noise, auxiliary-button, or reboot acceptance checks.

User confirmed "Clear voice, then silence" after replay of 98 known healthy
browser RTP packets directly to manet02 with the idle gate installed. This is
physical acceptance for received recorded voice and return to idle silence;
it does not yet prove a fresh live browser-to-radio test or reverse microphone
transmission after the change. Test tone was also confirmed audible then silent.

User confirmed reverse-direction physical acceptance with the idle gate:
"Clear on website; BS-22 silent afterward" after holding hardware PTT, waiting
for its tone, speaking, and releasing. The received recorded voice, intentional
tone, microphone-to-website audio, and return to idle silence have now passed
listening checks. Auxiliary volume/channel buttons and full reboot acceptance
remain outstanding; do not mark the overall goal complete.

Full reboot performed after the reverse voice test. A different kernel boot_id
was verified on manet02 at its saved IP. Without BEGIN_SETUP/CONNECT/ACTIVATE,
status became ready/active/audioReady/buttonsReady/autoEnable=true. A test tone
was then sent; physical post-reboot tone/silence confirmation is pending.
Reboot terminated the passive monitors and removed their /tmp binaries; re-upload
and restart them before asking for further button tests. Previous exec handles
13209 and 44111 must not be treated as current listeners.

User confirmed post-reboot "Heard tone, then silence". Automatic saved-accessory
reconnect across a real reboot and post-reboot idle muting now have physical
acceptance. Auxiliary volume/channel buttons remain unresolved.

## Temporary auxiliary-control experiment IN PROGRESS

The post-reboot '+' short press also produced no HCI ACL traffic. Reference
app N0/a.smali initialization explicitly sends SET_BLE_AUDIO=1. Its HM event
handler recognizes key IDs 0x13/0x14 with event 0x12 for volume actions (not yet
validated on this physical device). To test whether mode controls event
reporting, a reversible diagnostic is running; production code still never
sends command 15. User was told this alternate mode previously suppressed HFP
microphone and mesh TX is disabled during the comparison.

Host exec session 51962 runs /tmp/bs22-mode-buttons.py. It reconnects in setup
(active=false verified), writes mode1 via /tmp/bs22-audio-mode, and waits up to
10 minutes OR until host file /tmp/bs22-mode-buttons-finish exists. Its finally
writes mode0, reconnects, waits ready, and activates normal HFP audio. Finish
this experiment by creating that host flag, then join session 51962 and verify
restoration. If restoration fails, handle it before asking for voice tests.

Host exec session 20474 is the live post-reboot HCI ACL monitor. Mode1 write
100a000f01 received response100a800f0000 at19:15:42UTC. Asked user: short P1,
wait2s, short '+'. Awaiting reply and actual packets. Do not mistake setup-mode
microphone suppression for a new regression; do not leave mode1 installed.

Alternate-mode comparison finished: user pressed P1 then '+', but HCI monitor
reported no control packets. Flag /tmp/bs22-mode-buttons-finish was created.
Session51962 wrote mode0 and successfully reconnected/activated normal HFP
(terminal exit0, ready). No production mode1 change was made.

Reference APK downloaded read-only from pinned public repository commit
0fd3be0f2459e192a01ffca050e3b1e164a79563 to /tmp/bs22-reference-app.apk because
published smali loses case-colliding classes. Androguard installed in isolated
/tmp/bs22-apk-tools (venv bootstrapped using get-pip; host Python unchanged).
/tmp/bs22-dex-inspect.py dumps targeted classes. LK1/N settings has one boolean
field d; its meaning is not yet known. /tmp/bs22-dex-references.py searches
instruction references; currently searching LK1/N;->d in exec20798, output
/tmp/bs22-setting-refs.txt. Do not write unknown settings bits speculatively.

Read-only programmable-key queries succeeded on the selected HM write
characteristic, using standard command group2: GET_PF=55 and GET_PF_ACTIONS=75.
Responses (including vendor, response command, status):
00028037000600081504170212160018161400120326002808240022003600380734003200
0002804b00010217130c1112030405060f07080a0b15160910
GET_PF records are two bytes: high nibble of first byte=key index, low nibble
=event type, second byte=action. Event8 is SHORT_SINGLE, event6 LOW_TO_HIGH,
event4 DOUBLE, event2 LONG. Current short-single actions: key0 volume-=21,
key1 volume+=22, key2 nextchannel=8, key3 prevchannel=7. Mapping to physical
keys follows manual, but actual host events still absent. No key settings have
been written. Action0 is Disable; supported action list is recorded above.

Original APK class LK1/N's sole settings bit is ICOM mode, confirmed by binding
LC1/J fieldi -> resource id0x7f09022b name icom_mode. Do not assume it controls
key forwarding. Reference enum action65535 is not documented as host mode.
Inspecting SET_PF serialization via /tmp/bs22-dex-references.py, exec13867,
/tmp/bs22-key-write-refs.txt. APK original supplies case-colliding classes missing
from published smali. Source URLs and APK tooling above remain applicable.

SET_PF serialization traced in APK LY1/y.t5: sends the entire action array in
existing GET_PF record order, NOT (key,event,action) pairs. No SET_PF was sent.
The LL1/b boolean override only disables conflicting click types when a
press-down action exists; it is not a host-forwarding switch. Avoid assuming
any unknown action enables raw events.

Additional read-only initialization queries sent via /tmp/bs22-read-keys:
GET_DEV_INFO(group2,command4,payload03) response
000280040006020401003600000000;
GET_VOLUME(group2,22) response0002801601 (non-success status1).
Current physical monitor20474 is live. Requested one P1 press after these
queries to check whether app identification is needed before event forwarding.
Normal HFP mode, active mesh session, and idle gate remain restored. No key
assignments or persistent accessory settings were changed.

Active-audio button comparison: user reports neither P1 nor volume-down
changed the repeating voice. HCI20474 captured only host VGS15 then VGS0;
no accessory button traffic. Playback78821 has stopped, stop flag exists,
and BlueALSA sink volume verified0. No production changes during this test.

Important monitoring gap found: passive HCI/DBus cannot see notifications
that the peripheral has not been asked to send. HM1102 was subscribed, but
Nordic UART6e400003 and FF14 were not. Temporary /tmp/bs22-all-notify.go
compiled ARM64 and started on manet02 in exec22363. StartNotify succeeded
on all three characteristics, and prints matching-device Value events.
DBus connection close removes this observer's subscriptions; production HM
subscription remains separately owned. No persistent device settings changed.
Asked user P1 once, wait2s, minus once. Await reply; inspect22363 and20474.
Do not claim the additional services carry buttons until actual evidence.

All-notification comparison result: user completed P1 then minus, but22363
saw no Value events, and20474 saw only successful CCC subscription requests
and responses. No button traffic. Reference Ln0/f$b BLE initialization only
subscribes HM characteristic and keeps its write handle; no additional
control-characteristic mode write found. SET_NET_STATUS enum has no usages
outside enum initialization in inspected APK, so no speculative command sent.

Device info decoding verified against original APK LK1/o and big-endian
Lo2/t: vendor06, product0204, hardware01, firmware0036 (reference display
format0.3.6). This alone DOES NOT establish latest firmware or explain missing
buttons. Reference app gates READ_SETTINGS at0x54, but the tested accessory
answers that command despite reporting0x36. Avoid overclaiming compatibility.
Requested whether user has BTECH phone app available for official-app button
comparison and update-availability check. Leave manet02 active until reply;
no firmware flashing or device-setting changes performed. Idle volume remains0.

APK identity correction and deeper review (user explicitly requested download
and inspection): previous reference is HT2.7.10.0 / versionCode2071000,
package com.benshikj.ht, SHA256
4e5144aaf46a85e4175d480f38a94f5c02b515f259c4ac55b0af6fcc091d5a3d.
It is NOT the BTECH-branded APK. Google Play lists BTECH UV Programmer as
com.benshikj.ht.btech.ham. Attempts to download its binary from APKPure,
APKCombo and alternate mirrors failed (403/download unavailable); no branded
binary acquired. Do not present HT review as verified current BTECH behavior.
Stable evidence copied to /home/jmcgeeal/Desktop/bs22-apk-review/.

Whole-DEX references to Ln0/m found only Ln0/a's event handler and LG1/o's
PTT listener, aside from the data class itself. Ln0/a handles key0x13/0x14
with event0x12 by changing Android volume, and key0x10 down0x10/up0x11
for PTT. No channel-up/down handler was found in those key-event consumers.
SET_PF references resolve to programmable-key settings-save UI LY1/y.t5,
not automatic connection setup. Bluetooth callback enables HM notifications;
no hidden extra GATT configuration write found there. These findings narrow
the search but do not explain absent hardware events conclusively.

User has no phone available. Proposed audio-closed/button-only comparison
has NOT been performed: user steered to APK review before any interruption.
Normal daemon/audio connection preserved. All-notification observer22363 and
HCI20474 still running. No firmware flashing or production code edits during
APK review.

ACTIVE TEST: audio-closed comparison, host exec84879 running script
/tmp/bs22-no-audio-test.py. Session placed in setup/inactive before stopping
/etc/init.d/bluealsa. Verified pidof bluealsa absent and GET_DEV_INFO response
received with BLE still connected. All three notifications subscribed, fresh
observer exec41996 (older22363 also alive), HCI20474. Asked P1, wait2s, minus,
then brief PTT control check. Await reply.
To finish IMMEDIATELY create /tmp/bs22-no-audio-finish on host; script finally
starts bluealsa, reconnects via setup, waits ready, ACTIVATE, logout. Ten-minute
automatic recovery limit from~19:52:35UTC. Must inspect84879 completion and
confirm normal audio restoration. Earlier57989 experiment restored early due
to grep exit1; fixed check and retried, not a hardware failure. No production
code changed. User authorized this test after APK/plugin comparison.

Audio-closed test84879 timed out at20:02:35UTC and restored audio successfully
before user replied done. Neither P1/minus nor PTT control events captured;
result INCONCLUSIVE, cannot infer buttons absent with audio closed.
Rearmed script in exec23464 with no recording deadline (waits host finish flag
/tmp/bs22-no-audio-finish). Must create flag after user test, join23464 and
verify restoration. Observers41996/22363 and HCI20474 remain running.

No-deadline audio-closed23464 completed: user did P1/minus/PTT, observer41996
reported no button events at all (including PTT). Thus no positive button
control; cannot conclude audio-open state is or is not suppressing auxiliary
buttons. Host finish flag created,23464 terminal exit0 RESTORED. API verified
ready/active/audioReady/buttonsReady true. Now requested one large PTT press
in normal mode and whether start tone heard. Continuous41996/HCI20474 live.
No ongoing audio-stopped experiment or production changes remain.

Normal-mode PTT control succeeded on wire at20:19:00/01.65 and20:19:04.53/05.07:
HM100a01011010 down /100a01011011 up. User heard no tone. Observer showed
speaker Volume=0 even at tone onset. Recovery defect: bluetoothSpeaker captures
observed PCM Volume when opened; stopping BlueALSA while idle-muted prevents
close restoring original gain, so next open learns0 as playback gain.

Fix in internal/comms/audio/bluetooth_volume.go: atomically preserve selected
accessory gain in /etc/openmanetd/bluetooth-speaker-volume.json before applying
idle mute; same identity observed0 recovers saved gain, different accessory
never inherits another's gain. Nonzero observed changes update saved gain.
File errors fail initialization before mute; mutex serializes startup accesses.
Tests cover muted recovery, changed nonzero gain, identity isolation, failed
save/corrupt file. audio race tests and vet pass. Build exec27859 running,
log/tmp/bs22-volume-recovery-build.log. FeedMakefile package-local source override
restored by finally. Deployment helper/tmp/deploy-bs22-volume-recovery.py seeds
KNOWN pre-experiment gain15 (packed3840) for this BS22; this is restoring bench
state, not a generic zero-to-max policy. Yet to deploy/hardwareverify.

Volume recovery fix deployed successfully. Initial build27859 hit disk-full at
link; removed rebuildable host Go cache via go clean -cache (freed2.5GB), retry
3038 succeeded. FeedMakefile restored. Deployment91213 exit0, saved UCI retained,
ready/active/audio/buttons/autoEnable true. Seeded only bench accessory's known
previous gain15 (3840). /tmp/bs22-check-volume-recovery.py exec24467 reproduced
BlueALSA disappearance with idle Volume0 and saved gain3840, restarted service,
reconnected/activated, tone sent, idle0 restored, exit0. Observer41996 confirms
20:25:02.564 Volume3840 ->20:25:02.964 Volume0. Awaiting user's audible confirmation
via async question. All experiments restored; runtime active. Buttons other
than PTT still unresolved. No claim of full goal completion.

User physically accepted volume recovery: "Heard tone, then silence".
NEW ACTIVE REVERSIBLE P1 TEST: P1 short-single built-in assignment disabled
(group2 SET_PF56). This IS a temporary persistent accessory configuration
change; MUST RESTORE after test regardless of result. Current helper
/tmp/bs22-p1-assignment on manet02 accepts only disable-p1 / restore.
Restore command: /tmp/bs22-p1-assignment restore ; /tmp/bs22-read-keys keys.
Original GET_PF packet00028037000600081504170212160018161400120326002808240022003600380734003200.
Original16 action bytes00 15 17 12 00 16 00 03 00 08 00 00 00 07 00 00.
Changed only actionindex1 from21(volume-) to0(disabled), not PTT or other keys.
ACK0002803800 successful20:27:04UTC and GET_PF confirms exactly0800 vs0815.
Currentpacket00028037000600080004170212160018161400120326002808240022003600380734003200.
User prompt: P1 once then brief PTT control, no deadline. Await reply. Normal
audio/mesh active, observers41996/22363 and HCI20474. Hypothesis only: built-in
action may consume event; do NOT assume disabling emits raw events. If absent,
restore and record failure, do not leave P1 disabled. Hosthelper source
/tmp/bs22-p1-assignment.go uses known APK SET_PF action-array serialization.

P1-disabled test completed: no P1 event; PTT down/up captured20:29:17.381/17.650
with Volume3840 then0 for tones. Thus positive control and working speaker,
but disabling built-in P1 action does NOT expose its event in current mode.
RESTORED original assignments via /tmp/bs22-p1-assignment restore, ACK0002803800
at20:29:33.283; GET_PF exact match to original at20:29:33.342:
00028037000600081504170212160018161400120326002808240022003600380734003200.
No temporary accessory assignments remain. Volume/channel goal unresolved.


## BTECH APK supplied on Desktop — static analysis correction
Inspected base.apk, package com.benshikj.ht.btech.ham, version 2.9.2.1
(code 2090201), SHA256 cba8547d907b33bd4f36dc6ed0058b4b59aa70c4edde4cb9b6c57cc5174f0b40.
No accessory or daemon settings changed during this analysis.

CORRECTION: Earlier notes calling HM key IDs 0x13/0x14 volume are wrong.
BTECH Ly1/a.G dispatches key 0x13 event 0x12 to Ln4/d.c(context,true),
and 0x14 to the same helper with false. Tracing that helper proves these
select next/previous connected radio link or enabled IIChannel, wrapping
and announcing the selected name. Key 0x1e event 0x12 calls Ln4/d.b to
join/select a channel; it is NOT mute. Old HT LD1/d.c also selects channels,
so earlier claims that HT lacks channel handling are also superseded.

Classic headset +XEVENT handling (Ln4/d and Ln4/d$a) recognizes PTT_DOWN,
PTT_UP, NEXT_CH, PREV_CH, JOIN, PLAY_LAST, BLE, BSHM. This establishes app
support, not evidence that this BS-22 emits those commands in current mode.
Physical captures so far show PTT but no auxiliary button events.

New HM settings Lv4/j0 has four bits d/e/f/g. UI Lk5/a0.G5 and binding
Lm4/k0 prove d is id/icom_mode (2131296845), f is id/yaesuMode (2131297706).
The corresponding e/g bits gate visibility, with a product override.
The two enabled mode flags are mutually exclusive. These are radio
compatibility modes, not an identified enable-button-reporting switch.
Do not write unknown settings bits based on the four-bit difference.

BTECH startup Ly1/a.E reads settings then sends SET_BLE_AUDIO(1).
This remains a diagnostic difference, NOT a recommended production change:
prior bench comparisons found mode 1 broke the working microphone path.
Stable disassembly evidence: /home/jmcgeeal/Desktop/bs22-apk-review/btech-*.txt
and ht-control-actions.txt. Volume/channel-button goal remains unresolved.


Active channel-announcement comparison (2026-11-09 20:50 UTC):
manet02 10.41.179.77 status active/ready/audio/buttons/HFP/autoEnable true.
Current production sources only interpret PTT, but previous raw captures
also lacked auxiliary events, so parser additions alone do not solve it.
BTECH Ly1/a.onMessageEvent(Lv4/u$c) calls c0(current link name), which
sends HM vendor0x100a command9 UTF-8 SET_SESSION_NAME. Diagnostic helper
/tmp/bs22-session-name.go compiled and copied to manet02 /tmp/bs22-session-name.
Sent 'set' => 'OpenMANET Ch1' at20:50:21; ATT write acknowledged, no HM
response seen yet. No BLE audio mode/key-assignment changes.
After this physical comparison MUST clear temporary name using
/tmp/bs22-session-name clear (command9 empty payload, matching app null-name).
Own continuous observers: exec43314 DBus tee /tmp/bs22-session-name-notify.log;
exec87131 HCI saved /tmp/bs22-session-name-hci.log (paths on host).
Asked user short +, short P1, brief PTT control; await input, no timeout.
Hypothesis only: announcing current channel might enable auxiliary controls.
Do not describe ATT write acknowledgement as successful application handling.

Channel-name comparison completed: user confirmed all presses. Raw HCI
and all-notification capture show only PTT at20:51:04.040 down/04.310 up,
plus normal host VGS15/0 tone gating. No + or P1 event. Cleared command9
name at20:52:51; queried keys and verified original GET_PF packet unchanged.
BTECH SET_NET_STATUS enum references scan found only enum initializer/array;
no app caller located, so no guessed network-status payload sent.
Now tracing APK firmware availability lookup (read-only; no flash planned).

Firmware lookup traced: Lv4/a0.x calls benshikj.DeviceManagement/CheckFirmwareUpdate
at rpc.benshikj.com:800 over TLS. ProductId field1=516 (0x204), firmware
field2=54 (0x36). Anonymous stable request bytes0884041036 with hl=en
returned successful empty protobuf (zero bytes). No firmware offer without
account; not proof newest firmware or cause. No flash/download performed.

NEW ACTIVE TEST: close only OpenMANET audio streams, KEEP BlueALSA/HFP.
Host script /tmp/bs22-idle-link-test.py exec10347 running, waits without deadline
on /tmp/bs22-idle-link-finish. Original comms config saved host
/tmp/bs22-idle-link-original.json = enabled true, CONTROL_SOURCE_BS22.
Script began setup/reconnected ready then UpdateCommsConfig enableComms=false
at20:55:32. Bluetooth allowed/connected and autoEnable true; active false.
Bluealsa-cli confirms HFP-AG sink AND source present, sink Running=false,
CVSD8000 volume15 (restored by close). Device-info query responded.
Continuous observers43314/87131 still log to session-name-* files.
Requested user +, P1, brief PTT. No expected mesh audio/tone.
MUST RESTORE by creating /tmp/bs22-idle-link-finish; script finally restores
original CommsConfig, connect/ready/ACTIVATE, logs RESTORED. Then verify
audio ready and idle volume0. Unlike previous bluealsa-stopped test, classic
HFP remains connected. Do not leave comms disabled after this test.

Audio-closed/HFP-connected test returned three distinct raw PTT pairs at
20:56:13.251/13.521,20:56:15.081/15.291,20:56:36.291/36.741; no auxiliary
messages. User heard a beep. Asked which physical buttons produced these
(three presses vs repeated PTT) and which beeped. Awaiting clarification.
Restore script10347 failed enabling directly: start Bluetooth setup first.
Configuration enable=true persisted; manually connect/ready/ACTIVATE exec88627
restored status active/ready/audio/buttons/HFP/autoEnable true.

NEW CONCRETE CODE FINDING: raw HFP AT+CLIP=1 at20:55:23.368 had no reply.
BlueALSA4.3.1 ba-rfcomm.c forwards all non-predefined commands to RFCOMM1.Open
owner; unknown commands receive ERROR only if no external handler exists.
Our consumer treated that duplex handler as read-only, suppressing replies
to unsupported commands. CLIP has no built-in handler. It may stall optional
headset setup; causation of auxiliary-button failure NOT YET proven.
Implemented limited replies in bluetooth_event_source.go: AT+CLIP* gets
ERROR (no caller ID service); AT+XEVENT= gets transport OK independently
of dedupe. Built-in AT commands remain untouched to avoid double replies.
Bounded500ms write deadline on os.File; monitor cancellation closes descriptor.
Tests cover CLIP, XEVENT, builtin silence, case, failure, consumer PTT-only
output despite channel command. control package race tests and vet passed.
Firmware build exec57785 logs/tmp/bs22-rfcomm-reply-build.log, temporary
package-local USE_SOURCE_DIR override restored in finally. Not deployed yet.

RFCOMM reply fix first build57785 and deployment97985 succeeded. Userconfig
preserved, status active/ready/audio/buttons/autoEnable true, sink idleVolume0.
RAW HARDWARE EVIDENCE21:00:55: AT+CLIP=1 -> ERROR; then previously absent
AT+XAPL, AT+CNUM, AT+BIA, AT+VGM, AT+VGS, AT+IPHONEACCEV,
AT+XEVENT=BSHM:2.1.1:le -> OK. This proves optional setup progresses after
CLIP reply. Button fix still unverified. Distinguish BSHM2.1.1 announcement
from group2 firmware0.3.6; do not assume same component/version scheme.
CNUM newly exposed, no builtin handler or reply; added explicit ERROR for
subscriber-number request as we aren't a telephone gateway. Tests47742,
second build51003 /tmp/bs22-rfcomm-reply-build2.log underway.
Awaiting physical clarification from prior question (3PTT pairs, which beeped).

Second reply build51003 and deploy65393 succeeded. Packet trace21:03:27
confirms CLIP=1 ->ERROR, CNUM ->ERROR, XEVENT=BSHM:2.1.1:le ->OK;
normal BIA,VGM,VGS,IPHONEACCEV traffic now present. Normal audio restored
ready/active/audio/buttons/autoEnable true; HFP-AG CVSD8000 running,
sinkVolume0. No temporary assignments/mode/name/disabledcomms remain.
Latest physical prompt asks ONLY + once (no P1/PTT), continuous monitoring,
to isolate event identity after handshake fix. Await response. Old question
about threePTT pairs/beep still unanswered; don't infer physical mapping.
Logs remain /tmp/bs22-session-name-notify.log and -hci.log, own monitors
exec43314 and87131. Tests race+vet passed. No commits/push.

BREAKTHROUGH auxiliary event after handshake fix: user reported pressing
only '+' once. Raw HCI21:04:48.034437 peripheral -> AT+XEVENT=PREV_CH\r\r,
host OK21:04:48.034852. No BLE key event. This is FIRST captured channel
button event; transport is classic RFCOMM, not HM. Need physical mapping
confirmation (reported plus vs semantic PREV). Prompt now pending ONLY
minus once, no P1/P2/PTT. Do not invert mappings based on assumption.
Existing emitEventName currently logs/ignores NEXT_CH/PREV_CH; no channel
selection implementation deployed yet. Service.SelectTalkGroup already
provides exclusive RX+TX and publishes UI events. AuxEventSource separate
channel exists for OpenVLM volume; Bluetooth sources need equivalent aux
forwarding and consent-gated channel dispatch. Keep auxiliary events out of
PTT enum/state machine. Source attribution enum currently RPC/GPIO/Init;
API mapping numerically pins these, so adding Bluetooth requires checking
proto enum vs conversion (do not directly edit generated files).

Second isolated channel press: user reports '-' only; raw HCI21:10:28.235696
peripheral AT+XEVENT=NEXT_CH, host OK21:10:28.236230. Both semantic
channel events now captured; physical reported labels reversed (+PREV/-NEXT).
Do not silently swap firmware-assigned semantics. Need final user direction
acceptance after implementation. Latest pending physical test is P1 ONLY
(short press), no other buttons, to identify volume path after setup reply fix.
Normal audio active/idle-mute retained. No new channel implementation yet.
Implementation design: extend optional AuxEventSource for semantic next/prev
(not PTTEvent), forward from selected RFCOMM source through BS22 source;
consume consent-gated in comms and call existing Service.SelectTalkGroup so
exclusive RX/TX and web streamed state stay synchronized. Wrap among actual
provisioned ports. Add Bluetooth source enum in proto/source then regenerate
(not edit generated). Need session/held-PTT and lifecycle tests.

Isolated P1 at idle after handshake fix: user done, no new raw HCI or
volume event. Sink was0 (idle gate), so volume-down floor may explain it;
not sufficient evidence of dead button. Started repeat recorded voice
/tmp/bs22-volume-listen.py exec53332, max5minutes, stops via host flag
/tmp/bs22-volume-listen-stop. Asked P1 THREE short presses while audible,
report quieter/no voice. MUST stop playback when user replies by touching
flag; then verify sink returns0. Existing HCI/DBus observers still live.
No additional production changes this turn. Channel next/prev identified
but integration pending; need volume event path to complete cohesive changes.

LIVE BREAKTHROUGH during P1 playback test: before user text reply, monitor
shows accessory AT+VGS decrementing15->14->13->12 (21:12:45-46) and
subsequently down to7 at21:12:53.136. BlueALSA ACKs and sink Volume tracks
3584,3328,3072,etc. This is real HFP absolute speaker-gain control, not HM
key event. Playback stopped by flag at~21:12:55, exec53332 done. Await
user audible report. Need integrate preferred-volume tracking from received
AT+VGS ONLY (avoid treating host idle-mute Volume0 as desiredvolume).
Current gate still caches original15; production persistence must remember
remote chosen7 (or latest) across nextreceive/restart, and allow intentional0.
P1 idle-at0 produced noevent because floor; demonstrates why idle-volume
control needs design, cannot just listen to hardwarevolume.

USER OVERRIDE: wants +/- volume, P1/P2 previous/next channel. User confirmed
voice got quieter in P1 playback test. New persistent accessory assignment
is explicitly authorized; do NOT restore factory map unless testing requires
rollback. Applied /tmp/bs22-button-mapping openmanet21:17:05, ACK0002803800.
Readback00028037000600080704170212160018081400120326002815240022003600381634003200.
Short-single assignments: key0(P1)7prev, key1(P2)8next, key2=21vol-,
key3=22vol+. key2/key3 orientation follows USER reported minus->NEXT(old8),
plus->PREV(old7) earlier, needs physical verification due apparent reversal.
All other records unchanged (long/double/PTT). Source helper
/tmp/bs22-button-mapping.go; node binary same stem. 'restore' restores factory
map only if needed; 'openmanet' applies current requested mapping.
Latest pending prompt: P1 once wait2s P2 once, no other keys, verify NEXT/PREV
XEVENT. Website WILL NOT switch yet; semantic channel integration pending.
Normalaudio active, no playback loop or temporary mode running.
Important idle-volume design remains: device volume-down at hardware0 has no
event. New physical +/- assignments still use HFP VGS absolute levels;
need solve idle volume changes and preferred gain persistence, not claimdone.

P1/P2 mapping correction: two-press capture21:18:46 NEXT then21:18:49 PREV
was opposite requested order. Swapped key0/key1 single assignments.
Current GETPF00028037000600080804170212160018071400120326002815240022003600381634003200
ACK21:19:18, readbackverified. User then isolated P1, explicitly upperLEFT
facing FRONT; raw21:19:32.349229 AT+XEVENT=PREV_CH ->OK. PhysicalP1
confirmed PREV now; key1 maps to user-describedP1. Key0 nowNEXT(P2pending).
Volume assignments key2=21down key3=22up still pendingphysicalverification.

Implemented channel integration: new control.ChannelEventSource optional
<-chan int (+1/-1), bounded8events nonblocking alongside separate PTT.
BlueALSA dispatches NEXT_CH/PREV_CH; BS22 forwards selected fallback channel
and closes it after transportworkerjoin. Comms Run consumes channel events
on same loop as PTT, cfg.stepBluetoothChannel gates on activeconsent and
!Broadcasting; wraps among actual provisionedports and calls existing
Service.SelectTalkGroup (exclusiveRX/TX+UIevent). API source0 unspecified,
not falselyGPIO/RPC; no proto changes or generatededits.
Tests added for channel-not-PTT, boundedqueue allowsPTT, next/prev/wrap,
setup/heldPTT/invalidstep ignored. Race tests entirecontrol+comms andvetpassed
73078/60086. Firmwarebuild84645 passed, log/tmp/bs22-channel-build.log;
feedMakefile finallyrestored. Deploy84942 inprogress via existing safe
/tmp/deploy-bs22-rfcomm-reply.py (filename reused, newestIPK).
Plan setmanet02channel2 and askP1 to verifygoes1 withUI sync.
Volume preferredgain/idlefloor remains unfinished; do not claimgoalcomplete.

Channel integration deployed84942 successfully. Runtime readyactiveautoEnable
true, HFP CVSD8k running, idlevolume0. Set manet02 channel2 via normal
SelectTalkGroup API, verified onlychannel2 RX/TXenabled. Latest pending
physical prompt P1 once -> expectchannel1, optionallyverify manet02UI
(not manet01UI). Check rawHCI then GetCommsStatus after userreply.
No recordingdeadline; hardwarecontrol daemoncontinues indefinitely.

USER confirms P1 channel change worked (21:24), but +/- volume not working.
Raw trace confirms remote AT+VGS=1 at21:24:10 then channel announcement
restorescached15; existing gate never updatespreferredgain. Also silent
hardware0 meansvolumedown emitsnoevent, so just trackingVGS is insufficient
for full idlecontrols. Do not claim fixed by updatingpreferredvolumeonly.

NEW ACTIVE REVERSIBLE volume-events experiment: decoded official APK action
labels from LL1/b staticresources:10prevchannelGROUP,11nextchannelGROUP,
21vol-,22vol+. GET_PF_ACTIONS advertises10/11. TryingnonTX groupactions
on +/- to obtain separate raw events, which hostcouldmap tovolumesteps.
P1/P2 actions key0=8/key1=7 unchanged. Appliedhelper 'volume-events'
21:27:22 ACKsuccess/readback:
0002803700060008080417021216001807140012032600280a240022003600380b34003200
Currenthelper /tmp/bs22-button-mapping.go and nodebinary supports
openmanet (restoreUSER mapping), volume-events(temporary), restore(FACTORY).
MUST restore USER mapping with '/tmp/bs22-button-mapping openmanet' after
experiment unless testprovescandidate anddeliberatelyintegrated. Do NOT
use 'restore' factory mapping. Lastuserprompt minusonce wait2s plusonce;
expectno actualvolumechange duringdiagnostic,continuousmonitors43314/87131
logs /tmp/bs22-session-name-{notify,hci}.log. NeedinspectnewXEVENTs and
restoreuserassignments. No productionvolumechangeswritten yet.

Volume-events experiment FAILED: user minus/plus produced no rawHCI event
with groupactions10/11. RestoredUSER 'openmanet' mapping21:28:23 ACK/readback
00028037000600080804170212160018071400120326002815240022003600381634003200.
No temporaryassignments remain.

Implemented first real volume-event integration (not full idleminus solution):
control.SpeakerVolumeEventSource optional <-chanint; parser accepts only
accessory AT+VGS=0..15, excludes outgoing +VGS:0 andmicrophoneVGM.
BlueALSA ownqueue16, BS22 forward+joinclosure, mainRun selectedconsentgate
forwards to first available playback controller once (shared sink).
sharedPlaybackPort forwards to sharedspeakerStream; SetBluetoothVolume
boundedqueue to speakerGate worker, noIO inPTT/audio callback. Worker
updates preferredpackedgain, forcesphysicalapply evenifidle stateunchanged
(remutes idleup). s.volume onlyworkerowned, cleanupafterjoin.
Explicit writeBluetoothVolume(...recovering=false) persistsintentionalzero
withoutstartupmuteheuristic. set stillphysicalmutes ifpersistfails.
AudibleVGS isabsolutechosenlevel; idlepositiveVGS treatedrelativefrom0
andaddedtochosenlevel capped15. Idleminusat0 sendsNOTHING andREMAINS
UNSOLVED; do notclaimfullgoal. Preferredgain startuphandshake VGS15
may needfilter untilBSHM announcement toavoidclobberingretainedlevel;
investigate before rebootacceptance.
control+audio+comms wholepackagesrace andvetpassed11689; finalaudio
storage-errorhandlingadjustmentrace/vetpassed5194. Build99064 running
/tmp/bs22-volume-buttons-build.log; feedMakefileoverridefinallyrestored.
Notdeployed yet. Needtest +/minus duringplayback, nextburstretainslevel,
idlequiet, rebootretention; selectedchannel maynot1afteruserchanneltests.

Startup gain concern addressed BEFORE deployment: consumeRFCOMM ignores
AT+VGS until accessory sends AT+XEVENT=BSHM: identification. Existing raw
trace proves startupVGS precedesBSHM; subsequentcommands useradjustments.
Regression ensuresstartup15/outgoingmute0notemitted, subsequentuser7 emitted.
Tests22505, secondbuild9193 /tmp/bs22-volume-buttons-build2.log underway.
Firstvolume build99064 passedbutNOTdeployed; use newerbuildaftercompletion.

Volume-event update build9193 passed, deploy58304 passed. Status readyactive
audio/buttons/autoEnable true; idleVolume0. Controlstartupfiltertests22505
race+vetpassed. Node daemon23674. Setchannel1, started repeatedvoice
exec56930 /tmp/bs22-volume-listen.py (max5min). STOP via touch
/tmp/bs22-volume-listen-stop; MUST stopwhenuserreplies. Latestprompt
ONLY minusTHREE presses duringaudibleplayback, leavebuttonsreleased;
askquieter andcheck /etc/openmanetd/bluetooth-speaker-volume.json
(nonsensitivegainonly) +rawVGS/currentVolume, then replayafteridletoverify
retention. Existingrawlog /tmp/bs22-session-name-hci.log andnotify.log.
Idleminusat0stillunsolved, do notclaimfullgoal. Alternatekey experiment
fullyrestored USERmapping; P1 channelpreviousconfirmedphysicalandUI.

IMPORTANT revised volume evidence: user confirmed '-' THREE duringplayback
madevoicequieter, but rawHCI contained NO VGS andsavedhostgainremained15.
Do not claimhostpreferredgain changed. Repeatvoice afteridle user explicitly
said 'Still at the quieter level' despite hostVGS0->15. This suggests an
independent accessory-local volume level for remapped +/- actions. Earlier
claim idleminus impossible applies onlyobservedHFPcontrolpath, NOT yet
proven for accessory-localvolume; testidleminus audibly beforeconcluding.
Repeatedvoice exec62168 +THREE user says 'seems to' getlouder; stopped
byflag now. Needconfirmquietidle and idle-adjustmentretained audibly.
No changes to assignments/code this turn; stableUSER GETPF verified
21:36:05.999. Existing gain-eventintegration remains usefulforHFPVGS but
not source of remappedlocal-volume success. No claimonpowercyclelocalgain
retention yet.

2026-11-09 volume evidence correction: the earlier separate accessory-local
volume inference is NOT established. Raw HCI for the requested + three-press
test shows VGS 15->14->13->12, saved packed gain3072: assignments were reversed.
Corrected persisted key2 action22(volume up), key3 action21(volume down);
P1/P2 channel assignments unchanged. GET_PF at21:37:42 readback:
00028037000600080804170212160018071400120326002816240022003600381534003200.
After correction user pressed only + once during playback: incoming AT+VGS=13
at21:38:25.744 UTC, saved volume3328 (13). Playback session94232 stopped
after reply. This confirms corrected physical + and host saved gain.
Corrected physical minus and idle-minus remain to verify; no claim of
reboot retention for this latest volume implementation yet.

Corrected physical minus confirmed: user isolated minus press during playback produced AT+VGS=12 at21:40:16.865 UTC, from prior13. Saved packed gain3072. Playback89554 stopped after reply. Both corrected physical +/- now verified during audible playback. Next test: idle minus, no playback, continuous HCI monitor. No claim idle-minus works yet.

Idle minus test after corrected mapping: user replied done, raw HCI contains no new AT+VGS or other button event after21:40:29 host mute0; saved gain remains3072(level12), physicalsink0. Volume-down at muted zero is therefore not functional in this tested state. Both directions during playback are proven. Pending user test idle + once, continuous monitors. Do not mark whole goal complete.

Idle plus CONFIRMED: user + once produced AT+VGS=1 at21:41:18.455 UTC; host remuted0 at21:41:18.459. Saved gain changed12->13 (3328), sink0. Idle minus remains no-event at floor0. Reboot initiated to verify latest volume persistence/autoreconnect; preboot ID5097ea6e-5954-4e41-a3ae-342d7495c8b9. No manual Connect action used.

Reboot test succeeded: boot ID327597e7-9adc-42ec-98d1-6dddcd8bd5e5. Status available/enabled/active/ready/audioReady/buttonsReady/hfpReady/autoEnable true; paired connected BS-22. No manual connect. Saved gain3328(level13) survived, idle physical0. TEST_TONE requested successfully; awaiting user heard-tone-then-silence confirmation. Prior raw/DBus monitor SSH sessions ended on reboot and MUST be restarted before more physical event tests. Idle-minus unresolved.

User confirmed post-reboot tone then silence. Investigating idle-minus via closing audio link while preserving listening gain. BlueALSA source rfcomm_notify_volume_change_spk serializes gain only (mute bit not remote amplifier command). ACTIVE experiment77316 runs /tmp/bs22-idle-link-test.py, streams disabled, HFP connected. Raw HCI18786 /tmp/bs22-idle-volume-hci.log. RESTORE by touch /tmp/bs22-idle-link-finish then verify script restoration (previous helper restore needed manual BeginSetup/Connect/Activate if enable failed). User next asked idle silence and minus once. Current gain restored13 by speaker close. Need restore active comms after test; do not leave disabled.

Closed-audio idle-minus test: user done, no new VGS/event, physical gain stayed13. User did not explicitly confirm silence. Helper restore failed enable prerequisite; restored via Client.connect/ready/ACTIVATE successfully19825. Running sink0 after restore. Closed-link is NOT a proven solution. ACTIVE brief baseline1 diagnostic: bluealsa-cli volume sink1, saved preference unchanged13, no code edits. Ask whether static audible and one minus press; continuous HCI18786 log /tmp/bs22-idle-volume-hci.log. MUST restore physical sink0 after reply; idle-gain math unchanged so gain0 will not yet decrement saved13. Do not claim idle-minus implementation.

BREAKTHROUGH idle-minus: user confirmed no static at manually applied physical gain1. Minus produced AT+VGS=0 at21:48:00.128 UTC, versus no event at floor0. Restored physical0 after diagnostic. Implemented speakerIdleGain=1<<8 with signed/clamped preferred += received-idleReference, preserving absolute gain during audible playback. Worker restores reference after each idle button event, enabling repeated down/up. Tests cover down/up across idle, retained burst gain, saturation, zero underflow. Audio race+vet passed63843. Firmware build51330 log /tmp/bs22-idle-volume-build.log underway. Bench deploy and repeated idleminus physical acceptance still pending. Existing saved gain13. This quiet baseline has only been physically verified on this BS-22, not other accessories.

Idle baseline1 build51330 passed; deployed51575. Startup initially Bluetooth off (startup log disabled/poweroff error); explicitly Client.connect/ready/ACTIVATE26195 restored. Need investigate deployment/restart setting before declaring reconnect complete for latest build. Sink Runningtrue Volume1, saved gain13. Continuous rawHCI18786 /tmp/bs22-idle-volume-hci.log. Next prompt minus3 at idle expects saved10 and three VGS0 with hostrestore1 between. No playback active. All quiet-baseline physical repeated-press acceptance still pending.

Idle minus3 ACCEPTED by hardware: rawVGS0 at21:54:29.817,30.292,30.837 each followed by hostVGS1 within10-13ms. Saved2560(level10), down from13. Physical idle1. User said done but did not separately reconfirm silence with this build. Started8-second recorded voice replay atchannel1 to verify newlevel10 and returntoidle1. Next ask heard clearvoice then silence.

User explicitly accepted latest idle1 implementation: clear voice then silence after idleminus3. HCI playback gain10 at21:54:54.254, restoredidle1 at21:55:02.774, saved10 intact. Next physical test requested idle PLUS3 -> expected saved13 from10, rawAT+VGS2 then hostVGS1 each. Monitor18786 still continuous /tmp/bs22-idle-volume-hci.log, no playback active. Need latest-version reboot/restart retest and P2 verification before finishing goal.

Idle PLUS3 verified21:55:52.463,53.068,53.698: each AT+VGS2 followed hostVGS1 in3-4ms, saved10->13 (3328). Both idle directions now hardwareverified. Service restart10743 passed without manualConnect: active/ready/audio/buttons/autoEnable true, saved13 preserved, physicalidle1. Earlier deployment startup error poweroff remains unexplained; cleanrestart did not reproduce. Pending latest-build fullreboot test and robustness followup. Set manet02 channel1 and requested isolated P2 once -> expectedchannel2; continuous HCI18786 remains /tmp/bs22-idle-volume-hci.log. No playback active.

P2 isolatedpress verified: AT+XEVENT=NEXT_CH at21:57:53.590UTC; API activeTalkgroup2 with RX/TX2 only, savedvolume13 unchanged. User saiddone, no separatebrowserdisplayconfirmation. Initiatedfullreboot latestidle1build; preboot327597e7-9adc-42ec-98d1-6dddcd8bd5e5. No manualConnect for this test. HCI monitor18786 will terminate onreboot; restart before further button tests.

Fullreboot81233 latestidle1build succeeded withoutmanualconnect: active/ready/audio/buttons/autoEnabletrue. Found recovery edge: rememberBluetoothVolume only recognized old idle0, could overwrite savedvolume withidle1 afterabrupt audio loss. Added recovery recognition for0 orspeakerIdleGain with tests for saved0/1/13, explicit1, identityisolation; audio race+vet96120 passed. Build49986 /tmp/bs22-idle-recovery-build.log underway. Needdeploy and readiness/tone/voice test. No manualBluetooth connect needed for reboot.

Recovery0/1 correction built49986 and deployed32222 successfully, autoready/active/audio/buttons/autoEnabletrue WITHOUT manualconnect; idle1. Fullreboot priorbuild IDd05ad06a-8268-47c2-a78d-95ed23d0a5cf had saved13 andautoactivation. Latestpackage install auto-reconnect also passed, earlieroneoffpoweroff error not reproduced. Both nodes now channel1. RestartedrawHCI /tmp/bs22-final-volume-hci.log (new session, see tool). Pending final user BS22PTT -> manet01clearvoice and BS22quietafter, no recordingdeadline. No activeplayback. Goal still incomplete until latestphysicalregression accepted.

Latest final-build BS22PTT->manet01website user acceptance: "sounds good!". HCI shows complete PTT_DOWN/UP pairs, latest UP22:03:41.985UTC, feedbackgain13 then idle1 at22:03:42.396. Saved gain13 unchanged. Next request live reverse websitePTT->BS22 for finaltwo-way confirmation. Bothnodeschannel1; monitoring24975 /tmp/bs22-final-volume-hci.log continuous. No playback running. Goal notyetcomplete; last live reverse check pending.

## Final hardware acceptance — November 9, 2026

User confirmed latest build: BS-22 PTT to manet01 website sounds good;
live manet01 website PTT to BS-22 gives clear voice followed by silence.
Final status active/ready/audioReady/buttonsReady/autoEnable true, channel1,
saved listening gain13 (3328 packed), physical quiet idle gain1.

Accepted physical mapping: + increases volume, - decreases volume, including
repeated idle presses; P1 previous channel, P2 next channel. P1 previously
confirmed by user and P2 confirmed raw NEXT_CH plus API exclusive channel2.
Idle minus3 saved13->10; idle plus3 saved10->13. Recorded and live voice both
restore the saved gain and return to quiet idle. No auxiliary button PTT events
were observed during isolated channel/volume tests; regression tests guard
channel handling from initiating transmission.

Service restart and full node reboot automatically reconnected with saved13.
Latest recovery correction also reconnected automatically after installation.
Audio tests with race detector and go vet passed for latest changes. Recovery
recognizes both old idle0 and new quiet idle1 without overwriting saved gain;
explicit user0/1 and different-accessory isolation have regression coverage.

Bench acceptance for the requested static/button goal is complete. Changes
remain on existing codex/mm8108-bluetooth-native work; no commit or push made.
Quiet idle1 acceptance is specific to this tested BS-22, not a claim for all
Bluetooth accessories. One earlier package-install startup power-off error
was not reproduced by subsequent service restart, full reboot, or installation;
retain that observation for future startup hardening. Production packaging of
accessory key setup should be reviewed separately: current device assignments
were persisted with the bench helper, not newly integrated into the pairing UI.

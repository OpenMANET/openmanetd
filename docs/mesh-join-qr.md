# Mesh join by QR code

A configured node shows its mesh credentials as a QR code on
Settings › Wireless › Share Mesh. Point another node's WebUI at it from a
phone and that node joins the same HaLow mesh — and the same 2.4 GHz
backhaul, when the source runs one. A native app can do the same through
the API.

## Sharing

`MeshJoinService.GetMeshJoinQR` (session required) reads the HaLow mesh
interface and the optional 2.4 GHz mesh backhaul from `/etc/config/wireless`
and returns the payload three ways: decoded (`payload`), as the exact QR
text (`payload_text`), and rendered as an SVG (`svg`). The panel shows the
SVG on a light ground with the passphrases masked; Reveal shows them.

The node refuses to share (`FailedPrecondition`) when the HaLow mesh is not
WPA3 (SAE) with a passphrase. A backhaul that is not SAE is left out of the
payload; the HaLow mesh still shares.

## Payload format

```
OPENMANET1:<base64url, no padding, of MeshJoinPayload>
```

`MeshJoinPayload` (`proto/openmanet/mesh_join/v1/mesh_join.proto`) carries
the source hostname and one `MeshCredentials` for the HaLow mesh plus an
optional one for the backhaul: mesh ID, passphrase, encryption (SAE),
bandwidth in MHz, channel and regulatory country. `OPENMANET1:` is the
format version; a node reports "newer OpenMANET build" when it sees a
higher number.

The QR is the credential. Anyone who can photograph the screen can join
the mesh — the same trust as reading the passphrase off the settings page.
The payload is never logged and the WebUI never stores it.

## Joining from the WebUI

The phone's browser does the decoding — nothing about the photo reaches
the node. Live camera preview needs HTTPS, which the WebUI does not use,
so **Scan QR** opens the phone's camera app through a file picker (works on
iOS Safari and Android Chrome) and also accepts a screenshot from the
gallery. **Paste code** accepts the text form.

- **Configured node** — Settings › Wireless › Share Mesh › Join from QR.
  The scanned values fill the HaLow radio and the 2.4 GHz radio that is
  currently in mesh mode; review them, then press **Join mesh**. That sends
  one `ApplyMeshJoin` and reloads wireless once. Values the radio cannot
  accept are listed and block the button. If no 2.4 GHz radio is in mesh
  mode the backhaul is skipped — switch one to mesh and scan again.
- **Fresh node** — the setup wizard's mesh step has the same scanner. It
  fills the HaLow radio and, when the code carries a backhaul and the
  device has a capable radio, switches that radio to mesh backhaul with
  the scanned channel, width and country. Illegal values are flagged and
  Next waits until they are fixed.

Expect your own connection to drop while the radios restart.

## Joining from a native app

1. Scan the QR with the platform scanner and decode the text with the
   generated `MeshJoinPayload` stubs.
2. Configured node: `POST /auth/login`, then call `ApplyMeshJoin` with
   `Authorization: Bearer <token>` (see `docs/api-authentication.md`).
   Leave `halow_radio` / `backhaul_radio` empty unless the node has more
   than one candidate. The response lists each radio as applied or
   skipped; validation failures come back as `InvalidArgument` naming the
   radio and field.
3. Fresh node: build a `MeshNodeProfile` (hostname, admin password, role,
   mode, uplink) from the payload's credentials and call `ApplySetup`.

## Recovery

A join that leaves the node unreachable is fixed like any other wireless
change: reconnect over Ethernet or the client AP, open Settings › Wireless
and correct the radio, or re-run the wizard (`docs/setup-wizard-recovery.md`).

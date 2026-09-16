import { WifiEncryption } from '../gen/openmanet/wifi_config/v1/wifi_config_pb.js';

// apDefaults is the per-radio entry shape in state.aps. `enabled` is
// the client-AP switch; `meshBackhaul` marks the one radio (at most)
// that runs the 2.4 GHz batman-adv backhaul instead, with its own
// mesh ID and passphrase.
//
// Lives outside SetupContext.jsx so that file only exports components
// and hooks — a non-component export there breaks React Fast Refresh.
export function apDefaults(radioName) {
  return {
    radioName,
    enabled:            false,
    ssid:               '',
    passphrase:         '',
    encryption:         WifiEncryption.PSK2,
    meshBackhaul:       false,
    backhaulMeshId:     '',
    backhaulPassphrase: '',
    // Backhaul radio tuning. 0 / '' keep the daemon's fixed defaults
    // (channel 8, HE40, country untouched).
    backhaulBandwidthMhz: 0,
    backhaulChannel:      0,
    backhaulCountryCode:  '',
    // True while the values above came from a scanned code and have
    // not been edited; gates the snap-to-legal effects.
    backhaulFromScan:     false,
  };
}

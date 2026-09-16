// =============================================================================
// SetupContext.jsx — Wizard state machine over MeshNodeProfile
// =============================================================================
//
// The wizard lives entirely in client memory: a browser refresh wipes the
// in-progress profile and restarts at Step 1 (matching the plan's "no
// server-side draft" decision). State is shaped to mirror MeshNodeProfile
// so the Review step can submit it directly without a copy/transform pass.
//
// Each action only mutates one slice; the full state object is replaced on
// every dispatch so React.memo'd children re-render only when their slice
// changed (the reducer never returns the same reference unless no field
// actually changed). Tests in __tests__/contexts/SetupContext.test.jsx
// exercise every action plus immutability.

import { createContext, useContext, useReducer, useMemo } from 'react';
import {
  MeshRole,
  MeshPointMode,
  MeshGateMode,
  UplinkType,
} from '../gen/openmanet/setup/v1/setup_pb.js';
import { WifiEncryption } from '../gen/openmanet/wifi_config/v1/wifi_config_pb.js';
import { apDefaults } from './apDefaults.js';

// initialState mirrors the MeshNodeProfile shape with Mesh Point selected
// by default (plan: "Mesh Point should be selected by default") and the
// canonical S1G defaults (2 MHz / channel 42).
export const initialState = {
  hostname:       '',
  adminPassword:  '',
  role:           MeshRole.MESH_POINT,
  meshpointMode:  MeshPointMode.EXTENDER,
  meshgateMode:   MeshGateMode.UNSPECIFIED,
  mesh: {
    radioName:    '',
    meshId:       'openmanet',
    passphrase:   '',
    encryption:   WifiEncryption.SAE,
    bandwidthMhz: 2,
    channel:      42,
    countryCode:  '',
    fromScan:     false,
  },
  uplink: {
    type:           UplinkType.UNSPECIFIED,
    ethernetPort:   '',
    wireless: {
      radioName:  '',
      ssid:       '',
      passphrase: '',
      encryption: WifiEncryption.PSK2,
    },
  },
  aps: [],
  // adminPasswordConfirm is UI-only — never sent to the backend; just
  // forces the user to type the same password twice.
  adminPasswordConfirm: '',
  // IANA zone name (e.g. "America/Denver"). Empty until HYDRATE_FROM_STATUS
  // seeds it from the browser or the device's current zone, or the user
  // picks one on the Identity step.
  timezone: '',
  // Set by APPLY_MESH_JOIN when a scanned code was applied; null until
  // then. Drives the "Scanned from X" review row and the backhaul-skip
  // notice on StepMesh.
  meshJoin: null,
};

export const SETUP_ACTIONS = {
  SET_HOSTNAME:           'SET_HOSTNAME',
  SET_ROLE:               'SET_ROLE',
  SET_MESHPOINT_MODE:     'SET_MESHPOINT_MODE',
  SET_MESHGATE_MODE:      'SET_MESHGATE_MODE',
  SET_MESH_FIELD:         'SET_MESH_FIELD',
  APPLY_MESH_JOIN:        'APPLY_MESH_JOIN',
  SET_UPLINK_TYPE:        'SET_UPLINK_TYPE',
  SET_UPLINK_FIELD:       'SET_UPLINK_FIELD',
  SET_UPLINK_WIRELESS:    'SET_UPLINK_WIRELESS',
  SET_AP:                 'SET_AP',
  REMOVE_AP:              'REMOVE_AP',
  SET_RADIO_MODE:         'SET_RADIO_MODE',
  SET_ADMIN_PASSWORD:     'SET_ADMIN_PASSWORD',
  SET_ADMIN_PASSWORD_CONFIRM: 'SET_ADMIN_PASSWORD_CONFIRM',
  SET_TIMEZONE:           'SET_TIMEZONE',
  RESET:                  'RESET',
  HYDRATE_FROM_STATUS:    'HYDRATE_FROM_STATUS',
};

// reducer is exported for unit tests so they can drive it without a
// full Provider tree.
export function reducer(state, action) {
  switch (action.type) {
    case SETUP_ACTIONS.SET_HOSTNAME:
      return { ...state, hostname: action.value };

    case SETUP_ACTIONS.SET_ROLE: {
      // Role flip clears the orthogonal mode so the resulting profile
      // is consistent (handler validation rejects mixed mode/role).
      const next = { ...state, role: action.value };
      if (action.value === MeshRole.MESH_GATE) {
        next.meshpointMode = MeshPointMode.UNSPECIFIED;
        if (next.meshgateMode === MeshGateMode.UNSPECIFIED) {
          next.meshgateMode = MeshGateMode.ROUTER;
        }
      } else if (action.value === MeshRole.MESH_POINT) {
        next.meshgateMode = MeshGateMode.UNSPECIFIED;
        if (next.meshpointMode === MeshPointMode.UNSPECIFIED) {
          next.meshpointMode = MeshPointMode.EXTENDER;
        }
        // Mesh Point doesn't expose an uplink step.
        next.uplink = { ...initialState.uplink };
      }
      return next;
    }

    case SETUP_ACTIONS.SET_MESHPOINT_MODE:
      return { ...state, meshpointMode: action.value };

    case SETUP_ACTIONS.SET_MESHGATE_MODE:
      return { ...state, meshgateMode: action.value };

    case SETUP_ACTIONS.SET_MESH_FIELD: {
      // Any manual edit (other than picking the radio) ends the
      // scanned-values hold so the snap-to-legal effects resume.
      const fromScan = action.field === 'radioName' ? state.mesh.fromScan : false;
      return { ...state, mesh: { ...state.mesh, [action.field]: action.value, fromScan } };
    }

    case SETUP_ACTIONS.SET_UPLINK_TYPE:
      return { ...state, uplink: { ...state.uplink, type: action.value } };

    case SETUP_ACTIONS.SET_UPLINK_FIELD:
      return { ...state, uplink: { ...state.uplink, [action.field]: action.value } };

    case SETUP_ACTIONS.SET_UPLINK_WIRELESS:
      return {
        ...state,
        uplink: {
          ...state.uplink,
          wireless: { ...state.uplink.wireless, [action.field]: action.value },
        },
      };

    case SETUP_ACTIONS.SET_AP: {
      // Insert or update by radioName. Action carries a partial AP profile
      // — fields not in `value` are merged with the existing entry, or
      // defaulted on insert.
      const idx = state.aps.findIndex(ap => ap.radioName === action.value.radioName);
      const baseDefaults = apDefaults(action.value.radioName);
      const merged = idx >= 0
        ? { ...state.aps[idx], ...action.value }
        : { ...baseDefaults, ...action.value };
      const aps = [...state.aps];
      if (idx >= 0) aps[idx] = merged;
      else aps.push(merged);
      return { ...state, aps };
    }

    case SETUP_ACTIONS.SET_RADIO_MODE: {
      // One control per radio: 'off' | 'ap' | 'backhaul'. Only one
      // radio may be the backhaul (the daemon runs a single batmesh1
      // hardif), so choosing it on one radio clears it on every other.
      // AP and backhaul fields both survive a mode flip so the user can
      // change their mind without retyping.
      const { radioName, mode } = action;
      const isBackhaul = mode === 'backhaul';
      const idx = state.aps.findIndex(ap => ap.radioName === radioName);
      const existing = idx >= 0 ? state.aps[idx] : apDefaults(radioName);
      const updated = {
        ...existing,
        enabled:      mode === 'ap',
        meshBackhaul: isBackhaul,
        // The Step-2 mesh-ID input allows up to 32 chars, but
        // MeshBackhaulProfile.mesh_id caps at 32 — clamp the seed so
        // appending "-2g" can never push it past the proto limit.
        backhaulMeshId: isBackhaul && !existing.backhaulMeshId
          ? `${state.mesh.meshId.slice(0, 29)}-2g`
          : existing.backhaulMeshId,
      };
      const aps = state.aps.map(ap => (
        isBackhaul && ap.radioName !== radioName && ap.meshBackhaul
          ? { ...ap, meshBackhaul: false }
          : ap
      ));
      if (idx >= 0) aps[idx] = updated;
      else aps.push(updated);
      return { ...state, aps };
    }

    case SETUP_ACTIONS.REMOVE_AP:
      return { ...state, aps: state.aps.filter(ap => ap.radioName !== action.radioName) };

    case SETUP_ACTIONS.SET_ADMIN_PASSWORD:
      return { ...state, adminPassword: action.value };

    case SETUP_ACTIONS.SET_ADMIN_PASSWORD_CONFIRM:
      return { ...state, adminPasswordConfirm: action.value };

    case SETUP_ACTIONS.SET_TIMEZONE:
      return { ...state, timezone: action.value };

    case SETUP_ACTIONS.RESET:
      return { ...initialState };

    case SETUP_ACTIONS.HYDRATE_FROM_STATUS: {
      // Pre-fill the mesh radio with the first HaLow radio reported by
      // the backend so the user doesn't have to pick when there's an
      // obvious choice. Pre-fill the hostname with the device's current
      // system hostname (the factory image ships e.g. `BCM2711-97d6`) —
      // matches the LuCI Morse wizard which seeded its hostname field
      // from `system.@system[0].hostname`. The user can still change it.
      // Pre-fill the country with the current `wireless.<radio>.country`
      // so the channel/bandwidth filter starts narrowed to a sensible
      // default; if the device has no country set, fall back to "US"
      // when the regdb has it (the OpenMANET reference firmware ships
      // factory-defaulted to US), otherwise leave empty for the user
      // to pick.
      const halow = (action.status?.radios ?? []).find(r => r.isHalow);
      const countries = action.status?.countries ?? [];
      const next = { ...state, mesh: { ...state.mesh } };
      if (halow) next.mesh.radioName = halow.name;

      const current = action.status?.currentHostname;
      if (!state.hostname && typeof current === 'string' && current !== '') {
        next.hostname = current;
      }

      if (!state.mesh.countryCode) {
        const cur = action.status?.currentCountry;
        if (cur && countries.some(c => c.code === cur)) {
          next.mesh.countryCode = cur;
        } else if (countries.some(c => c.code === 'US')) {
          next.mesh.countryCode = 'US';
        } else if (countries.length > 0) {
          next.mesh.countryCode = countries[0].code;
        }
      }

      // Pre-fill the timezone: prefer the browser's own zone (rides on
      // the action so the reducer stays pure — the dispatch site
      // supplies it via Intl.DateTimeFormat) when the device's zone
      // list actually offers it, otherwise fall back to the device's
      // current zone. Never overwrite a zone the user already picked.
      if (!state.timezone) {
        const zones = action.status?.timezones ?? [];
        if (action.browserTimezone && zones.includes(action.browserTimezone)) {
          next.timezone = action.browserTimezone;
        } else if (action.status?.currentTimezone) {
          next.timezone = action.status.currentTimezone;
        }
      }

      return next;
    }

    case SETUP_ACTIONS.APPLY_MESH_JOIN: {
      // A scanned code fills the mesh slice and, when it carries a
      // backhaul, switches the first capable 2.4 GHz radio (never the
      // STA uplink radio) to backhaul with the scanned tuning. Values
      // are marked fromScan so StepMesh/StepAPs do not snap them.
      const { payload, radios = [] } = action;
      const h = payload.halow;
      const next = {
        ...state,
        mesh: {
          ...state.mesh,
          meshId:       h.meshId,
          passphrase:   h.passphrase,
          encryption:   h.encryption,
          bandwidthMhz: h.bandwidthMhz,
          channel:      h.channel,
          countryCode:  h.countryCode || state.mesh.countryCode,
          fromScan:     true,
        },
      };
      const meshJoin = { sourceHostname: payload.sourceHostname ?? '', backhaulRadio: '', backhaulSkippedReason: '' };

      if (payload.backhaul) {
        const uplinkRadio = state.uplink.wireless.radioName;
        const target = radios.find(r => r.supportsMeshBackhaul && !r.isHalow && r.name !== uplinkRadio);
        if (target) {
          const b = payload.backhaul;
          const existing = state.aps.find(ap => ap.radioName === target.name) ?? apDefaults(target.name);
          const updated = {
            ...existing,
            enabled:              false,
            meshBackhaul:         true,
            backhaulMeshId:       b.meshId,
            backhaulPassphrase:   b.passphrase,
            backhaulBandwidthMhz: b.bandwidthMhz,
            backhaulChannel:      b.channel,
            backhaulCountryCode:  b.countryCode || h.countryCode || '',
            backhaulFromScan:     true,
          };
          next.aps = state.aps
            .filter(ap => ap.radioName !== target.name)
            .map(ap => (ap.meshBackhaul ? { ...ap, meshBackhaul: false } : ap));
          next.aps.push(updated);
          meshJoin.backhaulRadio = target.name;
        } else {
          meshJoin.backhaulSkippedReason = 'no-capable-radio';
        }
      }

      next.meshJoin = meshJoin;
      return next;
    }

    default:
      return state;
  }
}

const SetupContext = createContext(null);

export function SetupProvider({ children }) {
  const [state, dispatch] = useReducer(reducer, initialState);

  const value = useMemo(() => ({ state, dispatch }), [state]);

  return <SetupContext.Provider value={value}>{children}</SetupContext.Provider>;
}

export function useSetup() {
  const ctx = useContext(SetupContext);
  if (ctx === null) {
    throw new Error('useSetup must be called inside a <SetupProvider>');
  }
  return ctx;
}

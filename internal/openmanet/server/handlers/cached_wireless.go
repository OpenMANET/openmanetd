package handlers

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/mdlayher/wifi"
	"github.com/openmanet/openmanetd/internal/mgmt"
)

// DefaultWirelessCacheTTL is the staleness window for cached wireless
// netlink reads. DashboardService, MeshService, StatusService, and
// WifiConfigService all call Interfaces()/StationInfo() on their own
// polling cadences and get byte-identical data back within a few
// seconds; the cache collapses all of those calls to a single
// enumeration per TTL window.
const DefaultWirelessCacheTTL = 5 * time.Second

// CachedWirelessProvider wraps an inner mgmt.WirelessProvider with a
// time-bounded cache. The snapshot holds both the enumerated wifi
// interfaces and the per-interface station-info list, populated in one
// refresh so a single lock-free read serves any combination of
// Interfaces / GetMeshInterfaces / StationInfo calls that arrive in the
// same TTL window.
//
// Errors are not cached: a transient nl80211 failure retries on the
// next call. Interfaces that did not exist at refresh time fall through
// to the inner provider so freshly-created interfaces still work —
// callers passing a stale *wifi.Interface pointer keep getting the
// cached stations for that iface name until the next refresh.
type CachedWirelessProvider struct {
	Inner     mgmt.WirelessProvider
	val       atomic.Pointer[cachedWirelessEntry]
	TTL       time.Duration
	refreshMu sync.Mutex
}

type cachedWirelessEntry struct {
	at            time.Time
	interfacesErr error
	stations      map[string]stationResult
	interfaces    []*wifi.Interface
}

type stationResult struct {
	err  error
	info []*wifi.StationInfo
}

// NewCachedWirelessProvider wraps inner with a TTL-bounded cache. If
// ttl is <= 0, DefaultWirelessCacheTTL is used.
func NewCachedWirelessProvider(inner mgmt.WirelessProvider, ttl time.Duration) *CachedWirelessProvider {
	if ttl <= 0 {
		ttl = DefaultWirelessCacheTTL
	}

	return &CachedWirelessProvider{Inner: inner, TTL: ttl}
}

// EnsureCachedWireless returns p wrapped in a TTL cache unless it is
// nil or already a *CachedWirelessProvider. The daemon builds one
// cache up front and shares it with the instrumentation snapshotter;
// re-wrapping it here would double the netlink traffic.
func EnsureCachedWireless(p mgmt.WirelessProvider) mgmt.WirelessProvider {
	switch v := p.(type) {
	case nil:
		return nil
	case *CachedWirelessProvider:
		if v == nil {
			return nil
		}

		return v
	default:
		return NewCachedWirelessProvider(p, DefaultWirelessCacheTTL)
	}
}

// Interfaces returns the cached wifi-interface list.
func (p *CachedWirelessProvider) Interfaces() ([]*wifi.Interface, error) {
	entry := p.getOrRefresh()
	if entry == nil {
		return nil, nil
	}

	return entry.interfaces, entry.interfacesErr
}

// GetMeshInterfaces returns the cached interfaces filtered to
// InterfaceTypeMeshPoint. The filter rebuilds the mesh list on every
// call so there's no separate cache entry to keep in sync.
func (p *CachedWirelessProvider) GetMeshInterfaces() ([]*wifi.Interface, error) {
	entry := p.getOrRefresh()
	if entry == nil {
		return nil, nil
	}

	if entry.interfacesErr != nil {
		return nil, entry.interfacesErr
	}

	var meshIfaces []*wifi.Interface

	for _, iface := range entry.interfaces {
		if iface.Type == wifi.InterfaceTypeMeshPoint {
			meshIfaces = append(meshIfaces, iface)
		}
	}

	return meshIfaces, nil
}

// StationInfo returns the cached station list for iface if the iface
// was present at refresh time, otherwise falls through to the inner
// provider so newly-created interfaces still work.
func (p *CachedWirelessProvider) StationInfo(iface *wifi.Interface) ([]*wifi.StationInfo, error) {
	entry := p.getOrRefresh()
	if entry != nil && entry.stations != nil {
		if res, ok := entry.stations[iface.Name]; ok {
			return res.info, res.err
		}
	}

	// Cold cache or unknown interface: query live so callers never see
	// a stale-cache "no such interface" that the uncached code path
	// would have returned real data for.
	return p.Inner.StationInfo(iface)
}

// getOrRefresh returns the current cached entry if it's fresh,
// otherwise grabs the refresh mutex and issues a single inner
// enumeration + station walk. Concurrent callers coalesce onto one
// fetch: the second and subsequent contenders find the entry already
// populated after they acquire the mutex.
func (p *CachedWirelessProvider) getOrRefresh() *cachedWirelessEntry {
	if entry := p.val.Load(); entry != nil && time.Since(entry.at) < p.TTL {
		return entry
	}

	p.refreshMu.Lock()
	defer p.refreshMu.Unlock()

	if entry := p.val.Load(); entry != nil && time.Since(entry.at) < p.TTL {
		return entry
	}

	entry := p.refresh()
	// Only cache successful enumerations. A transient nl80211 failure
	// in Interfaces() returns the error to the caller but leaves the
	// cache empty so the next call retries, matching the uncached
	// provider's semantics. Per-station errors observed during a
	// successful enumeration are still cached inside the entry so they
	// are not re-probed every call within TTL.
	if entry.interfacesErr == nil {
		p.val.Store(entry)
	}

	return entry
}

func (p *CachedWirelessProvider) refresh() *cachedWirelessEntry {
	entry := &cachedWirelessEntry{at: time.Now()}
	entry.interfaces, entry.interfacesErr = p.Inner.Interfaces()

	if entry.interfacesErr == nil && len(entry.interfaces) > 0 {
		entry.stations = make(map[string]stationResult, len(entry.interfaces))
		for _, iface := range entry.interfaces {
			info, err := p.Inner.StationInfo(iface)
			entry.stations[iface.Name] = stationResult{info: info, err: err}
		}
	}

	return entry
}

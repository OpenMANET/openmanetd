package handlers_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mdlayher/wifi"
	"github.com/openmanet/openmanetd/internal/openmanet/server/handlers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeWirelessInner struct {
	ifacesCalls   atomic.Int64
	stationsCalls atomic.Int64

	mu          sync.Mutex
	ifaces      []*wifi.Interface
	ifacesErr   error
	stationsBy  map[string][]*wifi.StationInfo
	stationsErr map[string]error
	delay       time.Duration
}

func (f *fakeWirelessInner) Interfaces() ([]*wifi.Interface, error) {
	f.ifacesCalls.Add(1)

	if f.delay > 0 {
		time.Sleep(f.delay)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	return f.ifaces, f.ifacesErr
}

func (f *fakeWirelessInner) GetMeshInterfaces() ([]*wifi.Interface, error) {
	ifaces, err := f.Interfaces()
	if err != nil {
		return nil, err
	}

	out := make([]*wifi.Interface, 0, len(ifaces))

	for _, i := range ifaces {
		if i.Type == wifi.InterfaceTypeMeshPoint {
			out = append(out, i)
		}
	}

	return out, nil
}

func (f *fakeWirelessInner) StationInfo(iface *wifi.Interface) ([]*wifi.StationInfo, error) {
	f.stationsCalls.Add(1)

	f.mu.Lock()
	defer f.mu.Unlock()

	if err, ok := f.stationsErr[iface.Name]; ok {
		return nil, err
	}

	return f.stationsBy[iface.Name], nil
}

func TestCachedWirelessProvider_ServesFromCacheWithinTTL(t *testing.T) {
	meshIface := &wifi.Interface{Name: "phy0-mesh0", Type: wifi.InterfaceTypeMeshPoint}
	apIface := &wifi.Interface{Name: "wlh0", Type: wifi.InterfaceTypeAP}

	inner := &fakeWirelessInner{
		ifaces: []*wifi.Interface{meshIface, apIface},
		stationsBy: map[string][]*wifi.StationInfo{
			"phy0-mesh0": {{}, {}},
			"wlh0":       {{}, {}, {}},
		},
	}

	p := handlers.NewCachedWirelessProvider(inner, 500*time.Millisecond)

	ifaces, err := p.Interfaces()
	require.NoError(t, err)
	assert.Len(t, ifaces, 2)

	meshOnly, err := p.GetMeshInterfaces()
	require.NoError(t, err)
	assert.Len(t, meshOnly, 1)
	assert.Equal(t, "phy0-mesh0", meshOnly[0].Name)

	meshStations, err := p.StationInfo(meshIface)
	require.NoError(t, err)
	assert.Len(t, meshStations, 2)

	apStations, err := p.StationInfo(apIface)
	require.NoError(t, err)
	assert.Len(t, apStations, 3)

	// Interfaces() called exactly once across all cached reads.
	assert.Equal(t, int64(1), inner.ifacesCalls.Load())
	// StationInfo called twice (once per iface) at refresh time, but
	// not again for the cache-hit reads.
	assert.Equal(t, int64(2), inner.stationsCalls.Load())
}

func TestCachedWirelessProvider_RefetchesAfterTTL(t *testing.T) {
	inner := &fakeWirelessInner{
		ifaces:     []*wifi.Interface{{Name: "wlh0"}},
		stationsBy: map[string][]*wifi.StationInfo{"wlh0": {}},
	}

	p := handlers.NewCachedWirelessProvider(inner, 20*time.Millisecond)

	_, err := p.Interfaces()
	require.NoError(t, err)

	time.Sleep(30 * time.Millisecond)

	_, err = p.Interfaces()
	require.NoError(t, err)

	assert.Equal(t, int64(2), inner.ifacesCalls.Load(),
		"Interfaces should be refetched once the TTL expires")
}

func TestCachedWirelessProvider_StationInfoFallsThroughForUnknownIface(t *testing.T) {
	known := &wifi.Interface{Name: "wlh0"}
	unknown := &wifi.Interface{Name: "wlan-new"}

	inner := &fakeWirelessInner{
		ifaces:     []*wifi.Interface{known},
		stationsBy: map[string][]*wifi.StationInfo{"wlh0": {{}}, "wlan-new": {{}, {}}},
	}

	p := handlers.NewCachedWirelessProvider(inner, 5*time.Second)

	// Warm the cache with the known iface only.
	_, err := p.Interfaces()
	require.NoError(t, err)

	// Reset the per-request counter so we can observe the fall-through.
	inner.stationsCalls.Store(0)

	// wlh0 is in the cache — served without hitting the inner.
	got, err := p.StationInfo(known)
	require.NoError(t, err)
	assert.Len(t, got, 1)
	assert.Equal(t, int64(0), inner.stationsCalls.Load())

	// wlan-new is NOT in the cache — the cache must fall through so
	// newly-created interfaces still work.
	got, err = p.StationInfo(unknown)
	require.NoError(t, err)
	assert.Len(t, got, 2)
	assert.Equal(t, int64(1), inner.stationsCalls.Load())
}

func TestCachedWirelessProvider_InterfacesErrorIsNotCached(t *testing.T) {
	wantErr := errors.New("nl80211 unavailable")
	inner := &fakeWirelessInner{ifacesErr: wantErr}

	p := handlers.NewCachedWirelessProvider(inner, 5*time.Second)

	_, err := p.Interfaces()
	assert.ErrorIs(t, err, wantErr)

	_, err = p.Interfaces()
	assert.ErrorIs(t, err, wantErr)

	// Each call retries — error must not poison the cache.
	assert.Equal(t, int64(2), inner.ifacesCalls.Load())
}

func TestCachedWirelessProvider_PerStationErrorIsCached(t *testing.T) {
	iface := &wifi.Interface{Name: "wlh0"}
	wantErr := errors.New("station dump failed")
	inner := &fakeWirelessInner{
		ifaces:      []*wifi.Interface{iface},
		stationsErr: map[string]error{"wlh0": wantErr},
	}

	p := handlers.NewCachedWirelessProvider(inner, 5*time.Second)

	// First StationInfo triggers refresh; inner is called twice
	// (Interfaces + StationInfo once for wlh0).
	_, err := p.StationInfo(iface)
	assert.ErrorIs(t, err, wantErr)

	innerStationsBefore := inner.stationsCalls.Load()

	// Subsequent StationInfo calls within TTL must serve the cached
	// error, not re-call the inner.
	for range 5 {
		_, err := p.StationInfo(iface)
		assert.ErrorIs(t, err, wantErr)
	}

	assert.Equal(t, innerStationsBefore, inner.stationsCalls.Load(),
		"per-iface station errors should be served from the cache within TTL")
}

func TestCachedWirelessProvider_CoalescesConcurrentRefreshes(t *testing.T) {
	inner := &fakeWirelessInner{
		ifaces:     []*wifi.Interface{{Name: "wlh0"}},
		stationsBy: map[string][]*wifi.StationInfo{"wlh0": {}},
		delay:      50 * time.Millisecond,
	}

	p := handlers.NewCachedWirelessProvider(inner, 5*time.Second)

	const workers = 10

	var wg sync.WaitGroup

	wg.Add(workers)

	for range workers {
		go func() {
			defer wg.Done()

			_, err := p.Interfaces()
			assert.NoError(t, err)
		}()
	}

	wg.Wait()

	assert.LessOrEqual(t, inner.ifacesCalls.Load(), int64(3),
		"concurrent callers should coalesce onto a single in-flight Interfaces() call")
}

func TestNewCachedWirelessProvider_DefaultTTL(t *testing.T) {
	p := handlers.NewCachedWirelessProvider(&fakeWirelessInner{}, 0)
	assert.Equal(t, handlers.DefaultWirelessCacheTTL, p.TTL)
}

func TestEnsureCachedWireless(t *testing.T) {
	t.Parallel()

	assert.Nil(t, handlers.EnsureCachedWireless(nil), "nil stays nil")

	var typedNil *handlers.CachedWirelessProvider

	assert.Nil(t, handlers.EnsureCachedWireless(typedNil), "a typed nil must not become a non-nil interface")

	cached := handlers.NewCachedWirelessProvider(&fakeWirelessInner{}, time.Second)
	assert.Same(t, cached, handlers.EnsureCachedWireless(cached), "an existing cache is reused, not re-wrapped")

	wrapped := handlers.EnsureCachedWireless(&fakeWirelessInner{})
	_, ok := wrapped.(*handlers.CachedWirelessProvider)
	assert.True(t, ok, "a raw provider is wrapped")
}

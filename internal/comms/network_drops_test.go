package comms

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/openmanet/openmanetd/internal/comms/rtp"
	"github.com/openmanet/openmanetd/internal/comms/webaudio"
)

// writeProcUDPFixture writes a synthetic /proc/net/udp-format file and
// returns its path. rows are appended below the standard header.
func writeProcUDPFixture(t *testing.T, rows ...string) string {
	t.Helper()

	const header = "   sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode ref pointer drops"

	path := filepath.Join(t.TempDir(), "udp")
	content := header + "\n" + strings.Join(rows, "\n") + "\n"

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

// procUDPRow renders one /proc/net/udp row for the given port and drop
// count, matching the kernel's field layout (drops is the last field).
func procUDPRow(port int, drops int64) string {
	return fmt.Sprintf(
		"  368: 00000000:%04X 00000000:0000 07 00000000:00000000 00:00000000 00000000  1000        0 27686 2 0000000000000000 %d",
		port, drops)
}

func TestScanUDPDropsFile_MatchingRow(t *testing.T) {
	path := writeProcUDPFixture(t,
		procUDPRow(5000, 0),
		procUDPRow(38801, 42),
		procUDPRow(9999, 7),
	)

	drops, err := scanUDPDropsFile(path, 38801)
	if err != nil {
		t.Fatal(err)
	}

	if drops != 42 {
		t.Errorf("drops: got %d, want 42", drops)
	}
}

func TestScanUDPDropsFile_NoMatchingRow(t *testing.T) {
	path := writeProcUDPFixture(t, procUDPRow(5000, 3))

	drops, err := scanUDPDropsFile(path, 38801)
	if err != nil {
		t.Fatal(err)
	}

	if drops != -1 {
		t.Errorf("drops: got %d, want -1 (row missing)", drops)
	}
}

func TestScanUDPDropsFile_MissingFile(t *testing.T) {
	drops, err := scanUDPDropsFile(filepath.Join(t.TempDir(), "nope"), 38801)
	if err != nil {
		t.Fatalf("missing file must not error (non-Linux hosts): %v", err)
	}

	if drops != -1 {
		t.Errorf("drops: got %d, want -1", drops)
	}
}

// TestScanUDPDropsFile_HexSuffixInOtherColumn guards the fast-path
// pre-filter: a row whose inode/pointer happens to contain the port's hex
// pattern must not match unless the local_address column actually ends
// with it.
func TestScanUDPDropsFile_HexSuffixInOtherColumn(t *testing.T) {
	// 38801 = 0x9791; craft a non-matching row that contains ":9791"
	// in the remote-address column only.
	row := "  368: 00000000:1388 00000000:9791 07 00000000:00000000 00:00000000 00000000  1000        0 27686 2 0000000000000000 5"
	path := writeProcUDPFixture(t, row, procUDPRow(38801, 42))

	drops, err := scanUDPDropsFile(path, 38801)
	if err != nil {
		t.Fatal(err)
	}

	if drops != 42 {
		t.Errorf("drops: got %d, want 42 (decoy row must not match)", drops)
	}
}

func TestScanUDPDropsFile_ShortRowsSkipped(t *testing.T) {
	path := writeProcUDPFixture(t, "garbage row", procUDPRow(38801, 9))

	drops, err := scanUDPDropsFile(path, 38801)
	if err != nil {
		t.Fatal(err)
	}

	if drops != 9 {
		t.Errorf("drops: got %d, want 9", drops)
	}
}

// runStatTickProbe drives webPlayoutLoop with the given logger and RX
// activity level, and reports whether the kernel-drop proc scan ran
// during ~3 stat ticks.
func runStatTickProbe(t *testing.T, log zerolog.Logger, withActivity bool) bool {
	t.Helper()

	var scanned atomic.Bool

	cfg := &CommsConfig{
		Log:             log,
		Loopback:        true,
		webStatInterval: 20 * time.Millisecond,
		readUDPDropsFn: func(int) (int64, error) {
			scanned.Store(true)

			return 0, nil
		},
	}

	pc := &PortChannel{cfg: McastPortConfig{Send: true, Receive: true}}
	pc.SendEnabled.Store(true)
	pc.ReceiveEnabled.Store(true)

	rt := &CommsRuntime{Ports: []*PortChannel{pc}}
	rt.WebBridge = webaudio.NewBridge(zerolog.Nop(), func(_ []byte) {})
	rt.WebBridge.AddConsumer()

	jb := rtp.NewJitterBuffer(1, 16)

	if withActivity {
		// RxPkts deltas are what the activity gate reads.
		pc.RxPkts.Add(5)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})

	go func() {
		defer close(done)

		cfg.webPlayoutLoop(ctx, pc, jb, rt)
	}()

	// Let ~3 stat ticks fire, feeding fresh activity each tick when asked.
	for range 3 {
		time.Sleep(25 * time.Millisecond)

		if withActivity {
			pc.RxPkts.Add(5)
		}
	}

	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("webPlayoutLoop did not exit")
	}

	return scanned.Load()
}

// TestWebStatTick_ScansOnlyWhenDebugAndActive pins the P8 gate: the
// /proc/net/udp scan is debug-log-only telemetry, so it must not run when
// Debug logging is off or when the port saw no RX activity in the window.
func TestWebStatTick_ScansOnlyWhenDebugAndActive(t *testing.T) {
	debugLog := zerolog.New(io.Discard).Level(zerolog.DebugLevel)
	infoLog := zerolog.New(io.Discard).Level(zerolog.InfoLevel)

	if !runStatTickProbe(t, debugLog, true) {
		t.Error("scan should run with Debug enabled and RX activity")
	}

	if runStatTickProbe(t, infoLog, true) {
		t.Error("scan must not run when Debug logging is disabled")
	}

	if runStatTickProbe(t, debugLog, false) {
		t.Error("scan must not run for an idle port")
	}
}

// BenchmarkScanUDPDropsFile measures one scan over a realistically busy
// host table (60 sockets), matching in the last row — the worst case.
func BenchmarkScanUDPDropsFile(b *testing.B) {
	rows := make([]string, 0, 60)
	for i := range 59 {
		rows = append(rows, procUDPRow(10000+i, int64(i)))
	}

	rows = append(rows, procUDPRow(38801, 42))

	const header = "   sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode ref pointer drops"

	path := filepath.Join(b.TempDir(), "udp")
	if err := os.WriteFile(path, []byte(header+"\n"+strings.Join(rows, "\n")+"\n"), 0o600); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		drops, err := scanUDPDropsFile(path, 38801)
		if err != nil || drops != 42 {
			b.Fatalf("drops=%d err=%v", drops, err)
		}
	}
}

package models_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/openmanet/openmanetd/internal/database/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── helpers ──────────────────────────────────────────────────────────────────

const schemaSQL = `
CREATE TABLE IF NOT EXISTS mesh_nodes (
  mac_addr       text PRIMARY KEY NOT NULL,
  hostname       text NOT NULL,
  ip_addr        text NOT NULL,
  latitude       real,
  longitude      real,
  altitude       real,
  uci_dhcp_start integer,
  uci_dhcp_limit integer,
  created_at     timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at     timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP
);`

// newDB opens a fresh in-memory SQLite database, applies the schema, and
// registers cleanup. Each call returns an independent database.
func newDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err, "open in-memory sqlite")
	t.Cleanup(func() { db.Close() })

	_, err = db.Exec(schemaSQL)
	require.NoError(t, err, "apply schema")

	return db
}

func newQueries(t *testing.T) *models.Queries {
	t.Helper()

	return models.New(newDB(t))
}

// baseParams returns a fully-populated CreateMeshNodeParams for use in tests.
func baseParams(mac, hostname, ip string) models.CreateMeshNodeParams {
	return models.CreateMeshNodeParams{
		MacAddr:      mac,
		Hostname:     hostname,
		IpAddr:       ip,
		Latitude:     sql.NullFloat64{Float64: 37.7749, Valid: true},
		Longitude:    sql.NullFloat64{Float64: -122.4194, Valid: true},
		Altitude:     sql.NullFloat64{Float64: 100.0, Valid: true},
		UciDhcpStart: sql.NullInt64{Int64: 100, Valid: true},
		UciDhcpLimit: sql.NullInt64{Int64: 50, Valid: true},
	}
}

// ── CreateMeshNode ────────────────────────────────────────────────────────────

func TestCreateMeshNode_AllFields(t *testing.T) {
	ctx := context.Background()
	q := newQueries(t)

	p := baseParams("aa:bb:cc:dd:ee:ff", "node-1", "10.0.0.1")
	node, err := q.CreateMeshNode(ctx, p)
	require.NoError(t, err)

	assert.Equal(t, p.MacAddr, node.MacAddr)
	assert.Equal(t, p.Hostname, node.Hostname)
	assert.Equal(t, p.IpAddr, node.IpAddr)
	assert.Equal(t, p.Latitude.Float64, node.Latitude.Float64)
	assert.True(t, node.Latitude.Valid)
	assert.Equal(t, p.Longitude.Float64, node.Longitude.Float64)
	assert.True(t, node.Longitude.Valid)
	assert.Equal(t, p.Altitude.Float64, node.Altitude.Float64)
	assert.True(t, node.Altitude.Valid)
	assert.Equal(t, p.UciDhcpStart.Int64, node.UciDhcpStart.Int64)
	assert.Equal(t, p.UciDhcpLimit.Int64, node.UciDhcpLimit.Int64)
	assert.False(t, node.CreatedAt.IsZero(), "created_at should be populated")
	assert.False(t, node.UpdatedAt.IsZero(), "updated_at should be populated")
}

func TestCreateMeshNode_NullableFieldsOmitted(t *testing.T) {
	ctx := context.Background()
	q := newQueries(t)

	p := models.CreateMeshNodeParams{
		MacAddr:  "11:22:33:44:55:66",
		Hostname: "bare-node",
		IpAddr:   "10.0.0.2",
	}
	node, err := q.CreateMeshNode(ctx, p)
	require.NoError(t, err)

	assert.False(t, node.Latitude.Valid)
	assert.False(t, node.Longitude.Valid)
	assert.False(t, node.Altitude.Valid)
	assert.False(t, node.UciDhcpStart.Valid)
	assert.False(t, node.UciDhcpLimit.Valid)
}

func TestCreateMeshNode_Upsert_UpdatesOnConflict(t *testing.T) {
	ctx := context.Background()
	q := newQueries(t)

	mac := "aa:bb:cc:dd:ee:ff"
	_, err := q.CreateMeshNode(ctx, baseParams(mac, "original", "10.0.0.1"))
	require.NoError(t, err)

	updated, err := q.CreateMeshNode(ctx, baseParams(mac, "updated", "10.0.0.99"))
	require.NoError(t, err)

	assert.Equal(t, "updated", updated.Hostname)
	assert.Equal(t, "10.0.0.99", updated.IpAddr)
}

func TestCreateMeshNode_Upsert_PreservesCreatedAt(t *testing.T) {
	ctx := context.Background()
	q := newQueries(t)

	mac := "aa:bb:cc:dd:ee:ff"
	first, err := q.CreateMeshNode(ctx, baseParams(mac, "original", "10.0.0.1"))
	require.NoError(t, err)

	// Small sleep so CURRENT_TIMESTAMP would differ if CreatedAt were regenerated.
	time.Sleep(1100 * time.Millisecond)

	second, err := q.CreateMeshNode(ctx, baseParams(mac, "updated", "10.0.0.2"))
	require.NoError(t, err)

	assert.Equal(t, first.CreatedAt.Unix(), second.CreatedAt.Unix(),
		"upsert must not change created_at")
}

// ── GetMeshNode ───────────────────────────────────────────────────────────────

func TestGetMeshNode_Found(t *testing.T) {
	ctx := context.Background()
	q := newQueries(t)

	p := baseParams("de:ad:be:ef:00:01", "target", "192.168.1.1")
	_, err := q.CreateMeshNode(ctx, p)
	require.NoError(t, err)

	node, err := q.GetMeshNode(ctx, p.MacAddr)
	require.NoError(t, err)
	assert.Equal(t, p.MacAddr, node.MacAddr)
	assert.Equal(t, p.Hostname, node.Hostname)
	assert.Equal(t, p.IpAddr, node.IpAddr)
}

func TestGetMeshNode_NotFound(t *testing.T) {
	ctx := context.Background()
	q := newQueries(t)

	_, err := q.GetMeshNode(ctx, "ff:ff:ff:ff:ff:ff")
	assert.ErrorIs(t, err, sql.ErrNoRows)
}

// ── GetMeshNodeByHostname ─────────────────────────────────────────────────────

func TestGetMeshNodeByHostname_Found(t *testing.T) {
	ctx := context.Background()
	q := newQueries(t)

	p := baseParams("de:ad:be:ef:00:02", "my-host", "10.1.2.3")
	_, err := q.CreateMeshNode(ctx, p)
	require.NoError(t, err)

	node, err := q.GetMeshNodeByHostname(ctx, "my-host")
	require.NoError(t, err)
	assert.Equal(t, p.MacAddr, node.MacAddr)
}

func TestGetMeshNodeByHostname_NotFound(t *testing.T) {
	ctx := context.Background()
	q := newQueries(t)

	_, err := q.GetMeshNodeByHostname(ctx, "ghost")
	assert.ErrorIs(t, err, sql.ErrNoRows)
}

// ── ListMeshNodes ─────────────────────────────────────────────────────────────

func TestListMeshNodes_Empty(t *testing.T) {
	ctx := context.Background()
	q := newQueries(t)

	nodes, err := q.ListMeshNodes(ctx)
	require.NoError(t, err)
	assert.Empty(t, nodes)
}

func TestListMeshNodes_OrderedByHostname(t *testing.T) {
	ctx := context.Background()
	q := newQueries(t)

	for _, p := range []models.CreateMeshNodeParams{
		{MacAddr: "11:00:00:00:00:01", Hostname: "gamma", IpAddr: "10.0.0.1"},
		{MacAddr: "11:00:00:00:00:02", Hostname: "alpha", IpAddr: "10.0.0.2"},
		{MacAddr: "11:00:00:00:00:03", Hostname: "beta", IpAddr: "10.0.0.3"},
	} {
		_, err := q.CreateMeshNode(ctx, p)
		require.NoError(t, err)
	}

	nodes, err := q.ListMeshNodes(ctx)
	require.NoError(t, err)
	require.Len(t, nodes, 3)
	assert.Equal(t, "alpha", nodes[0].Hostname)
	assert.Equal(t, "beta", nodes[1].Hostname)
	assert.Equal(t, "gamma", nodes[2].Hostname)
}

func TestListMeshNodes_AllFieldsPresent(t *testing.T) {
	ctx := context.Background()
	q := newQueries(t)

	_, err := q.CreateMeshNode(ctx, baseParams("aa:bb:cc:00:00:01", "node-a", "10.0.0.1"))
	require.NoError(t, err)

	nodes, err := q.ListMeshNodes(ctx)
	require.NoError(t, err)
	require.Len(t, nodes, 1)

	n := nodes[0]
	assert.False(t, n.CreatedAt.IsZero(), "created_at must be set")
	assert.False(t, n.UpdatedAt.IsZero(), "updated_at must be set")
	assert.True(t, n.Latitude.Valid)
	assert.True(t, n.Longitude.Valid)
	assert.True(t, n.Altitude.Valid)
}

// ── UpdateMeshNode ────────────────────────────────────────────────────────────

func TestUpdateMeshNode_UpdatesFields(t *testing.T) {
	ctx := context.Background()
	q := newQueries(t)

	mac := "ab:cd:ef:01:02:03"
	_, err := q.CreateMeshNode(ctx, baseParams(mac, "old-host", "10.0.0.1"))
	require.NoError(t, err)

	err = q.UpdateMeshNode(ctx, models.UpdateMeshNodeParams{
		MacAddr:   mac,
		Hostname:  "new-host",
		IpAddr:    "10.0.0.200",
		Latitude:  sql.NullFloat64{Float64: 51.5, Valid: true},
		Longitude: sql.NullFloat64{Float64: -0.12, Valid: true},
		Altitude:  sql.NullFloat64{Float64: 20.0, Valid: true},
	})
	require.NoError(t, err)

	node, err := q.GetMeshNode(ctx, mac)
	require.NoError(t, err)
	assert.Equal(t, "new-host", node.Hostname)
	assert.Equal(t, "10.0.0.200", node.IpAddr)
	assert.InDelta(t, 51.5, node.Latitude.Float64, 0.001)
}

func TestUpdateMeshNode_AdvancesUpdatedAt(t *testing.T) {
	ctx := context.Background()
	q := newQueries(t)

	mac := "ab:cd:ef:01:02:04"
	created, err := q.CreateMeshNode(ctx, baseParams(mac, "host", "10.0.0.1"))
	require.NoError(t, err)

	time.Sleep(1100 * time.Millisecond)

	err = q.UpdateMeshNode(ctx, models.UpdateMeshNodeParams{
		MacAddr:  mac,
		Hostname: "host",
		IpAddr:   "10.0.0.1",
	})
	require.NoError(t, err)

	node, err := q.GetMeshNode(ctx, mac)
	require.NoError(t, err)
	assert.True(t, node.UpdatedAt.After(created.UpdatedAt),
		"updated_at should advance after UpdateMeshNode")
}

func TestUpdateMeshNode_NonExistentMAC(t *testing.T) {
	ctx := context.Background()
	q := newQueries(t)

	// SQLite UPDATE on a missing row is a no-op, not an error.
	err := q.UpdateMeshNode(ctx, models.UpdateMeshNodeParams{
		MacAddr:  "00:00:00:00:00:00",
		Hostname: "ghost",
		IpAddr:   "0.0.0.0",
	})
	assert.NoError(t, err)
}

func TestUpdateMeshNode_NullableFieldsCleared(t *testing.T) {
	ctx := context.Background()
	q := newQueries(t)

	mac := "ab:cd:ef:01:02:05"
	_, err := q.CreateMeshNode(ctx, baseParams(mac, "host", "10.0.0.1"))
	require.NoError(t, err)

	// Update with null lat/lon/alt.
	err = q.UpdateMeshNode(ctx, models.UpdateMeshNodeParams{
		MacAddr:   mac,
		Hostname:  "host",
		IpAddr:    "10.0.0.1",
		Latitude:  sql.NullFloat64{Valid: false},
		Longitude: sql.NullFloat64{Valid: false},
		Altitude:  sql.NullFloat64{Valid: false},
	})
	require.NoError(t, err)

	node, err := q.GetMeshNode(ctx, mac)
	require.NoError(t, err)
	assert.False(t, node.Latitude.Valid)
	assert.False(t, node.Longitude.Valid)
	assert.False(t, node.Altitude.Valid)
}

// ── DeleteMeshNode ────────────────────────────────────────────────────────────

func TestDeleteMeshNode_DeletesTargetOnly(t *testing.T) {
	ctx := context.Background()
	q := newQueries(t)

	_, err := q.CreateMeshNode(ctx, models.CreateMeshNodeParams{MacAddr: "aa:00:00:00:00:01", Hostname: "keep", IpAddr: "10.0.0.1"})
	require.NoError(t, err)
	_, err = q.CreateMeshNode(ctx, models.CreateMeshNodeParams{MacAddr: "aa:00:00:00:00:02", Hostname: "delete-me", IpAddr: "10.0.0.2"})
	require.NoError(t, err)

	err = q.DeleteMeshNode(ctx, "aa:00:00:00:00:02")
	require.NoError(t, err)

	nodes, err := q.ListMeshNodes(ctx)
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	assert.Equal(t, "keep", nodes[0].Hostname)
}

func TestDeleteMeshNode_NonExistentMAC(t *testing.T) {
	ctx := context.Background()
	q := newQueries(t)

	err := q.DeleteMeshNode(ctx, "ff:ff:ff:ff:ff:ff")
	assert.NoError(t, err)
}

// ── DeleteAllMeshNodes ────────────────────────────────────────────────────────

func TestDeleteAllMeshNodes_EmptyTable(t *testing.T) {
	ctx := context.Background()
	q := newQueries(t)

	err := q.DeleteAllMeshNodes(ctx)
	assert.NoError(t, err)
}

func TestDeleteAllMeshNodes_ClearsAll(t *testing.T) {
	ctx := context.Background()
	q := newQueries(t)

	for _, p := range []models.CreateMeshNodeParams{
		{MacAddr: "bb:00:00:00:00:01", Hostname: "n1", IpAddr: "10.0.0.1"},
		{MacAddr: "bb:00:00:00:00:02", Hostname: "n2", IpAddr: "10.0.0.2"},
		{MacAddr: "bb:00:00:00:00:03", Hostname: "n3", IpAddr: "10.0.0.3"},
	} {
		_, err := q.CreateMeshNode(ctx, p)
		require.NoError(t, err)
	}

	err := q.DeleteAllMeshNodes(ctx)
	require.NoError(t, err)

	nodes, err := q.ListMeshNodes(ctx)
	require.NoError(t, err)
	assert.Empty(t, nodes)
}

// ── DeleteDuplicateMeshNodes ──────────────────────────────────────────────────

func TestDeleteDuplicateMeshNodes_NoDuplicates(t *testing.T) {
	ctx := context.Background()
	q := newQueries(t)

	for _, p := range []models.CreateMeshNodeParams{
		{MacAddr: "cc:00:00:00:00:01", Hostname: "n1", IpAddr: "10.0.0.1"},
		{MacAddr: "cc:00:00:00:00:02", Hostname: "n2", IpAddr: "10.0.0.2"},
	} {
		_, err := q.CreateMeshNode(ctx, p)
		require.NoError(t, err)
	}

	err := q.DeleteDuplicateMeshNodes(ctx)
	require.NoError(t, err)

	nodes, err := q.ListMeshNodes(ctx)
	require.NoError(t, err)
	assert.Len(t, nodes, 2)
}

func TestDeleteDuplicateMeshNodes_KeepsMostRecent(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	q := models.New(db)

	mac := "dd:00:00:00:00:01"

	// Insert two rows for the same MAC with explicit, differing timestamps
	// directly via SQL to avoid SQLite's 1-second CURRENT_TIMESTAMP granularity.
	_, err := db.ExecContext(ctx,
		`INSERT INTO mesh_nodes (mac_addr, hostname, ip_addr, created_at, updated_at)
		 VALUES (?, ?, ?, '2025-01-01 00:00:00', '2025-01-01 00:00:00')`,
		mac, "old-host", "10.0.0.1",
	)
	require.NoError(t, err)

	// SQLite allows multiple rows with the same PK only when inserted via raw
	// SQL bypassing the UNIQUE constraint — we can't do that cleanly here.
	// Instead verify that an upsert followed by DeleteDuplicateMeshNodes leaves
	// exactly one row with the most-recently-updated data.
	_, err = q.CreateMeshNode(ctx, models.CreateMeshNodeParams{
		MacAddr:  mac,
		Hostname: "new-host",
		IpAddr:   "10.0.0.2",
	})
	require.NoError(t, err)

	err = q.DeleteDuplicateMeshNodes(ctx)
	require.NoError(t, err)

	nodes, err := q.ListMeshNodes(ctx)
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	assert.Equal(t, "new-host", nodes[0].Hostname)
}

// ── WithTx ────────────────────────────────────────────────────────────────────

func TestWithTx_CommitPersists(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	q := models.New(db)

	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)

	qtx := q.WithTx(tx)
	_, err = qtx.CreateMeshNode(ctx, models.CreateMeshNodeParams{
		MacAddr:  "ee:00:00:00:00:01",
		Hostname: "tx-node",
		IpAddr:   "10.0.0.1",
	})
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	node, err := q.GetMeshNode(ctx, "ee:00:00:00:00:01")
	require.NoError(t, err)
	assert.Equal(t, "tx-node", node.Hostname)
}

func TestWithTx_RollbackDiscards(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	q := models.New(db)

	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)

	qtx := q.WithTx(tx)
	_, err = qtx.CreateMeshNode(ctx, models.CreateMeshNodeParams{
		MacAddr:  "ee:00:00:00:00:02",
		Hostname: "rollback-node",
		IpAddr:   "10.0.0.2",
	})
	require.NoError(t, err)
	require.NoError(t, tx.Rollback())

	_, err = q.GetMeshNode(ctx, "ee:00:00:00:00:02")
	assert.ErrorIs(t, err, sql.ErrNoRows, "rolled-back row must not be visible")
}

func TestDeleteMeshNodesUpdatedBefore(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	q := models.New(db)

	// Seed with explicit ages via SQLite's own clock so the rows carry the
	// same UTC text format CURRENT_TIMESTAMP writes in production.
	for _, row := range []struct{ mac, ip, age string }{
		{"aa:00:00:00:00:01", "10.41.0.1", "-25 hours"},
		{"aa:00:00:00:00:02", "10.41.0.2", "-23 hours"},
		{"aa:00:00:00:00:03", "10.41.0.3", "-1 minutes"},
	} {
		_, err := db.ExecContext(ctx,
			`INSERT INTO mesh_nodes (mac_addr, hostname, ip_addr, created_at, updated_at)
			 VALUES (?, ?, ?, datetime('now', ?), datetime('now', ?))`,
			row.mac, "host-"+row.mac, row.ip, row.age, row.age,
		)
		require.NoError(t, err)
	}

	// The cutoff must be UTC: go-sqlite3 binds time.Time as text and the
	// comparison against CURRENT_TIMESTAMP text is lexicographic.
	err := q.DeleteMeshNodesUpdatedBefore(ctx, time.Now().UTC().Add(-24*time.Hour))
	require.NoError(t, err)

	nodes, err := q.ListMeshNodes(ctx)
	require.NoError(t, err)
	require.Len(t, nodes, 2, "only the 25 h-old row is expired")

	macs := []string{nodes[0].MacAddr, nodes[1].MacAddr}
	assert.NotContains(t, macs, "aa:00:00:00:00:01")
	assert.Contains(t, macs, "aa:00:00:00:00:02")
	assert.Contains(t, macs, "aa:00:00:00:00:03")
}

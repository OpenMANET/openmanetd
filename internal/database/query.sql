-- name: GetMeshNode :one
SELECT * FROM mesh_nodes
WHERE mac_addr = ? LIMIT 1;

-- name: GetMeshNodeByHostname :one
SELECT * FROM mesh_nodes
WHERE hostname = ? LIMIT 1;

-- name: ListMeshNodes :many
SELECT * FROM mesh_nodes
ORDER BY hostname;

-- name: CreateMeshNode :one
INSERT INTO mesh_nodes (
  mac_addr, hostname, ip_addr, latitude, longitude, altitude, uci_dhcp_start, uci_dhcp_limit, created_at, updated_at
) VALUES (
  ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
)
ON CONFLICT(mac_addr) DO UPDATE SET
  hostname = excluded.hostname,
  ip_addr = excluded.ip_addr,
  latitude = excluded.latitude,
  longitude = excluded.longitude,
  altitude = excluded.altitude,
  uci_dhcp_start = excluded.uci_dhcp_start,
  uci_dhcp_limit = excluded.uci_dhcp_limit,
  updated_at = CURRENT_TIMESTAMP
RETURNING *;

-- name: UpdateMeshNode :exec
UPDATE mesh_nodes
set hostname = ?,
ip_addr = ?,
latitude = ?,
longitude = ?,
altitude = ?,
uci_dhcp_start = ?,
uci_dhcp_limit = ?,
updated_at = CURRENT_TIMESTAMP
WHERE mac_addr = ?;

-- name: DeleteMeshNode :exec
DELETE FROM mesh_nodes
WHERE mac_addr = ?;

-- name: DeleteAllMeshNodes :exec
DELETE FROM mesh_nodes;

-- name: DeleteDuplicateMeshNodes :exec
DELETE FROM mesh_nodes
WHERE rowid NOT IN (
  SELECT mn.rowid
  FROM mesh_nodes mn
  INNER JOIN (
    SELECT mac_addr, MAX(updated_at) as max_updated_at
    FROM mesh_nodes
    GROUP BY mac_addr
  ) latest ON mn.mac_addr = latest.mac_addr AND mn.updated_at = latest.max_updated_at
);

-- name: DeleteMeshNodesUpdatedBefore :exec
DELETE FROM mesh_nodes
WHERE updated_at < ?;

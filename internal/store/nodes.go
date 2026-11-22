package store

import (
	"context"
	"encoding/json"
	"time"
)

// Node row. Capabilities and Usage are the JSON forms of the agent's
// Capabilities / ResourceUsage messages (protojson, snake_case field names).
type Node struct {
	ID             string
	Name           string
	Address        string
	Enrolled       bool
	CertSerial     string
	TokenExpiresAt *time.Time
	AgentVersion   string
	LastSeen       *time.Time
	Capabilities   json.RawMessage
	Usage          json.RawMessage
	CreatedAt      time.Time
}

const nodeCols = `id, name, address, enrolled, cert_serial, token_expires_at, agent_version, last_seen, capabilities, usage, created_at`

func scanNode(row interface{ Scan(...any) error }) (*Node, error) {
	n := &Node{}
	if err := row.Scan(&n.ID, &n.Name, &n.Address, &n.Enrolled, &n.CertSerial, &n.TokenExpiresAt, &n.AgentVersion, &n.LastSeen, &n.Capabilities, &n.Usage, &n.CreatedAt); err != nil {
		return nil, mapErr(err)
	}
	return n, nil
}

// CreateNode inserts a node with an enrollment token hash.
func (s *Store) CreateNode(ctx context.Context, n *Node, tokenHash string, tokenExpires time.Time) error {
	err := s.pool.QueryRow(ctx, `INSERT INTO nodes (id, name, address, token_hash, token_expires_at) VALUES ($1,$2,$3,$4,$5) RETURNING created_at`,
		n.ID, n.Name, n.Address, tokenHash, tokenExpires).Scan(&n.CreatedAt)
	n.TokenExpiresAt = &tokenExpires
	n.Capabilities, n.Usage = json.RawMessage(`{}`), json.RawMessage(`{}`)
	return mapErr(err)
}

// GetNode by ID.
func (s *Store) GetNode(ctx context.Context, id string) (*Node, error) {
	return scanNode(s.pool.QueryRow(ctx, `SELECT `+nodeCols+` FROM nodes WHERE id=$1`, id))
}

// ListNodes returns all nodes ordered by name.
func (s *Store) ListNodes(ctx context.Context) ([]*Node, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+nodeCols+` FROM nodes ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// UpdateNodeMeta changes name and address.
func (s *Store) UpdateNodeMeta(ctx context.Context, id, name, address string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE nodes SET name=$2, address=$3 WHERE id=$1`, id, name, address)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ResetNodeToken sets a new enrollment token and revokes the current
// certificate (the node must enroll again).
func (s *Store) ResetNodeToken(ctx context.Context, id, tokenHash string, expires time.Time) error {
	tag, err := s.pool.Exec(ctx, `UPDATE nodes SET token_hash=$2, token_expires_at=$3, cert_serial='', enrolled=false WHERE id=$1`, id, tokenHash, expires)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// NodeByToken returns the node with a valid (unexpired) enrollment token.
func (s *Store) NodeByToken(ctx context.Context, tokenHash string) (*Node, error) {
	return scanNode(s.pool.QueryRow(ctx, `SELECT `+nodeCols+` FROM nodes WHERE token_hash=$1 AND token_expires_at > now()`, tokenHash))
}

// CompleteEnrollment stores the issued certificate serial and consumes the token.
func (s *Store) CompleteEnrollment(ctx context.Context, id, serial string) error {
	_, err := s.pool.Exec(ctx, `UPDATE nodes SET enrolled=true, cert_serial=$2, token_hash=NULL, token_expires_at=NULL WHERE id=$1`, id, serial)
	return mapErr(err)
}

// UpdateNodeConnection stores agent version and capabilities on Hello.
func (s *Store) UpdateNodeConnection(ctx context.Context, id, version string, caps json.RawMessage) error {
	_, err := s.pool.Exec(ctx, `UPDATE nodes SET agent_version=$2, capabilities=$3, last_seen=now() WHERE id=$1`, id, version, caps)
	return mapErr(err)
}

// UpdateNodeCapabilities stores new capabilities.
func (s *Store) UpdateNodeCapabilities(ctx context.Context, id string, caps json.RawMessage) error {
	_, err := s.pool.Exec(ctx, `UPDATE nodes SET capabilities=$2, last_seen=now() WHERE id=$1`, id, caps)
	return mapErr(err)
}

// TouchNode records a heartbeat (usage may be nil to only bump last_seen).
func (s *Store) TouchNode(ctx context.Context, id string, usage json.RawMessage) error {
	if usage == nil {
		_, err := s.pool.Exec(ctx, `UPDATE nodes SET last_seen=now() WHERE id=$1`, id)
		return err
	}
	_, err := s.pool.Exec(ctx, `UPDATE nodes SET last_seen=now(), usage=$2 WHERE id=$1`, id, usage)
	return err
}

// DeleteNode deletes a node without devices (ErrConflict otherwise).
func (s *Store) DeleteNode(ctx context.Context, id string) error {
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM devices WHERE node_id=$1`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrConflict
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM nodes WHERE id=$1`, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/finn-freitag/tossbox/internal/model"
	"github.com/jackc/pgx/v5"
)

// Service row (one exposed service of a device).
type Service struct {
	Name         string
	InternalPort int
	PublicPort   int
}

// Device row.
type Device struct {
	ID             string
	OwnerID        string
	OwnerName      string // joined
	NodeID         string // "" if unassigned
	Driver         string
	TemplateID     string
	TemplateName   string
	TemplateFamily string
	TemplateSpec   json.RawMessage
	Name           string
	Hostname       string
	Res            model.Resources
	Power          string
	State          string
	Ready          bool
	StatusMessage  string
	InternalIP     string
	StorageUsedMB  int64
	MemoryUsedMB   int64
	CPUUsedPercent float64
	UsageAt        *time.Time
	Exposure       string
	Allowlist      []string
	LicenseMode    string
	LicensePool    string // from license_leases
	LicenseKeyEnc  string
	Username       string
	PasswordEnc    string
	SSHKeys        []string
	ADBKeys        []string
	NetworkPolicy  string
	GPU            bool
	Params         map[string]string
	TTLHours       int
	ExpiresAt      *time.Time
	Revision       int64
	Deleting       bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
	Services       []Service
}

// Service returns the named service or nil.
func (d *Device) Service(name string) *Service {
	for i := range d.Services {
		if d.Services[i].Name == name {
			return &d.Services[i]
		}
	}
	return nil
}

const deviceSelect = `SELECT d.id, d.owner_id, u.username, coalesce(d.node_id::text, ''), d.driver, d.template_id, d.template_name,
	d.template_family, d.template_spec, d.name, d.hostname, d.vcpus, d.cpu_cap_percent, d.ram_mb, d.storage_gb, d.power,
	d.state, d.ready, d.status_message, d.internal_ip, d.storage_used_mb, d.exposure, d.allowlist, d.license_mode,
	coalesce(l.pool, ''), d.license_key_enc, d.username, d.password_enc, d.ssh_keys, d.params, d.ttl_hours, d.expires_at,
	d.revision, d.deleting, d.created_at, d.updated_at, d.adb_keys, d.network_policy, d.gpu, d.memory_used_mb, d.cpu_used_percent, d.usage_at,
	coalesce((SELECT json_agg(json_build_array(s.name, s.internal_port, s.public_port) ORDER BY s.public_port)
	          FROM device_services s WHERE s.device_id = d.id), '[]')
	FROM devices d JOIN users u ON u.id = d.owner_id LEFT JOIN license_leases l ON l.device_id = d.id`

func scanDevice(row interface{ Scan(...any) error }) (*Device, error) {
	d := &Device{}
	var svcs [][]any
	err := row.Scan(&d.ID, &d.OwnerID, &d.OwnerName, &d.NodeID, &d.Driver, &d.TemplateID, &d.TemplateName,
		&d.TemplateFamily, &d.TemplateSpec, &d.Name, &d.Hostname, &d.Res.VCPUs, &d.Res.CPUCapPercent, &d.Res.RAMMB, &d.Res.StorageGB, &d.Power,
		&d.State, &d.Ready, &d.StatusMessage, &d.InternalIP, &d.StorageUsedMB, &d.Exposure, &d.Allowlist, &d.LicenseMode,
		&d.LicensePool, &d.LicenseKeyEnc, &d.Username, &d.PasswordEnc, &d.SSHKeys, &d.Params, &d.TTLHours, &d.ExpiresAt,
		&d.Revision, &d.Deleting, &d.CreatedAt, &d.UpdatedAt, &d.ADBKeys, &d.NetworkPolicy, &d.GPU, &d.MemoryUsedMB, &d.CPUUsedPercent, &d.UsageAt, &svcs)
	if err != nil {
		return nil, mapErr(err)
	}
	for _, s := range svcs {
		if len(s) != 3 {
			continue
		}
		name, _ := s[0].(string)
		ip, _ := s[1].(float64)
		pp, _ := s[2].(float64)
		d.Services = append(d.Services, Service{Name: name, InternalPort: int(ip), PublicPort: int(pp)})
	}
	if d.Allowlist == nil {
		d.Allowlist = []string{}
	}
	if d.Params == nil {
		d.Params = map[string]string{}
	}
	return d, nil
}

func (s *Store) queryDevices(ctx context.Context, where string, args ...any) ([]*Device, error) {
	rows, err := s.pool.Query(ctx, deviceSelect+" "+where+" ORDER BY d.created_at", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Device{}
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetDevice by ID.
func (s *Store) GetDevice(ctx context.Context, id string) (*Device, error) {
	return scanDevice(s.pool.QueryRow(ctx, deviceSelect+` WHERE d.id=$1`, id))
}

// ListDevices returns all devices (ownerID "" = all users).
func (s *Store) ListDevices(ctx context.Context, ownerID string) ([]*Device, error) {
	if ownerID == "" {
		return s.queryDevices(ctx, "")
	}
	return s.queryDevices(ctx, "WHERE d.owner_id=$1", ownerID)
}

// ListNodeDevices returns all devices assigned to a node.
func (s *Store) ListNodeDevices(ctx context.Context, nodeID string) ([]*Device, error) {
	return s.queryDevices(ctx, "WHERE d.node_id=$1", nodeID)
}

// ListExpiredDevices returns devices whose TTL has passed and that are not being deleted.
func (s *Store) ListExpiredDevices(ctx context.Context, now time.Time) ([]*Device, error) {
	return s.queryDevices(ctx, "WHERE d.expires_at IS NOT NULL AND d.expires_at < $1 AND NOT d.deleting", now)
}

// UsedPorts returns all allocated public ports.
func (s *Store) UsedPorts(ctx context.Context) (map[int]bool, error) {
	rows, err := s.pool.Query(ctx, `SELECT public_port FROM device_services`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	used := map[int]bool{}
	for rows.Next() {
		var p int
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		used[p] = true
	}
	return used, rows.Err()
}

// HostnameTaken reports whether a hostname is in use.
func (s *Store) HostnameTaken(ctx context.Context, h string) (bool, error) {
	var b bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM devices WHERE hostname=$1)`, h).Scan(&b)
	return b, err
}

// InsertDevice inserts a device with its services and license lease in one transaction.
func (s *Store) InsertDevice(ctx context.Context, d *Device) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var node any
		if d.NodeID != "" {
			node = d.NodeID
		}
		if d.Allowlist == nil {
			d.Allowlist = []string{}
		}
		if d.SSHKeys == nil {
			d.SSHKeys = []string{}
		}
		if d.ADBKeys == nil {
			d.ADBKeys = []string{}
		}
		if d.NetworkPolicy == "" {
			d.NetworkPolicy = "internet"
		}
		if d.Params == nil {
			d.Params = map[string]string{}
		}
		err := tx.QueryRow(ctx, `INSERT INTO devices (id, owner_id, node_id, driver, template_id, template_name, template_family,
			template_spec, name, hostname, vcpus, cpu_cap_percent, ram_mb, storage_gb, power, state, status_message, exposure, allowlist,
			license_mode, license_key_enc, username, password_enc, ssh_keys, params, ttl_hours, expires_at, revision, adb_keys, network_policy, gpu)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31)
			RETURNING created_at, updated_at`,
			d.ID, d.OwnerID, node, d.Driver, d.TemplateID, d.TemplateName, d.TemplateFamily, d.TemplateSpec, d.Name, d.Hostname,
			d.Res.VCPUs, d.Res.CPUCapPercent, d.Res.RAMMB, d.Res.StorageGB, d.Power, d.State, d.StatusMessage, d.Exposure, d.Allowlist,
			d.LicenseMode, d.LicenseKeyEnc, d.Username, d.PasswordEnc, d.SSHKeys, d.Params, d.TTLHours, d.ExpiresAt, d.Revision, d.ADBKeys, d.NetworkPolicy, d.GPU,
		).Scan(&d.CreatedAt, &d.UpdatedAt)
		if err != nil {
			return mapErr(err)
		}
		for _, svc := range d.Services {
			if _, err := tx.Exec(ctx, `INSERT INTO device_services (device_id, name, internal_port, public_port) VALUES ($1,$2,$3,$4)`,
				d.ID, svc.Name, svc.InternalPort, svc.PublicPort); err != nil {
				return mapErr(err)
			}
		}
		if d.LicensePool != "" && d.NodeID != "" {
			if _, err := tx.Exec(ctx, `INSERT INTO license_leases (device_id, node_id, pool) VALUES ($1,$2,$3)`, d.ID, d.NodeID, d.LicensePool); err != nil {
				return mapErr(err)
			}
		}
		return nil
	})
}

// DeviceStatus is the actual-state part of a device reported by the agent.
type DeviceStatus struct {
	State          string
	Ready          bool
	Message        string
	InternalIP     string
	StorageUsedMB  int64
	MemoryUsedMB   int64
	CPUUsedPercent float64
}

// UpdateDeviceStatus stores the agent-reported status. It returns false if
// the device doesn't exist (any more).
func (s *Store) UpdateDeviceStatus(ctx context.Context, id string, st DeviceStatus) (bool, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE devices SET state=$2, ready=$3, status_message=$4, internal_ip=$5, storage_used_mb=$6,
		memory_used_mb=$7, cpu_used_percent=$8, usage_at=now(), updated_at=now() WHERE id=$1`,
		id, st.State, st.Ready, st.Message, st.InternalIP, st.StorageUsedMB, st.MemoryUsedMB, st.CPUUsedPercent)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// DeviceUpdate holds optional user-editable fields.
type DeviceUpdate struct {
	Name      *string
	Power     *string
	TTLHours  *int
	ExpiresAt **time.Time
	Allowlist *[]string
}

// UpdateDevice applies a DeviceUpdate and bumps the revision.
func (s *Store) UpdateDevice(ctx context.Context, id string, u DeviceUpdate) error {
	d, err := s.GetDevice(ctx, id)
	if err != nil {
		return err
	}
	if u.Name != nil {
		d.Name = *u.Name
	}
	if u.Power != nil {
		d.Power = *u.Power
	}
	if u.TTLHours != nil {
		d.TTLHours = *u.TTLHours
	}
	if u.ExpiresAt != nil {
		d.ExpiresAt = *u.ExpiresAt
	}
	if u.Allowlist != nil {
		d.Allowlist = *u.Allowlist
	}
	_, err = s.pool.Exec(ctx, `UPDATE devices SET name=$2, power=$3, ttl_hours=$4, expires_at=$5, allowlist=$6,
		revision=revision+1, updated_at=now() WHERE id=$1`, id, d.Name, d.Power, d.TTLHours, d.ExpiresAt, d.Allowlist)
	return mapErr(err)
}

// MarkDeleting flags a device for deletion.
func (s *Store) MarkDeleting(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `UPDATE devices SET deleting=true, state='destroying', status_message='Destroying', revision=revision+1, updated_at=now() WHERE id=$1`, id)
	return mapErr(err)
}

// DeleteDevice removes a device row (services and lease cascade, freeing ports).
func (s *Store) DeleteDevice(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM devices WHERE id=$1`, id)
	return mapErr(err)
}

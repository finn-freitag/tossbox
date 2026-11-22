# Tossbox REST API (v1)

Contract between the Go backend (`internal/`) and the React UI (`web/`), and the public API for automation (CI).

General rules:

- Base path: `/api`. JSON in, JSON out (`Content-Type: application/json`), except where noted.
- Times: RFC 3339 strings (UTC). IDs: UUID strings.
- **Auth:** either a session cookie `tossbox_session` (set by `POST /api/auth/login`, HttpOnly, SameSite=Lax), or `Authorization: Bearer <api token>`. Cookie-authenticated, state-changing requests must send the header `X-Tossbox-CSRF: 1` (simple CSRF guard; the UI always sends it).
- **Errors:** non-2xx with body `{"error": "human readable message", "code": "machine_code"}`. Codes include `unauthorized`, `forbidden`, `not_found`, `validation`, `quota_exceeded`, `no_capacity`, `conflict`, `internal`.
- Numbers: `0` or empty list in quota fields means **unlimited / all allowed**.
- Resource units: `ram_mb` (MiB), `storage_gb` (GiB), `cpu_cap_percent` (percent of the node's CPU), `vcpus` (count).

---

## Public

### `GET /api/info`
No auth.
```json
{
  "version": "0.1.0",
  "base_domain": "servers.finnfreitag.com",
  "guacamole_enabled": true,
  "setup_required": false
}
```

---

## Auth and profile

### `POST /api/auth/login`
`{"username": "admin", "password": "..."}` → `200` user object (see `GET /api/auth/me`) and sets the cookie. `401` on bad credentials.

### `POST /api/auth/logout`
→ `204`.

### `GET /api/auth/me`
```json
{
  "id": "uuid",
  "username": "finn",
  "role": "admin",
  "ssh_keys": ["ssh-ed25519 AAAA... finn@laptop"],
  "adb_keys": [],
  "quota": { ...Quota },
  "usage": { ...Usage }
}
```

### `PUT /api/me/ssh-keys`
`{"ssh_keys": ["ssh-ed25519 ..."]}` → updated user. These keys are added to every new device of the user.

### `PUT /api/me/adb-keys`
`{"adb_keys": ["QAAAA... you@laptop"]}` → updated user. adb public keys (content of `~/.android/adbkey.pub`), copied to every new Android device of the user. Devices with adb authentication only accept these keys.

### `POST /api/me/password`
`{"current_password": "...", "new_password": "..."}` → `204`.

### `GET /api/me/tokens`
`[{"id": "uuid", "name": "gitlab-ci", "created_at": "...", "last_used_at": null, "expires_at": null}]`

### `POST /api/me/tokens`
`{"name": "gitlab-ci", "expires_in_days": 0}` → `201` `{"id": "...", "name": "...", "token": "tbt_..."}`. The plain token is only returned once.

### `DELETE /api/me/tokens/{id}`
→ `204`.

---

## Shared objects

### Quota
```json
{
  "max_machines": 5,
  "max_vcpus": 16,
  "max_ram_mb": 32768,
  "max_storage_gb": 500,
  "max_phones": 1,
  "allowed_drivers": ["libvirt", "lxc"],
  "allowed_templates": [],
  "allowed_license_pools": ["win-retail-pro"],
  "allowed_exposures": ["public", "tailscale", "none"],
  "max_ttl_hours": 0
}
```

### Usage
```json
{ "machines": 2, "vcpus": 6, "ram_mb": 12288, "storage_gb": 214, "phones": 0 }
```

### Resources
```json
{ "vcpus": 4, "cpu_cap_percent": 50, "ram_mb": 8192, "storage_gb": 150 }
```

---

## Templates

### `GET /api/templates`
Templates visible to the current user (filtered by `allowed_templates` and `allowed_drivers`).
```json
[
  {
    "id": "debian-13",
    "name": "Debian 13 (Trixie)",
    "description": "Official Debian cloud image",
    "family": "linux",
    "version": "1",
    "icon": "debian",
    "compatible_drivers": ["libvirt", "docker-qemu"],
    "available_drivers": ["libvirt"],
    "supported_services": ["ssh", "vnc", "http", "https"],
    "default_services": ["ssh"],
    "min_resources": { "vcpus": 1, "cpu_cap_percent": 0, "ram_mb": 512, "storage_gb": 8 },
    "default_resources": { "vcpus": 2, "cpu_cap_percent": 0, "ram_mb": 2048, "storage_gb": 20 },
    "license": false,
    "source": "https://raw.githubusercontent.com/finn-freitag/tb-templates/main/catalog.json"
  }
]
```
`available_drivers`: compatible drivers that are enabled on at least one online node and allowed for the user. `default_services` = the global default services (ssh, rdp on; vnc off), filtered to what the template supports. `license: true` for Windows templates.

### `POST /api/templates/reload` (admin)
Re-reads all template sources. → `{"count": 12, "errors": []}`.

---

## Create options

### `GET /api/create-options`
Everything the create-device form needs in one call.
```json
{
  "templates": [ ...Template ],
  "nodes": [
    {
      "id": "uuid", "name": "homelab", "status": "online",
      "drivers": ["libvirt", "lxc"],
      "free": { "vcpus": 0, "cpu_percent": 20, "ram_mb": 8192, "storage_gb": 150 },
      "fixed_slots_free": { "orchard": 0 },
      "license_pools": [ { "name": "win-retail-pro", "type": "retail", "free": 2 } ],
      "host_cores": 12
    }
  ],
  "license_pools": [ { "name": "win-retail-pro", "type": "retail", "free": 2 } ],
  "quota": { ...Quota },
  "usage": { ...Usage },
  "allowed_exposures": ["public", "tailscale", "none"],
  "driver_services": { "libvirt": ["ssh", "rdp", "vnc", "http", "https"], "lxc": ["ssh", "http", "https"] },
  "default_ttl_hours": 0,
  "max_ttl_hours": 0,
  "service_catalog": [
    { "name": "ssh", "label": "SSH", "default": true, "desktop_only": false },
    { "name": "rdp", "label": "RDP", "default": true, "desktop_only": true },
    { "name": "vnc", "label": "VNC", "default": false, "desktop_only": true, "warning": "VNC is unencrypted; prefer Guacamole, an SSH tunnel or Tailscale" },
    { "name": "adb", "label": "ADB", "default": true, "desktop_only": false },
    { "name": "http", "label": "HTTP (port 80)", "default": false, "desktop_only": false },
    { "name": "https", "label": "HTTPS (port 443)", "default": false, "desktop_only": false }
  ]
}
```
`driver_services`: union of `services` reported by online nodes per driver (a service is offered only if template **and** driver support it). `license_pools` lists only pools the user may use; `free` is summed over online nodes. Nodes are visible to all users here (name, drivers, free capacity); full node details are admin only.

---

## Devices

### Device object
```json
{
  "id": "uuid",
  "name": "windowsGitlabBuildMachine1",
  "hostname": "windowsgitlabbuildmachine1",
  "fqdn": "windowsgitlabbuildmachine1.servers.finnfreitag.com",
  "owner": { "id": "uuid", "username": "finn" },
  "node": { "id": "uuid", "name": "homelab" },
  "driver": "libvirt",
  "template": { "id": "win11-pro", "name": "Windows 11 Pro", "family": "windows" },
  "resources": { "vcpus": 4, "cpu_cap_percent": 50, "ram_mb": 8192, "storage_gb": 150 },
  "power": "running",
  "state": "provisioning",
  "ready": false,
  "status_message": "Downloading image (42%)",
  "internal_ip": "192.168.122.31",
  "exposure": "public",
  "allowlist": [],
  "services": [
    {
      "name": "ssh", "port": 40000, "internal_port": 22,
      "username": "tossbox", "password": "Zq...24 chars...",
      "connect": "ssh -p 40000 tossbox@windowsgitlabbuildmachine1.servers.finnfreitag.com"
    },
    {
      "name": "rdp", "port": 40001, "internal_port": 3389,
      "username": "tossbox", "password": "...",
      "connect": "windowsgitlabbuildmachine1.servers.finnfreitag.com:40001"
    },
    {
      "name": "http", "port": 40002, "internal_port": 80,
      "connect": "http://windowsgitlabbuildmachine1.servers.finnfreitag.com"
    }
  ],
  "license": { "mode": "pool", "pool": "win-retail-pro" },
  "ttl_hours": 24,
  "expires_at": "2026-11-23T16:00:00Z",
  "created_at": "2026-11-22T16:00:00Z",
  "updated_at": "2026-11-22T16:01:10Z",
  "params": {}
}
```
- `power`: desired power, `"running" | "stopped"` (the on/off switch).
- `state`: actual state reported by the node: `pending | provisioning | running | stopping | stopped | destroying | destroyed | failed`. `"offline"` when the node is disconnected.
- `services[].password`: plaintext (decrypted for the owner and admins only). License user keys are never returned.

### `GET /api/devices`
Own devices; admins get all devices, and can filter with `?owner=<user id>` or `?all=false`. → `[Device]`.

### `POST /api/devices`
```json
{
  "name": "windowsGitlabBuildMachine1",
  "template_id": "win11-pro",
  "driver": "",
  "node_id": "",
  "resources": { "vcpus": 4, "cpu_cap_percent": 50, "ram_mb": 8192, "storage_gb": 150 },
  "services": ["ssh", "rdp"],
  "exposure": "public",
  "allowlist": [],
  "ttl_hours": 24,
  "license": { "mode": "pool", "pool": "win-retail-pro", "user_key": "" },
  "power": "running",
  "params": {}
}
```
- `driver` / `node_id` empty = chosen by the scheduler.
- `services` omitted = template default services. Services not supported by template **and** driver → `400 validation`.
- `power` defaults to `"running"`.
- `ttl_hours`: hours counted from creation (`expires_at = created_at + ttl_hours`); `0` = no expiry. The same applies to `PATCH`.
- `exposure` defaults to `"public"`; must be in the user's `allowed_exposures`.
- `license` only for Windows templates; default `{"mode": "none"}`.
- `hostname` is derived from `name` (lowercase, `[a-z0-9-]`, max 63 chars). Must be unique across all users and not reserved → `409 conflict`.

→ `201` Device. Errors: `400 validation`, `403 forbidden` (driver/template/pool/exposure not allowed), `409 conflict`, `422 quota_exceeded`, `503 no_capacity`.

### `GET /api/devices/{id}`
→ Device.

### `PATCH /api/devices/{id}`
Any subset of `{"power": "stopped", "name": "...", "ttl_hours": 48, "allowlist": [...]}` → Device. (`name` changes only the display name; the hostname is fixed.)

### `DELETE /api/devices/{id}`
→ `202` (the device moves to `destroying`; it disappears from lists once the node reports `destroyed`, or immediately if the node was never assigned).

### `GET /api/devices/{id}/rdp`
→ `.rdp` file download (`Content-Type: application/x-rdp`), full address + username.

### `GET /api/devices/{id}/ssh-config`
→ `text/plain` OpenSSH config snippet (`Host <hostname>` block).

### `POST /api/devices/{id}/guacamole`
Optional body `{"service": "rdp"}` (default: rdp, else vnc, else ssh). → `{"url": "https://guacamole.servers.finnfreitag.com/#/?data=..."}`. `404` if Guacamole isn't configured.

---

## Nodes (admin)

### Node object
```json
{
  "id": "uuid",
  "name": "homelab",
  "address": "100.64.0.12",
  "status": "online",
  "agent_version": "0.1.0",
  "last_seen": "2026-11-22T16:00:00Z",
  "enrolled": true,
  "capabilities": {
    "arch": "x86_64", "os": "linux", "host_cores": 12, "host_ram_mb": 65536, "host_storage_gb": 1800,
    "limits": { "ram_mb": 16384, "cpu_percent": 70, "storage_gb": 300 },
    "drivers": [ { "name": "libvirt", "families": ["linux", "windows"], "services": ["ssh","rdp","vnc","http","https"], "backends": [], "fixed_slots": 0, "healthy": true, "message": "", "physical_devices": [] } ],
    "license_pools": [ { "name": "win-retail-pro", "type": "retail", "total": 3, "in_use": 1 } ]
  },
  "allocated": { "vcpus": 6, "cpu_percent": 50, "ram_mb": 8192, "storage_gb": 150, "devices": 2 },
  "usage": { "ram_used_mb": 6100, "cpu_used_percent": 12.5, "storage_used_gb": 31, "host_ram_used_mb": 21000, "host_storage_used_gb": 722, "host_storage_total_gb": 914 }
}
```
`usage` comes from the agent heartbeat (every 15 s) and is measured, not allocated: `ram_used_mb` is the memory running devices really use (docker stats, libvirt QEMU RSS, Incus state); `cpu_used_percent` is whole-host CPU; `storage_used_gb` is the disk devices really occupy; `host_*` cover the whole host (including non-Tossbox workloads) and the filesystem of the agent's data dir.
`status`: `pending` (not enrolled yet) | `online` | `offline`.

### `GET /api/nodes` → `[Node]`
### `GET /api/nodes/{id}` → Node
### `POST /api/nodes`
`{"name": "homelab", "address": "100.64.0.12"}` → `201` `{"node": Node, "enrollment_token": "tb1.<fp>.<secret>", "grpc_endpoint": "dashboard.example.com:7443"}`.
`address` is where the **gateway** reaches the node's forwarded ports (e.g. its Tailscale IP).
### `PATCH /api/nodes/{id}` → `{"name": "...", "address": "..."}` → Node
### `POST /api/nodes/{id}/token`
Revokes the node's certificate and issues a new enrollment token → same shape as `POST /api/nodes`.
### `DELETE /api/nodes/{id}`
Only if the node has no devices (`409` otherwise) → `204`.

---

## Users (admin)

### User object
Same as `GET /api/auth/me` plus `"created_at"`.

### `GET /api/users` → `[User]`
### `POST /api/users`
`{"username": "...", "password": "...", "role": "user", "quota": {...}}` → `201` User.
### `PATCH /api/users/{id}`
Any subset of `{"password": "...", "role": "...", "quota": {...}}` → User.
### `DELETE /api/users/{id}`
`409` if the user still owns devices → `204`.

---

## Settings (admin)

### `GET /api/settings`
```json
{
  "overcommit_ram": false,
  "ram_overcommit_ratio": 1.0,
  "overcommit_cpu": true,
  "cpu_overcommit_ratio": 4.0,
  "storage_accounting": "provisioned",
  "stopped_keeps_reservation": true,
  "default_services": ["ssh", "rdp"],
  "password_length": 24,
  "default_ttl_hours": 0,
  "api_requires_ttl": true,
  "public_vnc_allowed": true,
  "public_adb_allowed": false
}
```
### `PUT /api/settings`
Full or partial object → settings. `password_length` must be ≥ 20.

---

## Server-sent events

### `GET /api/events`
`text/event-stream`. Events: `device` (data: Device JSON), `device_deleted` (data: `{"id": "..."}`), `node` (data: Node JSON, admin only). The UI uses this to update lists live; it re-fetches on reconnect.

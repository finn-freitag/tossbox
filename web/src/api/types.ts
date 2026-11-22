// Types mirroring tossbox/docs/API.md (the contract). Keep in sync.

export type Role = 'admin' | 'user'
export type Family = 'linux' | 'windows' | 'android' | 'macos' | 'ios'
export type ServiceName = 'ssh' | 'rdp' | 'vnc' | 'adb' | 'http' | 'https'
export type Exposure = 'public' | 'tailscale' | 'none'
export type Power = 'running' | 'stopped'
export type DeviceState =
  | 'pending'
  | 'provisioning'
  | 'running'
  | 'stopping'
  | 'stopped'
  | 'destroying'
  | 'destroyed'
  | 'failed'
  | 'offline'
export type LicenseMode = 'none' | 'user_key' | 'pool'
export type NodeStatus = 'pending' | 'online' | 'offline'

export interface ApiErrorBody {
  error: string
  code: string
}

export interface Info {
  version: string
  base_domain: string
  guacamole_enabled: boolean
  setup_required: boolean
}

export interface Quota {
  max_machines: number
  max_vcpus: number
  max_ram_mb: number
  max_storage_gb: number
  max_phones: number
  allowed_drivers: string[]
  allowed_templates: string[]
  allowed_license_pools: string[]
  allowed_exposures: Exposure[]
  max_ttl_hours: number
}

export interface Usage {
  machines: number
  vcpus: number
  ram_mb: number
  storage_gb: number
  phones: number
}

export interface Resources {
  vcpus: number
  cpu_cap_percent: number
  ram_mb: number
  storage_gb: number
}

export interface User {
  id: string
  username: string
  role: Role
  ssh_keys: string[]
  quota: Quota
  usage: Usage
  created_at?: string
}

export interface ApiToken {
  id: string
  name: string
  created_at: string
  last_used_at: string | null
  expires_at: string | null
}

export interface CreatedToken {
  id: string
  name: string
  token: string
}

export interface Template {
  id: string
  name: string
  description: string
  family: Family
  version: string
  icon: string
  compatible_drivers: string[]
  available_drivers: string[]
  supported_services: ServiceName[]
  default_services: ServiceName[]
  min_resources: Resources
  default_resources: Resources
  license: boolean
  source: string
}

export interface LicensePoolSummary {
  name: string
  type: string
  free: number
}

export interface NodeFree {
  vcpus: number
  cpu_percent: number
  ram_mb: number
  storage_gb: number
}

export interface CreateOptionNode {
  id: string
  name: string
  status: NodeStatus
  drivers: string[]
  free: NodeFree
  fixed_slots_free: Record<string, number>
  license_pools: LicensePoolSummary[]
  host_cores: number
}

export interface ServiceCatalogEntry {
  name: ServiceName
  label: string
  default: boolean
  desktop_only: boolean
  warning?: string
}

export interface CreateOptions {
  templates: Template[]
  nodes: CreateOptionNode[]
  license_pools: LicensePoolSummary[]
  quota: Quota
  usage: Usage
  allowed_exposures: Exposure[]
  /** Services per driver, reported by online nodes. */
  driver_services?: Record<string, ServiceName[]>
  default_ttl_hours: number
  max_ttl_hours: number
  service_catalog: ServiceCatalogEntry[]
}

export interface DeviceService {
  name: ServiceName
  port: number
  internal_port: number
  username?: string
  password?: string
  connect: string
}

export interface Device {
  id: string
  name: string
  hostname: string
  fqdn: string
  owner: { id: string; username: string }
  node: { id: string; name: string } | null
  driver: string
  template: { id: string; name: string; family: Family }
  resources: Resources
  power: Power
  state: DeviceState
  ready: boolean
  status_message: string
  internal_ip: string
  exposure: Exposure
  allowlist: string[]
  services: DeviceService[]
  license: { mode: LicenseMode; pool?: string }
  ttl_hours: number
  expires_at: string | null
  created_at: string
  updated_at: string
  params: Record<string, string>
}

export interface CreateDeviceRequest {
  name: string
  template_id: string
  driver?: string
  node_id?: string
  resources: Resources
  services?: ServiceName[]
  exposure?: Exposure
  allowlist?: string[]
  ttl_hours?: number
  license?: { mode: LicenseMode; pool?: string; user_key?: string }
  power?: Power
  params?: Record<string, string>
}

export interface PatchDeviceRequest {
  power?: Power
  name?: string
  ttl_hours?: number
  allowlist?: string[]
}

export interface DriverCapabilities {
  name: string
  families: string[]
  services: string[]
  backends: string[]
  fixed_slots: number
  healthy: boolean
  message: string
  physical_devices: { serial: string; family: string; model: string; available: boolean }[]
}

export interface NodeCapabilities {
  arch: string
  os: string
  host_cores: number
  host_ram_mb: number
  host_storage_gb: number
  limits: { ram_mb: number; cpu_percent: number; storage_gb: number }
  drivers: DriverCapabilities[]
  license_pools: { name: string; type: string; total: number; in_use: number }[]
}

export interface Node {
  id: string
  name: string
  address: string
  status: NodeStatus
  agent_version: string
  last_seen: string | null
  enrolled: boolean
  capabilities: NodeCapabilities | null
  allocated: { vcpus: number; cpu_percent: number; ram_mb: number; storage_gb: number; devices: number }
  usage: { ram_used_mb: number; cpu_used_percent: number; storage_used_gb: number } | null
}

export interface NodeEnrollment {
  node: Node
  enrollment_token: string
  grpc_endpoint: string
}

export interface Settings {
  overcommit_ram: boolean
  ram_overcommit_ratio: number
  overcommit_cpu: boolean
  cpu_overcommit_ratio: number
  storage_accounting: 'provisioned' | 'actual'
  stopped_keeps_reservation: boolean
  default_services: ServiceName[]
  password_length: number
  default_ttl_hours: number
  api_requires_ttl: boolean
  public_vnc_allowed: boolean
}

export interface CreateUserRequest {
  username: string
  password: string
  role: Role
  quota: Quota
}

export interface PatchUserRequest {
  password?: string
  role?: Role
  quota?: Quota
}

export interface ReloadResult {
  count: number
  errors: string[]
}

export const ALL_SERVICES: ServiceName[] = ['ssh', 'rdp', 'vnc', 'adb', 'http', 'https']
export const ALL_EXPOSURES: Exposure[] = ['public', 'tailscale', 'none']
export const ALL_DRIVERS = ['libvirt', 'docker-qemu', 'lxc', 'android', 'orchard', 'usb-phone']

export const emptyQuota = (): Quota => ({
  max_machines: 0,
  max_vcpus: 0,
  max_ram_mb: 0,
  max_storage_gb: 0,
  max_phones: 0,
  allowed_drivers: [],
  allowed_templates: [],
  allowed_license_pools: [],
  allowed_exposures: [],
  max_ttl_hours: 0,
})

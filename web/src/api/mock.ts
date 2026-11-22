// In-memory fake backend for `VITE_MOCK=1` (UI development without the Go server).
import { ApiError } from './client'
import type {
  ApiToken,
  CreateDeviceRequest,
  Device,
  Node,
  ServiceName,
  Settings,
  Template,
  User,
} from './types'
import { emptyQuota } from './types'
import { slug } from '../util'

const BASE = 'servers.example.com'
const now = () => new Date().toISOString()
const uid = () => crypto.randomUUID()
const pw = () =>
  Array.from(crypto.getRandomValues(new Uint8Array(24)), (b) =>
    'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789-_.'.charAt(b % 59),
  ).join('')

const PORTS: Record<ServiceName, number> = { ssh: 22, rdp: 3389, vnc: 5900, adb: 5555, http: 80, https: 443 }

const me: User = {
  id: 'u-admin',
  username: 'finn',
  role: 'admin',
  ssh_keys: ['ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyOnly finn@laptop'],
  quota: { ...emptyQuota(), max_machines: 10, max_ram_mb: 65536 },
  usage: { machines: 0, vcpus: 0, ram_mb: 0, storage_gb: 0, phones: 0 },
  created_at: now(),
}

const users: User[] = [
  me,
  {
    id: 'u-2',
    username: 'ci-bot',
    role: 'user',
    ssh_keys: [],
    quota: { ...emptyQuota(), max_machines: 3, allowed_drivers: ['libvirt'], allowed_exposures: ['none', 'tailscale'], max_ttl_hours: 24 },
    usage: { machines: 0, vcpus: 0, ram_mb: 0, storage_gb: 0, phones: 0 },
    created_at: now(),
  },
]

const res = (vcpus: number, ram_mb: number, storage_gb: number) => ({ vcpus, cpu_cap_percent: 0, ram_mb, storage_gb })

const templates: Template[] = [
  t('debian-13', 'Debian 13 (Trixie)', 'linux', ['libvirt', 'docker-qemu'], ['libvirt'], ['ssh', 'vnc', 'http', 'https'], res(1, 512, 8), res(2, 2048, 20)),
  t('ubuntu-24.04', 'Ubuntu 24.04 LTS', 'linux', ['libvirt', 'lxc'], ['libvirt', 'lxc'], ['ssh', 'vnc', 'http', 'https'], res(1, 1024, 10), res(2, 2048, 20)),
  { ...t('win11-pro', 'Windows 11 Pro', 'windows', ['libvirt', 'docker-qemu'], ['libvirt'], ['ssh', 'rdp', 'vnc', 'http', 'https'], res(2, 4096, 64), res(4, 8192, 80)), license: true },
  t('android-15', 'Android 15', 'android', ['android'], ['android'], ['adb', 'vnc'], res(2, 2048, 8), res(2, 4096, 16)),
  t('macos-tahoe', 'macOS Tahoe', 'macos', ['orchard'], [], ['ssh', 'vnc'], res(4, 8192, 60), res(4, 8192, 80)),
]

function t(
  id: string,
  name: string,
  family: Template['family'],
  compatible: string[],
  available: string[],
  services: ServiceName[],
  min: Template['min_resources'],
  def: Template['default_resources'],
): Template {
  return {
    id,
    name,
    description: `Mock template ${name}`,
    family,
    version: '1',
    icon: family,
    compatible_drivers: compatible,
    available_drivers: available,
    supported_services: services,
    default_services: services.filter((s) => s === 'ssh' || s === 'rdp' || s === 'adb'),
    min_resources: min,
    default_resources: def,
    license: false,
    source: 'mock',
  }
}

const nodes: Node[] = [
  {
    id: 'n-1',
    name: 'homelab',
    address: '100.64.0.12',
    status: 'online',
    agent_version: '0.1.0',
    last_seen: now(),
    enrolled: true,
    capabilities: {
      arch: 'x86_64',
      os: 'linux',
      host_cores: 12,
      host_ram_mb: 65536,
      host_storage_gb: 1800,
      limits: { ram_mb: 16384, cpu_percent: 70, storage_gb: 300 },
      drivers: [
        { name: 'libvirt', families: ['linux', 'windows'], services: ['ssh', 'rdp', 'vnc', 'http', 'https'], backends: [], fixed_slots: 0, healthy: true, message: '', physical_devices: [] },
        { name: 'lxc', families: ['linux'], services: ['ssh', 'http', 'https'], backends: [], fixed_slots: 0, healthy: true, message: '', physical_devices: [] },
        { name: 'android', families: ['android'], services: ['adb', 'vnc'], backends: ['redroid'], fixed_slots: 0, healthy: false, message: 'docker socket not found', physical_devices: [] },
      ],
      license_pools: [{ name: 'win-retail-pro', type: 'retail', total: 3, in_use: 1 }],
    },
    allocated: { vcpus: 0, cpu_percent: 0, ram_mb: 0, storage_gb: 0, devices: 0 },
    usage: { ram_used_mb: 3420, cpu_used_percent: 23.4, storage_used_gb: 41, host_ram_used_mb: 21500, host_storage_used_gb: 722, host_storage_total_gb: 914 },
  },
  {
    id: 'n-2',
    name: 'mac-mini',
    address: '100.64.0.20',
    status: 'offline',
    agent_version: '0.1.0',
    last_seen: new Date(Date.now() - 3600_000).toISOString(),
    enrolled: true,
    capabilities: {
      arch: 'arm64',
      os: 'darwin',
      host_cores: 8,
      host_ram_mb: 16384,
      host_storage_gb: 460,
      limits: { ram_mb: 0, cpu_percent: 0, storage_gb: 0 },
      drivers: [{ name: 'orchard', families: ['macos'], services: ['ssh', 'vnc'], backends: [], fixed_slots: 2, healthy: true, message: '', physical_devices: [] }],
      license_pools: [],
    },
    allocated: { vcpus: 0, cpu_percent: 0, ram_mb: 0, storage_gb: 0, devices: 0 },
    usage: null,
  },
]

let settings: Settings = {
  overcommit_ram: false,
  ram_overcommit_ratio: 1,
  overcommit_cpu: true,
  cpu_overcommit_ratio: 4,
  storage_accounting: 'provisioned',
  stopped_keeps_reservation: true,
  default_services: ['ssh', 'rdp'],
  password_length: 24,
  default_ttl_hours: 0,
  api_requires_ttl: true,
  public_vnc_allowed: true,
}

const tokens: ApiToken[] = [{ id: 'tk-1', name: 'gitlab-ci', created_at: now(), last_used_at: null, expires_at: null }]
const devices: Device[] = []

function makeDevice(req: CreateDeviceRequest): Device {
  const tpl = templates.find((x) => x.id === req.template_id)
  if (!tpl) throw new ApiError(400, { error: 'unknown template', code: 'validation' })
  const hostname = slug(req.name)
  if (!hostname) throw new ApiError(400, { error: 'name is required', code: 'validation' })
  if (devices.some((d) => d.hostname === hostname))
    throw new ApiError(409, { error: `hostname "${hostname}" is already taken`, code: 'conflict' })
  const driver = req.driver || tpl.available_drivers[0]
  if (!driver) throw new ApiError(503, { error: 'no online node can run this template', code: 'no_capacity' })
  const node = nodes.find((n) => n.id === req.node_id) ?? nodes.find((n) => n.status === 'online')!
  const fqdn = `${hostname}.${BASE}`
  let port = 40000 + devices.reduce((a, d) => a + d.services.length, 0)
  const password = pw()
  const services = (req.services ?? tpl.default_services).map((name) => {
    const p = port++
    const user = name === 'http' || name === 'https' || name === 'adb' ? undefined : 'tossbox'
    const connect =
      name === 'ssh'
        ? `ssh -p ${p} tossbox@${fqdn}`
        : name === 'http'
          ? `http://${fqdn}`
          : name === 'https'
            ? `https://${fqdn}`
            : name === 'adb'
              ? `adb connect ${fqdn}:${p}`
              : `${fqdn}:${p}`
    return { name, port: p, internal_port: PORTS[name], username: user, password: user ? password : undefined, connect }
  })
  const ttl = req.ttl_hours ?? 0
  return {
    id: uid(),
    name: req.name,
    hostname,
    fqdn,
    owner: { id: me.id, username: me.username },
    node: { id: node.id, name: node.name },
    driver,
    template: { id: tpl.id, name: tpl.name, family: tpl.family },
    resources: req.resources,
    power: req.power ?? 'running',
    state: 'provisioning',
    ready: false,
    status_message: 'Downloading image (0%)',
    internal_ip: '',
    exposure: req.exposure ?? 'public',
    allowlist: req.allowlist ?? [],
    services,
    license: req.license ?? { mode: 'none' },
    ttl_hours: ttl,
    expires_at: ttl ? new Date(Date.now() + ttl * 3600_000).toISOString() : null,
    created_at: now(),
    updated_at: now(),
    params: req.params ?? {},
  }
}

// Simulated provisioning progress, broadcast to the mock event stream.
type Listener = (event: string, data: unknown) => void
const listeners = new Set<Listener>()
export function mockSubscribe(fn: Listener) {
  listeners.add(fn)
  return () => listeners.delete(fn)
}
const emit = (event: string, data: unknown) => listeners.forEach((l) => l(event, data))

function simulate(d: Device) {
  let pct = 0
  const timer = setInterval(() => {
    if (!devices.includes(d)) return clearInterval(timer)
    pct += 25
    if (pct < 100) {
      d.status_message = `Downloading image (${pct}%)`
    } else if (pct === 100) {
      d.status_message = 'Booting'
    } else {
      d.state = d.power === 'running' ? 'running' : 'stopped'
      d.ready = d.power === 'running'
      d.status_message = ''
      d.internal_ip = '192.168.122.' + (20 + devices.indexOf(d))
      clearInterval(timer)
    }
    d.updated_at = now()
    emit('device', d)
  }, 900)
}

const delay = () => new Promise((r) => setTimeout(r, 150))

function recomputeUsage() {
  const mine = devices.filter((d) => d.owner.id === me.id)
  me.usage = {
    machines: mine.length,
    vcpus: mine.reduce((a, d) => a + d.resources.vcpus, 0),
    ram_mb: mine.reduce((a, d) => a + d.resources.ram_mb, 0),
    storage_gb: mine.reduce((a, d) => a + d.resources.storage_gb, 0),
    phones: 0,
  }
  for (const n of nodes) {
    const on = devices.filter((d) => d.node?.id === n.id)
    n.allocated = {
      vcpus: on.reduce((a, d) => a + d.resources.vcpus, 0),
      cpu_percent: on.reduce((a, d) => a + d.resources.cpu_cap_percent, 0),
      ram_mb: on.reduce((a, d) => a + d.resources.ram_mb, 0),
      storage_gb: on.reduce((a, d) => a + d.resources.storage_gb, 0),
      devices: on.length,
    }
  }
}

export async function mockRequest<T>(method: string, fullPath: string, body?: unknown): Promise<T> {
  await delay()
  const path = fullPath.split('?')[0]
  const b = (body ?? {}) as Record<string, unknown>
  const r = (v: unknown) => structuredClone(v) as T
  const notFound = () => {
    throw new ApiError(404, { error: 'not found', code: 'not_found' })
  }
  let m: RegExpMatchArray | null

  if (path === '/info') return r({ version: '0.1.0-mock', base_domain: BASE, guacamole_enabled: true, setup_required: false })
  if (path === '/auth/login') return r(me)
  if (path === '/auth/logout') return undefined as T
  if (path === '/auth/me') return r(me)
  if (path === '/me/ssh-keys') {
    me.ssh_keys = b.ssh_keys as string[]
    return r(me)
  }
  if (path === '/me/password') return undefined as T
  if (path === '/me/tokens' && method === 'GET') return r(tokens)
  if (path === '/me/tokens' && method === 'POST') {
    const tk = { id: uid(), name: String(b.name), created_at: now(), last_used_at: null, expires_at: null }
    tokens.push(tk)
    return r({ id: tk.id, name: tk.name, token: 'tbt_' + pw() + pw() })
  }
  if ((m = path.match(/^\/me\/tokens\/(.+)$/))) {
    tokens.splice(tokens.findIndex((x) => x.id === m![1]), 1)
    return undefined as T
  }
  if (path === '/templates') return r(templates)
  if (path === '/templates/reload') return r({ count: templates.length, errors: [] })
  if (path === '/create-options') {
    recomputeUsage()
    return r({
      templates,
      nodes: nodes.map((n) => ({
        id: n.id,
        name: n.name,
        status: n.status,
        drivers: n.capabilities?.drivers.map((d) => d.name) ?? [],
        free: {
          vcpus: 0,
          cpu_percent: Math.max(0, (n.capabilities?.limits.cpu_percent || 100) - n.allocated.cpu_percent),
          ram_mb: Math.max(0, (n.capabilities?.limits.ram_mb || n.capabilities?.host_ram_mb || 0) - n.allocated.ram_mb),
          storage_gb: Math.max(0, (n.capabilities?.limits.storage_gb || n.capabilities?.host_storage_gb || 0) - n.allocated.storage_gb),
        },
        fixed_slots_free: n.name === 'mac-mini' ? { orchard: 2 } : {},
        license_pools: n.capabilities?.license_pools.map((p) => ({ name: p.name, type: p.type, free: p.total - p.in_use })) ?? [],
        host_cores: n.capabilities?.host_cores ?? 0,
      })),
      license_pools: [{ name: 'win-retail-pro', type: 'retail', free: 2 }],
      quota: me.quota,
      usage: me.usage,
      allowed_exposures: ['public', 'tailscale', 'none'],
      default_ttl_hours: settings.default_ttl_hours,
      max_ttl_hours: me.quota.max_ttl_hours,
      service_catalog: [
        { name: 'ssh', label: 'SSH', default: true, desktop_only: false },
        { name: 'rdp', label: 'RDP', default: true, desktop_only: true },
        { name: 'vnc', label: 'VNC', default: false, desktop_only: true, warning: 'VNC is unencrypted; prefer Guacamole, an SSH tunnel or Tailscale' },
        { name: 'adb', label: 'ADB', default: true, desktop_only: false },
        { name: 'http', label: 'HTTP (port 80)', default: false, desktop_only: false },
        { name: 'https', label: 'HTTPS (port 443)', default: false, desktop_only: false },
      ],
    })
  }
  if (path === '/devices' && method === 'GET') return r(devices)
  if (path === '/devices' && method === 'POST') {
    const d = makeDevice(body as CreateDeviceRequest)
    devices.push(d)
    recomputeUsage()
    simulate(d)
    return r(d)
  }
  if ((m = path.match(/^\/devices\/([^/]+)\/guacamole$/))) return r({ url: 'https://guacamole.' + BASE + '/#/?data=mock' })
  if ((m = path.match(/^\/devices\/([^/]+)$/))) {
    const d = devices.find((x) => x.id === m![1]) ?? notFound()!
    if (method === 'GET') return r(d)
    if (method === 'PATCH') {
      if (b.name !== undefined) d.name = String(b.name)
      if (b.ttl_hours !== undefined) d.ttl_hours = Number(b.ttl_hours)
      if (b.allowlist !== undefined) d.allowlist = b.allowlist as string[]
      if (b.power) {
        d.power = b.power as Device['power']
        d.state = d.power === 'running' ? 'running' : 'stopped'
        d.ready = d.power === 'running'
      }
      d.updated_at = now()
      emit('device', d)
      return r(d)
    }
    if (method === 'DELETE') {
      d.state = 'destroying'
      emit('device', d)
      setTimeout(() => {
        devices.splice(devices.indexOf(d), 1)
        recomputeUsage()
        emit('device_deleted', { id: d.id })
      }, 1200)
      return undefined as T
    }
  }
  if (path === '/nodes' && method === 'GET') {
    recomputeUsage()
    return r(nodes)
  }
  if (path === '/nodes' && method === 'POST') {
    const n: Node = {
      id: uid(),
      name: String(b.name),
      address: String(b.address),
      status: 'pending',
      agent_version: '',
      last_seen: null,
      enrolled: false,
      capabilities: null,
      allocated: { vcpus: 0, cpu_percent: 0, ram_mb: 0, storage_gb: 0, devices: 0 },
      usage: null,
    }
    nodes.push(n)
    return r({ node: n, enrollment_token: 'tb1.' + 'ab12'.repeat(16) + '.' + pw(), grpc_endpoint: 'dashboard.example.com:7443' })
  }
  if ((m = path.match(/^\/nodes\/([^/]+)\/token$/))) {
    const n = nodes.find((x) => x.id === m![1]) ?? notFound()!
    return r({ node: n, enrollment_token: 'tb1.' + 'cd34'.repeat(16) + '.' + pw(), grpc_endpoint: 'dashboard.example.com:7443' })
  }
  if ((m = path.match(/^\/nodes\/([^/]+)$/))) {
    const i = nodes.findIndex((x) => x.id === m![1])
    if (i < 0) notFound()
    if (method === 'DELETE') {
      if (devices.some((d) => d.node?.id === nodes[i].id))
        throw new ApiError(409, { error: 'node still has devices', code: 'conflict' })
      nodes.splice(i, 1)
      return undefined as T
    }
    if (method === 'PATCH') Object.assign(nodes[i], b)
    return r(nodes[i])
  }
  if (path === '/users' && method === 'GET') return r(users)
  if (path === '/users' && method === 'POST') {
    const u: User = {
      id: uid(),
      username: String(b.username),
      role: b.role as User['role'],
      ssh_keys: [],
      quota: b.quota as User['quota'],
      usage: { machines: 0, vcpus: 0, ram_mb: 0, storage_gb: 0, phones: 0 },
      created_at: now(),
    }
    users.push(u)
    return r(u)
  }
  if ((m = path.match(/^\/users\/([^/]+)$/))) {
    const i = users.findIndex((x) => x.id === m![1])
    if (i < 0) notFound()
    if (method === 'DELETE') {
      users.splice(i, 1)
      return undefined as T
    }
    if (b.role) users[i].role = b.role as User['role']
    if (b.quota) users[i].quota = b.quota as User['quota']
    return r(users[i])
  }
  if (path === '/settings' && method === 'GET') return r(settings)
  if (path === '/settings' && method === 'PUT') {
    settings = { ...settings, ...(b as Partial<Settings>) }
    return r(settings)
  }
  return notFound()!
}

import { useEffect, useMemo, useState } from 'react'
import type { FormEvent, ReactNode } from 'react'
import { Link, useNavigate } from 'react-router'
import { useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, ArrowLeft, Loader2 } from 'lucide-react'
import { api } from '../api/client'
import { qk, upsertDevice, useCreateOptions, useInfo } from '../api/hooks'
import type { CreateOptionNode, CreateOptions, Exposure, Family, LicenseMode, NetworkPolicy, Resources, ServiceName, Template } from '../api/types'
import { ALL_SERVICES } from '../api/types'
import { ErrorBox, Field, Loading, OsIcon, useToast } from '../components/ui'
import { errorMessage, familyLabel, fmtMB, slug } from '../util'

/**
 * Services each driver can expose. The create-options API only reports driver
 * names per node, so the UI uses this table (mirrors the agent's drivers); the
 * backend validates again on create.
 */
const DRIVER_SERVICES: Record<string, ServiceName[]> = {
  libvirt: ['ssh', 'rdp', 'vnc', 'http', 'https'],
  'docker-qemu': ['ssh', 'rdp', 'vnc', 'http', 'https'],
  lxc: ['ssh', 'http', 'https'],
  android: ['adb', 'vnc'],
  orchard: ['ssh', 'vnc', 'http', 'https'],
  'usb-phone': ['adb', 'vnc'],
}
const COUNT_BASED = new Set(['orchard', 'usb-phone'])
const FAMILY_ORDER: Family[] = ['linux', 'windows', 'android', 'chromeos', 'browser', 'macos', 'ios']
const TTL_PRESETS = [0, 1, 4, 8, 24, 72, 168]
const CIDR = /^(\d{1,3}(\.\d{1,3}){3}(\/\d{1,2})?|[0-9a-fA-F:]+(\/\d{1,3})?)$/

const remaining = (max: number, used: number) => (max > 0 ? Math.max(0, max - used) : Infinity)
const ttlLabel = (h: number) => (h === 0 ? 'Never' : h < 24 ? `${h} h` : `${h / 24} d`)

export function CreateDevicePage() {
  const opts = useCreateOptions()
  if (opts.isPending) return <Loading />
  if (opts.isError) return <ErrorBox error={opts.error} retry={() => opts.refetch()} />
  return <CreateForm opts={opts.data} />
}

function CreateForm({ opts }: { opts: CreateOptions }) {
  const info = useInfo()
  const toast = useToast()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const baseDomain = info.data?.base_domain ?? 'servers.example.com'

  const [templateId, setTemplateId] = useState('')
  const [driver, setDriver] = useState('')
  const [nodeId, setNodeId] = useState('')
  const [res, setRes] = useState<Resources>({ vcpus: 1, cpu_cap_percent: 0, ram_mb: 1024, storage_gb: 10 })
  const [services, setServices] = useState<ServiceName[]>([])
  const [exposure, setExposure] = useState<Exposure>(opts.allowed_exposures.includes('public') ? 'public' : opts.allowed_exposures[0] ?? 'none')
  const [allowlist, setAllowlist] = useState('')
  const [networkPolicy, setNetworkPolicy] = useState<NetworkPolicy>('internet')
  const [gpu, setGpu] = useState(false)
  const [ttl, setTtl] = useState<number>(opts.max_ttl_hours ? Math.min(opts.default_ttl_hours || opts.max_ttl_hours, opts.max_ttl_hours) : opts.default_ttl_hours)
  const [licenseMode, setLicenseMode] = useState<LicenseMode>('none')
  const [pool, setPool] = useState('')
  const [userKey, setUserKey] = useState('')
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const tpl = opts.templates.find((t) => t.id === templateId)

  // Nodes that can run the selected template (+ driver).
  const eligibleNodes = useMemo(() => {
    if (!tpl) return []
    const drivers = driver ? [driver] : tpl.available_drivers
    return opts.nodes.filter(
      (n) =>
        n.status === 'online' &&
        n.drivers.some((d) => drivers.includes(d) && (!COUNT_BASED.has(d) || (n.fixed_slots_free[d] ?? 0) > 0)),
    )
  }, [opts.nodes, tpl, driver])
  const node = eligibleNodes.find((n) => n.id === nodeId)

  const effectiveDriver = driver || (tpl?.available_drivers.length === 1 ? tpl.available_drivers[0] : '')
  const countBased = effectiveDriver !== '' && COUNT_BASED.has(effectiveDriver)

  // Services the template AND (selected or any available) driver support.
  const availableServices = useMemo(() => {
    if (!tpl) return [] as ServiceName[]
    const drv = driver ? [driver] : tpl.available_drivers
    const fromDrivers = new Set(
      drv.flatMap((d) => opts.driver_services?.[d] ?? DRIVER_SERVICES[d] ?? ALL_SERVICES),
    )
    return tpl.supported_services.filter((s) => fromDrivers.has(s))
  }, [tpl, driver, opts.driver_services])

  // Capacity limits = min(node free (selected node or best eligible), quota remaining).
  const limits = useMemo(() => {
    const q = opts.quota
    const u = opts.usage
    const nodes = node ? [node] : eligibleNodes
    const best = (f: (n: CreateOptionNode) => number) => (nodes.length ? Math.max(...nodes.map(f)) : 0)
    return {
      vcpus: Math.min(best((n) => n.host_cores || 64), remaining(q.max_vcpus, u.vcpus)),
      cpu: Math.min(best((n) => n.free.cpu_percent), 100),
      ram: Math.min(best((n) => n.free.ram_mb), remaining(q.max_ram_mb, u.ram_mb)),
      storage: Math.min(best((n) => n.free.storage_gb), remaining(q.max_storage_gb, u.storage_gb)),
    }
  }, [opts.quota, opts.usage, node, eligibleNodes])

  const pools = node ? node.license_pools : opts.license_pools

  // Optional features of the candidate drivers (any online node).
  const features = useMemo(() => {
    const drv = tpl ? (driver ? [driver] : tpl.available_drivers) : []
    const f = drv.map((d) => opts.driver_features?.[d])
    return {
      offline: f.some((x) => x?.network_policies.includes('offline')),
      gpu: f.some((x) => x?.gpu),
    }
  }, [tpl, driver, opts.driver_features])

  const selectTemplate = (t: Template) => {
    setTemplateId(t.id)
    setDriver('')
    setNodeId('')
    setRes({ ...t.default_resources })
    setLicenseMode('none')
    setPool('')
    setUserKey('')
    setNetworkPolicy('internet')
    setGpu(t.gpu === 'required' || t.gpu === 'recommended')
  }

  // Reset services whenever the available set changes (template / driver switch).
  useEffect(() => {
    if (!tpl) return
    setServices(tpl.default_services.filter((s) => availableServices.includes(s)))
  }, [tpl, availableServices])

  useEffect(() => {
    if (nodeId && !eligibleNodes.some((n) => n.id === nodeId)) setNodeId('')
  }, [eligibleNodes, nodeId])

  const hostname = slug(name)
  const allowlistEntries = allowlist
    .split(/[\s,]+/)
    .map((s) => s.trim())
    .filter(Boolean)
  const badCidr = allowlistEntries.find((e) => !CIDR.test(e))
  const quotaMachinesLeft = remaining(opts.quota.max_machines, opts.usage.machines)

  const problems: string[] = []
  if (!tpl) problems.push('Choose a template')
  if (tpl && tpl.available_drivers.length === 0) problems.push('No online node can run this template')
  if (tpl && eligibleNodes.length === 0 && tpl.available_drivers.length > 0) problems.push('No node with free capacity for this driver')
  if (!hostname) problems.push('Enter a name')
  if (quotaMachinesLeft < 1) problems.push('Machine quota reached')
  if (tpl && !countBased) {
    if (res.vcpus < tpl.min_resources.vcpus || res.vcpus > limits.vcpus) problems.push('vCPUs out of range')
    if (res.ram_mb < tpl.min_resources.ram_mb || res.ram_mb > limits.ram) problems.push('RAM out of range')
    if (res.storage_gb < tpl.min_resources.storage_gb || res.storage_gb > limits.storage) problems.push('Storage out of range')
  }
  if (badCidr) problems.push(`Invalid allowlist entry "${badCidr}"`)
  if (networkPolicy === 'offline' && !features.offline) problems.push('No node offers the offline network policy for this driver')
  if (gpu && !features.gpu && tpl?.gpu !== 'recommended') problems.push('No node offers a GPU for this driver')
  if (licenseMode === 'pool' && !pool) problems.push('Choose a license pool')
  if (licenseMode === 'user_key' && !userKey.trim()) problems.push('Enter a product key')
  if (opts.max_ttl_hours > 0 && (ttl === 0 || ttl > opts.max_ttl_hours)) problems.push(`TTL must be at most ${opts.max_ttl_hours} h`)

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (problems.length || !tpl) return
    setBusy(true)
    setError('')
    try {
      const d = await api.createDevice({
        name: name.trim(),
        template_id: tpl.id,
        driver: driver || undefined,
        node_id: nodeId || undefined,
        resources: res,
        services,
        exposure,
        allowlist: allowlistEntries,
        network_policy: networkPolicy,
        gpu: gpu && features.gpu,
        ttl_hours: ttl,
        license: tpl.license
          ? { mode: licenseMode, pool: licenseMode === 'pool' ? pool : undefined, user_key: licenseMode === 'user_key' ? userKey.trim() : undefined }
          : undefined,
        power: 'running',
      })
      upsertDevice(qc, d)
      qc.invalidateQueries({ queryKey: qk.me })
      qc.invalidateQueries({ queryKey: qk.createOptions })
      toast(`Creating ${d.name}`)
      navigate('/')
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  const grouped = FAMILY_ORDER.map((f) => [f, opts.templates.filter((t) => t.family === f)] as const).filter(([, l]) => l.length)

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <Link to="/" className="back">
            <ArrowLeft size={16} /> Devices
          </Link>
          <h1>Create device</h1>
        </div>
      </div>

      <form className="create-grid" onSubmit={submit}>
        <div className="create-main">
          <Section n={1} title="Template">
            {opts.templates.length === 0 && <p className="muted">No templates available. Ask an admin to add template sources.</p>}
            {grouped.map(([family, list]) => (
              <div key={family} className="tpl-group">
                <h3 className="group-title">{familyLabel[family]}</h3>
                <div className="tpl-grid" role="radiogroup" aria-label={`${familyLabel[family]} templates`}>
                  {list.map((t) => {
                    const unavailable = t.available_drivers.length === 0
                    return (
                      <button
                        type="button"
                        key={t.id}
                        role="radio"
                        aria-checked={t.id === templateId}
                        className={`tpl ${t.id === templateId ? 'selected' : ''} ${unavailable ? 'unavailable' : ''}`}
                        onClick={() => selectTemplate(t)}
                        title={unavailable ? 'No online node can run this template' : t.description}
                      >
                        <OsIcon family={t.family} size={28} />
                        <span className="tpl-text">
                          <strong>{t.name}</strong>
                          <small className="muted">{unavailable ? 'No node available' : t.available_drivers.join(', ')}</small>
                        </span>
                      </button>
                    )
                  })}
                </div>
              </div>
            ))}
          </Section>

          {tpl && (
            <>
              <Section n={2} title="Placement">
                <div className="grid-2">
                  <Field label="Driver" hint="Auto picks a compatible driver with free capacity.">
                    <select value={driver} onChange={(e) => setDriver(e.target.value)}>
                      <option value="">Auto</option>
                      {tpl.available_drivers.map((d) => (
                        <option key={d} value={d}>
                          {d}
                        </option>
                      ))}
                    </select>
                  </Field>
                  <Field label="Node" hint={eligibleNodes.length === 0 ? 'No eligible node online.' : 'Auto = least loaded node.'}>
                    <select value={nodeId} onChange={(e) => setNodeId(e.target.value)}>
                      <option value="">Auto</option>
                      {eligibleNodes.map((n) => (
                        <option key={n.id} value={n.id}>
                          {n.name} — {fmtMB(n.free.ram_mb)} RAM, {n.free.storage_gb} GB free
                        </option>
                      ))}
                    </select>
                  </Field>
                </div>
              </Section>

              {!countBased && (
                <Section n={3} title="Resources">
                  <Range
                    label="vCPUs"
                    value={res.vcpus}
                    min={tpl.min_resources.vcpus}
                    max={limits.vcpus}
                    onChange={(v) => setRes({ ...res, vcpus: v })}
                    fmt={(v) => `${v}`}
                  />
                  <Range
                    label="CPU cap (% of node)"
                    value={res.cpu_cap_percent}
                    min={0}
                    max={limits.cpu}
                    step={5}
                    onChange={(v) => setRes({ ...res, cpu_cap_percent: v })}
                    fmt={(v) => (v === 0 ? 'no cap' : `${v} %`)}
                    hint="Enforced share of the node's CPU. 0 = no cap."
                  />
                  <Range
                    label="RAM"
                    value={res.ram_mb}
                    min={tpl.min_resources.ram_mb}
                    max={limits.ram}
                    step={256}
                    onChange={(v) => setRes({ ...res, ram_mb: v })}
                    fmt={fmtMB}
                  />
                  <Range
                    label="Storage"
                    value={res.storage_gb}
                    min={tpl.min_resources.storage_gb}
                    max={limits.storage}
                    onChange={(v) => setRes({ ...res, storage_gb: v })}
                    fmt={(v) => `${v} GB`}
                  />
                </Section>
              )}

              <Section n={countBased ? 3 : 4} title="Services">
                {availableServices.length === 0 ? (
                  <p className="muted">This template exposes no services on the selected driver.</p>
                ) : (
                  <div className="checks">
                    {opts.service_catalog
                      .filter((s) => availableServices.includes(s.name))
                      .map((s) => (
                        <label key={s.name} className="check-card">
                          <input
                            type="checkbox"
                            checked={services.includes(s.name)}
                            onChange={(e) => setServices(e.target.checked ? [...services, s.name] : services.filter((x) => x !== s.name))}
                          />
                          <span>{s.label}</span>
                        </label>
                      ))}
                  </div>
                )}
                {services.includes('vnc') && (
                  <div className="alert alert-warn">
                    <AlertTriangle size={16} />
                    {opts.service_catalog.find((s) => s.name === 'vnc')?.warning ?? 'VNC is unencrypted.'}
                  </div>
                )}
                {(services.includes('http') || services.includes('https')) && (
                  <p className="muted small">
                    Web traffic to <code>{hostname || 'name'}.{baseDomain}</code> on ports 80/443 is forwarded to this device.
                  </p>
                )}

                <div className="stack mt">
                  <Field label="Exposure">
                    <div className="seg" role="radiogroup" aria-label="Exposure">
                      {opts.allowed_exposures.map((x) => (
                        <button type="button" key={x} role="radio" aria-checked={exposure === x} className={exposure === x ? 'on' : ''} onClick={() => setExposure(x)}>
                          {x === 'none' ? 'internal' : x}
                        </button>
                      ))}
                    </div>
                  </Field>
                  {exposure !== 'none' && (
                    <Field label="IP allowlist (optional)" hint="CIDRs, separated by commas or new lines. Empty = everyone." error={badCidr ? `Invalid: ${badCidr}` : undefined}>
                      <textarea rows={2} value={allowlist} onChange={(e) => setAllowlist(e.target.value)} placeholder="203.0.113.0/24" />
                    </Field>
                  )}
                  <Field
                    label="Outbound network"
                    hint={
                      networkPolicy === 'offline'
                        ? 'No outbound traffic at all (no internet, no DNS). You can still connect to the device through its services.'
                        : 'Internet access; private networks (LAN, Tailscale, the node) are blocked.'
                    }
                  >
                    <div className="seg" role="radiogroup" aria-label="Outbound network">
                      <button type="button" role="radio" aria-checked={networkPolicy === 'internet'} className={networkPolicy === 'internet' ? 'on' : ''} onClick={() => setNetworkPolicy('internet')}>
                        internet
                      </button>
                      <button
                        type="button"
                        role="radio"
                        aria-checked={networkPolicy === 'offline'}
                        className={networkPolicy === 'offline' ? 'on' : ''}
                        disabled={!features.offline}
                        title={features.offline ? undefined : 'Not supported by the driver on any online node'}
                        onClick={() => setNetworkPolicy('offline')}
                      >
                        offline
                      </button>
                    </div>
                  </Field>
                  {(features.gpu || tpl?.gpu) && (
                    <label className="check">
                      <input type="checkbox" checked={gpu} disabled={!features.gpu || tpl?.gpu === 'required'} onChange={(e) => setGpu(e.target.checked)} />{' '}
                      Share the node's GPU{tpl?.gpu === 'required' ? ' (required by this template)' : tpl?.gpu === 'recommended' ? ' (recommended for this template)' : ''}
                      {!features.gpu && <span className="muted small"> — no node offers a GPU right now</span>}
                    </label>
                  )}
                </div>
              </Section>

              <Section n={countBased ? 4 : 5} title="Lifetime">
                <Field label="Auto-delete after" hint={opts.max_ttl_hours ? `Maximum ${ttlLabel(opts.max_ttl_hours)} for your account.` : undefined}>
                  <div className="seg wrap" role="radiogroup" aria-label="Auto-delete after">
                    {TTL_PRESETS.filter((h) => (opts.max_ttl_hours ? h > 0 && h <= opts.max_ttl_hours : true)).map((h) => (
                      <button type="button" key={h} role="radio" aria-checked={ttl === h} className={ttl === h ? 'on' : ''} onClick={() => setTtl(h)}>
                        {ttlLabel(h)}
                      </button>
                    ))}
                    <input
                      type="number"
                      min={0}
                      className="ttl-input"
                      aria-label="Custom hours"
                      value={ttl}
                      onChange={(e) => setTtl(Math.max(0, Number(e.target.value) || 0))}
                    />
                    <span className="muted small">hours</span>
                  </div>
                </Field>
              </Section>

              {tpl.license && (
                <Section n={countBased ? 5 : 6} title="Windows license">
                  <div className="radios">
                    <label className="check">
                      <input type="radio" name="lic" checked={licenseMode === 'none'} onChange={() => setLicenseMode('none')} /> No key (unactivated, fine for throwaway use)
                    </label>
                    <label className="check">
                      <input type="radio" name="lic" checked={licenseMode === 'user_key'} onChange={() => setLicenseMode('user_key')} /> My own product key
                    </label>
                    <label className={`check ${pools.length === 0 ? 'disabled' : ''}`}>
                      <input type="radio" name="lic" disabled={pools.length === 0} checked={licenseMode === 'pool'} onChange={() => setLicenseMode('pool')} /> Key from a
                      license pool {pools.length === 0 && <span className="muted">(none available to you)</span>}
                    </label>
                  </div>
                  {licenseMode === 'user_key' && (
                    <Field label="Product key" hint="Stored encrypted and deleted with the device.">
                      <input value={userKey} onChange={(e) => setUserKey(e.target.value)} placeholder="XXXXX-XXXXX-XXXXX-XXXXX-XXXXX" autoComplete="off" spellCheck={false} />
                    </Field>
                  )}
                  {licenseMode === 'pool' && (
                    <Field label="Pool">
                      <select value={pool} onChange={(e) => setPool(e.target.value)}>
                        <option value="">Choose…</option>
                        {pools.map((p) => (
                          <option key={p.name} value={p.name} disabled={p.free === 0}>
                            {p.name} ({p.type}, {p.free} free)
                          </option>
                        ))}
                      </select>
                    </Field>
                  )}
                </Section>
              )}

              <Section n={(countBased ? 5 : 6) + (tpl.license ? 1 : 0)} title="Name">
                <Field
                  label="Device name"
                  hint={
                    hostname ? (
                      <>
                        Hostname: <code>{hostname}.{baseDomain}</code> (fixed after creation)
                      </>
                    ) : (
                      'Letters, numbers and dashes become the hostname.'
                    )
                  }
                >
                  <input value={name} onChange={(e) => setName(e.target.value)} placeholder="windowsGitlabBuildMachine1" maxLength={100} required />
                </Field>
              </Section>
            </>
          )}
        </div>

        <aside className="create-side">
          <div className="card summary">
            <h2>Summary</h2>
            {tpl ? (
              <dl className="dl">
                <dt>Template</dt>
                <dd>{tpl.name}</dd>
                <dt>Driver / node</dt>
                <dd>
                  {driver || 'auto'} / {node?.name ?? 'auto'}
                </dd>
                {!countBased && (
                  <>
                    <dt>Resources</dt>
                    <dd>
                      {res.vcpus} vCPU{res.cpu_cap_percent ? `, cap ${res.cpu_cap_percent}%` : ''}, {fmtMB(res.ram_mb)}, {res.storage_gb} GB
                    </dd>
                  </>
                )}
                <dt>Services</dt>
                <dd>{services.length ? services.join(', ') : 'none'}</dd>
                <dt>Exposure</dt>
                <dd>{exposure === 'none' ? 'internal' : exposure}</dd>
                <dt>Network</dt>
                <dd>
                  {networkPolicy}
                  {gpu && features.gpu ? ' · GPU' : ''}
                </dd>
                <dt>Auto-delete</dt>
                <dd>{ttlLabel(ttl)}</dd>
                <dt>Hostname</dt>
                <dd className="truncate">{hostname ? `${hostname}.${baseDomain}` : '—'}</dd>
              </dl>
            ) : (
              <p className="muted">Pick a template to start.</p>
            )}
            {error && (
              <div className="alert alert-error" role="alert">
                {error}
              </div>
            )}
            {tpl && problems.length > 0 && (
              <ul className="problems">
                {problems.map((p) => (
                  <li key={p}>{p}</li>
                ))}
              </ul>
            )}
            <button className="btn btn-primary btn-block" disabled={busy || problems.length > 0}>
              {busy && <Loader2 size={16} className="spin" />} Create device
            </button>
          </div>
        </aside>
      </form>
    </div>
  )
}

function Section({ n, title, children }: { n: number; title: string; children: ReactNode }) {
  return (
    <section className="card section">
      <h2 className="section-title">
        <span className="step">{n}</span> {title}
      </h2>
      {children}
    </section>
  )
}

function Range({
  label,
  value,
  min,
  max,
  step = 1,
  onChange,
  fmt,
  hint,
}: {
  label: string
  value: number
  min: number
  max: number
  step?: number
  onChange: (v: number) => void
  fmt: (v: number) => string
  hint?: string
}) {
  const hardMax = Number.isFinite(max) ? max : Math.max(min * 8, value * 2, 64)
  const insufficient = hardMax < min
  return (
    <div className="range">
      <div className="range-head">
        <span className="field-label">{label}</span>
        <span className={insufficient || value > hardMax ? 'text-error' : ''}>{fmt(value)}</span>
      </div>
      <div className="range-row">
        <input
          type="range"
          aria-label={label}
          min={min}
          max={Math.max(min, hardMax)}
          step={step}
          value={value}
          disabled={insufficient}
          onChange={(e) => onChange(Number(e.target.value))}
        />
        <input type="number" aria-label={`${label} value`} min={min} max={hardMax} step={step} value={value} onChange={(e) => onChange(Number(e.target.value) || 0)} />
      </div>
      <span className="field-hint">
        {insufficient ? `Not enough capacity (minimum ${fmt(min)}, available ${fmt(Math.max(0, hardMax))})` : `${fmt(min)} – ${Number.isFinite(max) ? fmt(max) : '∞'}`}
        {hint ? ` · ${hint}` : ''}
      </span>
    </div>
  )
}

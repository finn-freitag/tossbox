import { useMemo, useState } from 'react'
import { Link } from 'react-router'
import { MonitorSmartphone, Plus, Search } from 'lucide-react'
import { useDevices, useMe } from '../api/hooks'
import type { Device } from '../api/types'
import { DeviceActions, Expiry, PowerSwitch, ResourceLine, ServiceRows } from '../components/DeviceParts'
import { Empty, ErrorBox, Loading, OsIcon, StateBadge } from '../components/ui'
import { Quotas } from '../components/Quotas'

export function DevicesPage() {
  const devices = useDevices()
  const me = useMe()
  const [q, setQ] = useState('')
  const [onlyMine, setOnlyMine] = useState(false)
  const admin = me.data?.role === 'admin'

  const list = useMemo(() => {
    let l = devices.data ?? []
    if (onlyMine && me.data) l = l.filter((d) => d.owner.id === me.data!.id)
    const s = q.trim().toLowerCase()
    if (s)
      l = l.filter((d) =>
        [d.name, d.hostname, d.template.name, d.node?.name ?? '', d.owner.username, d.driver].some((x) => x.toLowerCase().includes(s)),
      )
    return [...l].sort((a, b) => b.created_at.localeCompare(a.created_at))
  }, [devices.data, q, onlyMine, me.data])

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1>Devices</h1>
          {me.data && <Quotas quota={me.data.quota} usage={me.data.usage} />}
        </div>
        <Link to="/devices/new" className="btn btn-primary">
          <Plus size={16} /> Create device
        </Link>
      </div>

      {(devices.data?.length ?? 0) > 0 && (
        <div className="toolbar">
          <label className="search">
            <Search size={16} aria-hidden="true" />
            <input placeholder="Search devices" aria-label="Search devices" value={q} onChange={(e) => setQ(e.target.value)} />
          </label>
          {admin && (
            <label className="check">
              <input type="checkbox" checked={onlyMine} onChange={(e) => setOnlyMine(e.target.checked)} /> Only my devices
            </label>
          )}
        </div>
      )}

      {devices.isPending ? (
        <Loading />
      ) : devices.isError ? (
        <ErrorBox error={devices.error} retry={() => devices.refetch()} />
      ) : list.length === 0 ? (
        devices.data.length === 0 ? (
          <Empty icon={<MonitorSmartphone size={40} />} title="No devices yet">
            <p className="muted">Create a throwaway Linux, Windows, Android or macOS device in a few clicks.</p>
            <Link to="/devices/new" className="btn btn-primary">
              <Plus size={16} /> Create device
            </Link>
          </Empty>
        ) : (
          <Empty title="No matching devices" />
        )
      ) : (
        <div className="device-list">
          {list.map((d) => (
            <DeviceCard key={d.id} d={d} showOwner={admin && d.owner.id !== me.data?.id} />
          ))}
        </div>
      )}
    </div>
  )
}

function DeviceCard({ d, showOwner }: { d: Device; showOwner: boolean }) {
  return (
    <article className={`card device ${d.state === 'destroying' ? 'is-dim' : ''}`}>
      <header className="device-head">
        <OsIcon family={d.template.family} />
        <div className="device-title">
          <Link to={`/devices/${d.id}`} className="device-name">
            {d.name}
          </Link>
          <span className="muted small truncate">
            {d.fqdn}
          </span>
        </div>
        <PowerSwitch device={d} />
      </header>
      <div className="device-meta">
        <StateBadge state={d.state} ready={d.ready} />
        {d.status_message && <span className="muted small truncate">{d.status_message}</span>}
      </div>
      <div className="device-sub muted small">
        {d.template.name} · {d.driver} on {d.node?.name ?? 'unscheduled'}
        {showOwner && <> · owner {d.owner.username}</>}
      </div>
      <ServiceRows device={d} />
      <footer className="device-foot">
        <div className="col gap-xs">
          <ResourceLine device={d} />
          <Expiry device={d} />
        </div>
        <DeviceActions device={d} compact />
      </footer>
    </article>
  )
}

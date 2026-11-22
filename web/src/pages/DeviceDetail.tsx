import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router'
import { ArrowLeft, Pencil } from 'lucide-react'
import { useDevice, usePatchDevice } from '../api/hooks'
import type { Device } from '../api/types'
import { DeviceActions, Expiry, PowerSwitch, ServiceRows } from '../components/DeviceParts'
import { ErrorBox, Field, Loading, Modal, OsIcon, StateBadge, useToast } from '../components/ui'
import { errorMessage, fmtDate, fmtMB } from '../util'

export function DeviceDetailPage() {
  const { id = '' } = useParams()
  const q = useDevice(id)
  const navigate = useNavigate()
  if (q.isPending) return <Loading />
  if (q.isError) return <ErrorBox error={q.error} retry={() => q.refetch()} />
  const d = q.data
  return (
    <div className="page">
      <div className="page-head">
        <div>
          <Link to="/" className="back">
            <ArrowLeft size={16} /> Devices
          </Link>
          <div className="row gap-sm">
            <OsIcon family={d.template.family} size={36} />
            <div>
              <h1>{d.name}</h1>
              <span className="muted small">{d.fqdn}</span>
            </div>
          </div>
        </div>
        <div className="row gap-sm">
          <StateBadge state={d.state} ready={d.ready} />
          <PowerSwitch device={d} />
        </div>
      </div>
      {d.status_message && <p className="muted">{d.status_message}</p>}

      <div className="detail-grid">
        <section className="card section">
          <h2 className="section-title">Connect</h2>
          <ServiceRows device={d} />
          <DeviceActions device={d} onDeleted={() => navigate('/')} />
        </section>

        <section className="card section">
          <h2 className="section-title">Details</h2>
          <dl className="dl">
            <dt>Template</dt>
            <dd>{d.template.name}</dd>
            <dt>Driver</dt>
            <dd>{d.driver}</dd>
            <dt>Node</dt>
            <dd>{d.node?.name ?? 'unscheduled'}</dd>
            <dt>Owner</dt>
            <dd>{d.owner.username}</dd>
            <dt>Resources</dt>
            <dd>
              {d.resources.vcpus} vCPU{d.resources.cpu_cap_percent ? ` (cap ${d.resources.cpu_cap_percent}% of node)` : ''}, {fmtMB(d.resources.ram_mb)} RAM,{' '}
              {d.resources.storage_gb} GB disk
            </dd>
            <dt>Internal IP</dt>
            <dd>{d.internal_ip || '—'}</dd>
            <dt>Exposure</dt>
            <dd>
              {d.exposure === 'none' ? 'internal only' : d.exposure}
              {d.allowlist.length > 0 && <> · allow {d.allowlist.join(', ')}</>}
            </dd>
            <dt>License</dt>
            <dd>{d.license.mode === 'pool' ? `pool ${d.license.pool}` : d.license.mode === 'user_key' ? 'own key' : 'none'}</dd>
            <dt>Lifetime</dt>
            <dd>
              <Expiry device={d} />
            </dd>
            <dt>Created</dt>
            <dd>{fmtDate(d.created_at)}</dd>
          </dl>
          <EditButton device={d} />
        </section>
      </div>
    </div>
  )
}

function EditButton({ device }: { device: Device }) {
  const [open, setOpen] = useState(false)
  const [name, setName] = useState(device.name)
  const [ttl, setTtl] = useState(device.ttl_hours)
  const [allow, setAllow] = useState(device.allowlist.join('\n'))
  const patch = usePatchDevice()
  const toast = useToast()
  const save = () =>
    patch.mutate(
      {
        id: device.id,
        req: {
          name: name.trim(),
          ttl_hours: ttl,
          allowlist: allow
            .split(/[\s,]+/)
            .map((s) => s.trim())
            .filter(Boolean),
        },
      },
      {
        onSuccess: () => {
          toast('Saved')
          setOpen(false)
        },
        onError: (e) => toast(errorMessage(e), 'error'),
      },
    )
  return (
    <>
      <button
        className="btn btn-sm"
        onClick={() => {
          setName(device.name)
          setTtl(device.ttl_hours)
          setAllow(device.allowlist.join('\n'))
          setOpen(true)
        }}
      >
        <Pencil size={14} /> Edit
      </button>
      {open && (
        <Modal
          title="Edit device"
          onClose={() => setOpen(false)}
          footer={
            <>
              <button className="btn" onClick={() => setOpen(false)}>
                Cancel
              </button>
              <button className="btn btn-primary" disabled={patch.isPending || !name.trim()} onClick={save}>
                Save
              </button>
            </>
          }
        >
          <Field label="Display name" hint={`The hostname stays ${device.hostname}.`}>
            <input value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <Field label="Auto-delete after (hours from creation, 0 = never)">
            <input type="number" min={0} value={ttl} onChange={(e) => setTtl(Math.max(0, Number(e.target.value) || 0))} />
          </Field>
          {device.exposure !== 'none' && (
            <Field label="IP allowlist" hint="CIDRs, one per line. Empty = everyone.">
              <textarea rows={3} value={allow} onChange={(e) => setAllow(e.target.value)} />
            </Field>
          )}
        </Modal>
      )}
    </>
  )
}

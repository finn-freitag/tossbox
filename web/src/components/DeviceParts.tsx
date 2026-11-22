import { useState } from 'react'
import { Download, ExternalLink, Loader2, Trash2 } from 'lucide-react'
import { api, downloadUrl } from '../api/client'
import { useDeleteDevice, useInfo, usePatchDevice } from '../api/hooks'
import type { Device, DeviceService } from '../api/types'
import { errorMessage, fmtMB, fmtRelative, serviceLabel } from '../util'
import { Confirm, CopyButton, Secret, Switch, useToast } from './ui'

export function PowerSwitch({ device }: { device: Device }) {
  const patch = usePatchDevice()
  const toast = useToast()
  const busy = patch.isPending
  const disabled = busy || device.state === 'destroying' || device.state === 'destroyed'
  return (
    <span className="row gap-xs">
      {busy && <Loader2 size={14} className="spin" />}
      <Switch
        checked={device.power === 'running'}
        disabled={disabled}
        label={device.power === 'running' ? `Turn ${device.name} off` : `Turn ${device.name} on`}
        onChange={(on) =>
          patch.mutate(
            { id: device.id, req: { power: on ? 'running' : 'stopped' } },
            { onError: (e) => toast(errorMessage(e), 'error') },
          )
        }
      />
    </span>
  )
}

export function ServiceRows({ device }: { device: Device }) {
  if (device.services.length === 0) return <p className="muted small">No services exposed.</p>
  return (
    <div className="services">
      {device.services.map((s) => (
        <ServiceRow key={s.name} s={s} exposure={device.exposure} />
      ))}
    </div>
  )
}

function ServiceRow({ s, exposure }: { s: DeviceService; exposure: Device['exposure'] }) {
  return (
    <div className="service">
      <span className="service-name">
        {serviceLabel[s.name] ?? s.name}
        {exposure !== 'public' && <small className="muted"> ({exposure === 'none' ? 'internal' : 'tailscale'})</small>}
      </span>
      <span className="service-connect">
        <code title={s.connect}>{s.connect}</code>
        <CopyButton value={s.connect} label={`Copy ${s.name} connection`} />
      </span>
      {s.username && (
        <span className="service-cred">
          <code>{s.username}</code>
          <CopyButton value={s.username} label="Copy username" />
        </span>
      )}
      {s.password && (
        <span className="service-cred">
          <Secret value={s.password} />
        </span>
      )}
    </div>
  )
}

export function DeviceActions({ device, onDeleted, compact }: { device: Device; onDeleted?: () => void; compact?: boolean }) {
  const info = useInfo()
  const del = useDeleteDevice()
  const toast = useToast()
  const [confirm, setConfirm] = useState(false)
  const [guacBusy, setGuacBusy] = useState(false)
  const has = (n: string) => device.services.some((s) => s.name === n)
  const guacService = has('rdp') ? 'rdp' : has('vnc') ? 'vnc' : has('ssh') ? 'ssh' : null

  const openGuac = async () => {
    // Open the window synchronously (popup blockers), then point it at the URL.
    const w = window.open('', '_blank')
    setGuacBusy(true)
    try {
      const { url } = await api.guacamole(device.id, guacService ?? undefined)
      if (w) w.location.href = url
      else window.location.href = url
    } catch (e) {
      w?.close()
      toast(errorMessage(e), 'error')
    } finally {
      setGuacBusy(false)
    }
  }

  return (
    <div className="actions">
      {info.data?.guacamole_enabled && guacService && (
        <button className="btn btn-sm" onClick={openGuac} disabled={guacBusy || device.state !== 'running'} title="Open in Guacamole">
          {guacBusy ? <Loader2 size={14} className="spin" /> : <ExternalLink size={14} />}
          {!compact && ' Guacamole'}
        </button>
      )}
      {has('rdp') && (
        <a className="btn btn-sm" href={downloadUrl(`/devices/${device.id}/rdp`)} download title="Download .rdp file">
          <Download size={14} /> .rdp
        </a>
      )}
      {has('ssh') && (
        <a className="btn btn-sm" href={downloadUrl(`/devices/${device.id}/ssh-config`)} download title="Download SSH config snippet">
          <Download size={14} /> ssh config
        </a>
      )}
      <button
        className="btn btn-sm btn-danger-ghost"
        onClick={() => setConfirm(true)}
        disabled={device.state === 'destroying'}
        aria-label={`Delete ${device.name}`}
        title="Delete device"
      >
        <Trash2 size={14} />
        {!compact && ' Delete'}
      </button>
      {confirm && (
        <Confirm
          title="Delete device?"
          danger
          confirmLabel="Delete"
          busy={del.isPending}
          text={
            <>
              <strong>{device.name}</strong> and all its data will be destroyed on node {device.node?.name ?? '—'}. This can't be undone.
            </>
          }
          onClose={() => setConfirm(false)}
          onConfirm={() =>
            del.mutate(device.id, {
              onSuccess: () => {
                toast(`Deleting ${device.name}`)
                setConfirm(false)
                onDeleted?.()
              },
              onError: (e) => toast(errorMessage(e), 'error'),
            })
          }
        />
      )}
    </div>
  )
}

export function ResourceLine({ device }: { device: Device }) {
  const r = device.resources
  return (
    <span className="muted small">
      {r.vcpus} vCPU{r.cpu_cap_percent ? ` (cap ${r.cpu_cap_percent}%)` : ''} · {fmtMB(r.ram_mb)} RAM · {r.storage_gb} GB disk
    </span>
  )
}

export function Expiry({ device }: { device: Device }) {
  if (!device.expires_at) return <span className="muted small">No expiry</span>
  const soon = new Date(device.expires_at).getTime() - Date.now() < 3_600_000
  return (
    <span className={`small ${soon ? 'text-warn' : 'muted'}`} title={new Date(device.expires_at).toLocaleString()}>
      Expires {fmtRelative(device.expires_at)}
    </span>
  )
}

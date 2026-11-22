import { useState } from 'react'
import type { FormEvent } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { KeyRound, Pencil, Plus, Server, Trash2 } from 'lucide-react'
import { api } from '../../api/client'
import { qk, useNodes } from '../../api/hooks'
import type { Node, NodeEnrollment } from '../../api/types'
import { Badge, Confirm, CopyButton, Empty, ErrorBox, Field, Loading, Meter, Modal, useToast } from '../../components/ui'
import { errorMessage, fmtGB, fmtMB, fmtRelative } from '../../util'

export function NodesPage() {
  const nodes = useNodes()
  const [adding, setAdding] = useState(false)
  const [enrollment, setEnrollment] = useState<NodeEnrollment | null>(null)

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1>Nodes</h1>
          <p className="muted">Hosts running tb-agent. Each node can enable several drivers.</p>
        </div>
        <button className="btn btn-primary" onClick={() => setAdding(true)}>
          <Plus size={16} /> Add node
        </button>
      </div>
      {nodes.isPending ? (
        <Loading />
      ) : nodes.isError ? (
        <ErrorBox error={nodes.error} retry={() => nodes.refetch()} />
      ) : nodes.data.length === 0 ? (
        <Empty icon={<Server size={40} />} title="No nodes yet">
          <p className="muted">Add a node, then start tb-agent on that host with the enrollment token.</p>
        </Empty>
      ) : (
        <div className="node-list">
          {nodes.data.map((n) => (
            <NodeCard key={n.id} n={n} onToken={setEnrollment} />
          ))}
        </div>
      )}
      {adding && <AddNode onClose={() => setAdding(false)} onCreated={(e) => (setAdding(false), setEnrollment(e))} />}
      {enrollment && <EnrollmentModal e={enrollment} onClose={() => setEnrollment(null)} />}
    </div>
  )
}

function statusTone(s: Node['status']) {
  return s === 'online' ? 'ok' : s === 'offline' ? 'error' : 'warn'
}

function NodeCard({ n, onToken }: { n: Node; onToken: (e: NodeEnrollment) => void }) {
  const qc = useQueryClient()
  const toast = useToast()
  const [confirmDel, setConfirmDel] = useState(false)
  const [confirmToken, setConfirmToken] = useState(false)
  const [editing, setEditing] = useState(false)
  const cap = n.capabilities
  const lim = cap?.limits
  const ramTotal = lim?.ram_mb || cap?.host_ram_mb || 0
  const storageTotal = lim?.storage_gb || cap?.host_storage_gb || 0
  const cpuTotal = lim?.cpu_percent || 100

  return (
    <article className="card node">
      <header className="row between wrap gap-sm">
        <div>
          <h2 className="node-name">
            {n.name} <Badge tone={statusTone(n.status)}>{n.status}</Badge>
          </h2>
          <span className="muted small">
            {n.address}
            {cap && ` · ${cap.os}/${cap.arch} · ${cap.host_cores} cores · ${fmtMB(cap.host_ram_mb)}`}
            {n.agent_version && ` · agent ${n.agent_version}`}
            {n.status !== 'pending' && ` · seen ${fmtRelative(n.last_seen)}`}
          </span>
        </div>
        <div className="row gap-xs">
          <button className="btn btn-sm" onClick={() => setEditing(true)}>
            <Pencil size={14} /> Edit
          </button>
          <button className="btn btn-sm" onClick={() => setConfirmToken(true)}>
            <KeyRound size={14} /> New token
          </button>
          <button className="btn btn-sm btn-danger-ghost" aria-label={`Delete node ${n.name}`} onClick={() => setConfirmDel(true)}>
            <Trash2 size={14} />
          </button>
        </div>
      </header>

      {cap ? (
        <>
          <div className="meters">
            <Meter label="RAM allocated" used={n.allocated.ram_mb} total={ramTotal} fmt={fmtMB} />
            <Meter label="CPU allocated (%)" used={n.allocated.cpu_percent} total={cpuTotal} fmt={(v) => `${v}%`} />
            <Meter label="Storage allocated" used={n.allocated.storage_gb} total={storageTotal} fmt={fmtGB} />
          </div>
          <p className="muted small">
            {n.allocated.devices} device{n.allocated.devices === 1 ? '' : 's'} · {n.allocated.vcpus} vCPU
            {n.usage && ` · actual: ${fmtMB(n.usage.ram_used_mb)} RAM, ${n.usage.cpu_used_percent.toFixed(1)}% CPU, ${n.usage.storage_used_gb} GB disk`}
            {lim && ` · limits: ${lim.ram_mb ? fmtMB(lim.ram_mb) : 'all'} RAM, ${lim.cpu_percent || 100}% CPU, ${lim.storage_gb ? fmtGB(lim.storage_gb) : 'all'} disk`}
          </p>
          <div className="drivers">
            {cap.drivers.map((d) => (
              <div key={d.name} className={`driver ${d.healthy ? '' : 'unhealthy'}`} title={d.message || undefined}>
                <strong>{d.name}</strong>
                <Badge tone={d.healthy ? 'ok' : 'error'}>{d.healthy ? 'healthy' : 'error'}</Badge>
                <span className="muted small">
                  {d.families.join(', ')}
                  {d.backends.length > 0 && ` · ${d.backends.join(', ')}`}
                  {d.fixed_slots > 0 && ` · ${d.fixed_slots} slots`}
                </span>
                {d.message && <span className="small text-error">{d.message}</span>}
                {d.physical_devices.length > 0 && (
                  <span className="small muted">
                    {d.physical_devices.map((p) => `${p.model} (${p.available ? 'free' : 'in use'})`).join(', ')}
                  </span>
                )}
              </div>
            ))}
          </div>
          {cap.license_pools.length > 0 && (
            <p className="small">
              License pools:{' '}
              {cap.license_pools.map((p) => (
                <Badge key={p.name}>
                  {p.name} · {p.type} · {p.in_use}/{p.total} used
                </Badge>
              ))}
            </p>
          )}
        </>
      ) : (
        <p className="muted small">Waiting for the agent to enroll and connect.</p>
      )}

      {confirmDel && (
        <Confirm
          title="Delete node?"
          danger
          confirmLabel="Delete"
          text={`${n.name} will be removed. Nodes that still host devices can't be deleted.`}
          onClose={() => setConfirmDel(false)}
          onConfirm={async () => {
            try {
              await api.deleteNode(n.id)
              qc.invalidateQueries({ queryKey: qk.nodes })
              toast('Node deleted')
            } catch (e) {
              toast(errorMessage(e), 'error')
            }
            setConfirmDel(false)
          }}
        />
      )}
      {confirmToken && (
        <Confirm
          title="Issue new enrollment token?"
          confirmLabel="Issue token"
          text={`The current certificate of ${n.name} will be revoked. The agent must enroll again with the new token.`}
          onClose={() => setConfirmToken(false)}
          onConfirm={async () => {
            try {
              const e = await api.regenerateNodeToken(n.id)
              qc.invalidateQueries({ queryKey: qk.nodes })
              onToken(e)
            } catch (e) {
              toast(errorMessage(e), 'error')
            }
            setConfirmToken(false)
          }}
        />
      )}
      {editing && <EditNode n={n} onClose={() => setEditing(false)} />}
    </article>
  )
}

function AddNode({ onClose, onCreated }: { onClose: () => void; onCreated: (e: NodeEnrollment) => void }) {
  const [name, setName] = useState('')
  const [address, setAddress] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const qc = useQueryClient()
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      const res = await api.createNode(name.trim(), address.trim())
      qc.invalidateQueries({ queryKey: qk.nodes })
      onCreated(res)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal title="Add node" onClose={onClose}>
      <form className="stack" onSubmit={submit}>
        <Field label="Name">
          <input autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="homelab" required />
        </Field>
        <Field label="Address" hint="Where the gateway on this dashboard host reaches the node, e.g. its Tailscale IP.">
          <input value={address} onChange={(e) => setAddress(e.target.value)} placeholder="100.64.0.12" required />
        </Field>
        {error && (
          <div className="alert alert-error" role="alert">
            {error}
          </div>
        )}
        <div className="row end gap-sm">
          <button type="button" className="btn" onClick={onClose}>
            Cancel
          </button>
          <button className="btn btn-primary" disabled={busy}>
            Add node
          </button>
        </div>
      </form>
    </Modal>
  )
}

function EditNode({ n, onClose }: { n: Node; onClose: () => void }) {
  const [name, setName] = useState(n.name)
  const [address, setAddress] = useState(n.address)
  const qc = useQueryClient()
  const toast = useToast()
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    try {
      await api.patchNode(n.id, { name: name.trim(), address: address.trim() })
      qc.invalidateQueries({ queryKey: qk.nodes })
      toast('Node saved')
      onClose()
    } catch (err) {
      toast(errorMessage(err), 'error')
    }
  }
  return (
    <Modal title={`Edit ${n.name}`} onClose={onClose}>
      <form className="stack" onSubmit={submit}>
        <Field label="Name">
          <input value={name} onChange={(e) => setName(e.target.value)} required />
        </Field>
        <Field label="Address">
          <input value={address} onChange={(e) => setAddress(e.target.value)} required />
        </Field>
        <div className="row end gap-sm">
          <button type="button" className="btn" onClick={onClose}>
            Cancel
          </button>
          <button className="btn btn-primary">Save</button>
        </div>
      </form>
    </Modal>
  )
}

function EnrollmentModal({ e, onClose }: { e: NodeEnrollment; onClose: () => void }) {
  const nodeYaml = `# /etc/tb-agent/node.yaml
name: ${e.node.name}
dashboard:
  endpoint: ${e.grpc_endpoint}
  token: ${e.enrollment_token}
listen_address: 0.0.0.0
limits:
  ram: 16G
  cpu: 70%
  storage: 300G
drivers:
  libvirt: {}
`
  const compose = `# docker-compose.yml (Linux nodes)
services:
  tb-agent:
    image: ghcr.io/finn-freitag/tb-agent:latest
    restart: unless-stopped
    network_mode: host
    volumes:
      - ./node.yaml:/etc/tb-agent/node.yaml:ro
      - tb-agent-data:/var/lib/tb-agent
      - /var/run/libvirt:/var/run/libvirt
      # docker-qemu / android: - /var/run/docker.sock:/var/run/docker.sock
      # lxc:                   - /var/lib/incus/unix.socket:/var/lib/incus/unix.socket

volumes:
  tb-agent-data:
`
  return (
    <Modal
      title={`Enroll ${e.node.name}`}
      onClose={onClose}
      wide
      footer={
        <button className="btn btn-primary" onClick={onClose}>
          Done
        </button>
      }
    >
      <p>
        The token is valid for one enrollment and is shown only now. Install tb-agent on <strong>{e.node.address}</strong> with this config, adjust limits and
        drivers, then start it.
      </p>
      <Field label="Enrollment token">
        <div className="codeblock">
          <code>{e.enrollment_token}</code>
          <CopyButton value={e.enrollment_token} label="Copy token" />
        </div>
      </Field>
      <Field label="node.yaml">
        <div className="codeblock">
          <pre>{nodeYaml}</pre>
          <CopyButton value={nodeYaml} label="Copy node.yaml" />
        </div>
      </Field>
      <Field label="docker-compose.yml">
        <div className="codeblock">
          <pre>{compose}</pre>
          <CopyButton value={compose} label="Copy docker-compose.yml" />
        </div>
      </Field>
      <p className="muted small">macOS nodes (Orchard) run tb-agent natively as a launchd service with the same node.yaml.</p>
    </Modal>
  )
}

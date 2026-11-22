import { useState } from 'react'
import type { ChangeEvent, FormEvent } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Pencil, Plus, Trash2, Users as UsersIcon } from 'lucide-react'
import { api } from '../../api/client'
import { qk, useMe, useNodes, useTemplates, useUsers } from '../../api/hooks'
import type { Quota, Role, User } from '../../api/types'
import { ALL_DRIVERS, ALL_EXPOSURES, emptyQuota } from '../../api/types'
import { Badge, ChipSelect, Confirm, Empty, ErrorBox, Field, Loading, Modal, useToast } from '../../components/ui'
import { errorMessage, fmtMB, limitText } from '../../util'

export function UsersPage() {
  const users = useUsers()
  const me = useMe()
  const [editing, setEditing] = useState<User | 'new' | null>(null)
  const [del, setDel] = useState<User | null>(null)
  const qc = useQueryClient()
  const toast = useToast()

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1>Users</h1>
          <p className="muted">Quotas: 0 or nothing selected means unlimited / all allowed.</p>
        </div>
        <button className="btn btn-primary" onClick={() => setEditing('new')}>
          <Plus size={16} /> Add user
        </button>
      </div>
      {users.isPending ? (
        <Loading />
      ) : users.isError ? (
        <ErrorBox error={users.error} retry={() => users.refetch()} />
      ) : users.data.length === 0 ? (
        <Empty icon={<UsersIcon size={40} />} title="No users" />
      ) : (
        <div className="table-wrap card">
          <table className="table">
            <thead>
              <tr>
                <th>User</th>
                <th>Machines</th>
                <th>vCPU</th>
                <th>RAM</th>
                <th>Storage</th>
                <th>Phones</th>
                <th>Drivers</th>
                <th aria-label="Actions" />
              </tr>
            </thead>
            <tbody>
              {users.data.map((u) => (
                <tr key={u.id}>
                  <td>
                    <strong>{u.username}</strong> {u.role === 'admin' && <Badge tone="info">admin</Badge>}
                  </td>
                  <td>
                    {u.usage.machines} / {limitText(u.quota.max_machines)}
                  </td>
                  <td>
                    {u.usage.vcpus} / {limitText(u.quota.max_vcpus)}
                  </td>
                  <td>
                    {fmtMB(u.usage.ram_mb)} / {limitText(u.quota.max_ram_mb, fmtMB)}
                  </td>
                  <td>
                    {u.usage.storage_gb} / {limitText(u.quota.max_storage_gb, (v) => `${v} GB`)}
                  </td>
                  <td>
                    {u.usage.phones} / {limitText(u.quota.max_phones)}
                  </td>
                  <td className="small">{u.quota.allowed_drivers.length ? u.quota.allowed_drivers.join(', ') : 'all'}</td>
                  <td className="right nowrap">
                    <button className="icon-btn" aria-label={`Edit ${u.username}`} onClick={() => setEditing(u)}>
                      <Pencil size={14} />
                    </button>
                    <button className="icon-btn" aria-label={`Delete ${u.username}`} disabled={u.id === me.data?.id} onClick={() => setDel(u)}>
                      <Trash2 size={14} />
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {editing && <UserEditor user={editing === 'new' ? null : editing} onClose={() => setEditing(null)} />}
      {del && (
        <Confirm
          title="Delete user?"
          danger
          confirmLabel="Delete"
          text={`${del.username} will be deleted. Users that still own devices can't be deleted.`}
          onClose={() => setDel(null)}
          onConfirm={async () => {
            try {
              await api.deleteUser(del.id)
              qc.invalidateQueries({ queryKey: qk.users })
              toast('User deleted')
            } catch (e) {
              toast(errorMessage(e), 'error')
            }
            setDel(null)
          }}
        />
      )}
    </div>
  )
}

function UserEditor({ user, onClose }: { user: User | null; onClose: () => void }) {
  const [username, setUsername] = useState(user?.username ?? '')
  const [password, setPassword] = useState('')
  const [role, setRole] = useState<Role>(user?.role ?? 'user')
  const [quota, setQuota] = useState<Quota>(user ? { ...user.quota } : emptyQuota())
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const templates = useTemplates()
  const nodes = useNodes()
  const qc = useQueryClient()
  const toast = useToast()

  const pools = [...new Set((nodes.data ?? []).flatMap((n) => n.capabilities?.license_pools.map((p) => p.name) ?? []))].sort()
  const allPools = [...new Set([...pools, ...quota.allowed_license_pools])]
  const num = (k: keyof Quota) => (e: ChangeEvent<HTMLInputElement>) => setQuota({ ...quota, [k]: Math.max(0, Number(e.target.value) || 0) })

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      if (user) await api.patchUser(user.id, { role, quota, ...(password ? { password } : {}) })
      else await api.createUser({ username: username.trim(), password, role, quota })
      qc.invalidateQueries({ queryKey: qk.users })
      qc.invalidateQueries({ queryKey: qk.me })
      toast(user ? 'User saved' : 'User created')
      onClose()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal title={user ? `Edit ${user.username}` : 'Add user'} onClose={onClose} wide>
      <form className="stack" onSubmit={submit}>
        <div className="grid-3">
          <Field label="Username">
            <input value={username} onChange={(e) => setUsername(e.target.value)} disabled={!!user} required autoComplete="off" />
          </Field>
          <Field label={user ? 'New password (optional)' : 'Password'}>
            <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} required={!user} minLength={8} autoComplete="new-password" />
          </Field>
          <Field label="Role">
            <select value={role} onChange={(e) => setRole(e.target.value as Role)}>
              <option value="user">user</option>
              <option value="admin">admin</option>
            </select>
          </Field>
        </div>

        <h3 className="group-title">Limits</h3>
        <div className="grid-3">
          <Field label="Max machines">
            <input type="number" min={0} value={quota.max_machines} onChange={num('max_machines')} />
          </Field>
          <Field label="Max vCPUs (total)">
            <input type="number" min={0} value={quota.max_vcpus} onChange={num('max_vcpus')} />
          </Field>
          <Field label="Max RAM (MB, total)">
            <input type="number" min={0} step={1024} value={quota.max_ram_mb} onChange={num('max_ram_mb')} />
          </Field>
          <Field label="Max storage (GB, total)">
            <input type="number" min={0} value={quota.max_storage_gb} onChange={num('max_storage_gb')} />
          </Field>
          <Field label="Max phones">
            <input type="number" min={0} value={quota.max_phones} onChange={num('max_phones')} />
          </Field>
          <Field label="Max devices with GPU" hint="0 = unlimited">
            <input type="number" min={0} value={quota.max_gpus ?? 0} onChange={num('max_gpus')} />
          </Field>
          <Field label="Max TTL (hours)" hint="0 = devices may live forever">
            <input type="number" min={0} value={quota.max_ttl_hours} onChange={num('max_ttl_hours')} />
          </Field>
        </div>

        <h3 className="group-title">Access</h3>
        <Field label="Allowed drivers">
          <ChipSelect options={ALL_DRIVERS} value={quota.allowed_drivers} onChange={(v) => setQuota({ ...quota, allowed_drivers: v })} allLabel="All" />
        </Field>
        <Field label="Allowed templates">
          <ChipSelect
            options={(templates.data ?? []).map((t) => t.id)}
            value={quota.allowed_templates}
            onChange={(v) => setQuota({ ...quota, allowed_templates: v })}
            allLabel="All"
          />
        </Field>
        <Field label="Allowed license pools" hint={allPools.length === 0 ? 'No license pools are configured on any node.' : 'Nothing selected = all pools.'}>
          <ChipSelect options={allPools} value={quota.allowed_license_pools} onChange={(v) => setQuota({ ...quota, allowed_license_pools: v })} allLabel="All" />
        </Field>
        <Field label="Allowed exposures">
          <ChipSelect
            options={ALL_EXPOSURES}
            value={quota.allowed_exposures}
            onChange={(v) => setQuota({ ...quota, allowed_exposures: v as Quota['allowed_exposures'] })}
            allLabel="All"
          />
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
            {user ? 'Save' : 'Create user'}
          </button>
        </div>
      </form>
    </Modal>
  )
}

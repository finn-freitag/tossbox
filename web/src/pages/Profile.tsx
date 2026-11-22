import { useState } from 'react'
import type { FormEvent } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { KeyRound, Plus, Trash2 } from 'lucide-react'
import { api } from '../api/client'
import { qk, useMe, useTokens } from '../api/hooks'
import type { CreatedToken } from '../api/types'
import { Confirm, CopyButton, Empty, ErrorBox, Field, Loading, Modal, useToast } from '../components/ui'
import { Quotas } from '../components/Quotas'
import { errorMessage, fmtDate, fmtRelative } from '../util'

export function ProfilePage() {
  const me = useMe()
  if (me.isPending) return <Loading />
  if (me.isError || !me.data) return <ErrorBox error={me.error ?? 'Not signed in'} />
  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1>Profile</h1>
          <p className="muted">
            Signed in as <strong>{me.data.username}</strong> ({me.data.role})
          </p>
          <Quotas quota={me.data.quota} usage={me.data.usage} />
        </div>
      </div>
      <div className="stack">
        <SshKeys initial={me.data.ssh_keys} />
        <Tokens />
        <Password />
      </div>
    </div>
  )
}

function SshKeys({ initial }: { initial: string[] }) {
  const [text, setText] = useState(initial.join('\n'))
  const [busy, setBusy] = useState(false)
  const qc = useQueryClient()
  const toast = useToast()
  const keys = text
    .split('\n')
    .map((s) => s.trim())
    .filter(Boolean)
  const bad = keys.find((k) => !/^(ssh-(ed25519|rsa|dss)|ecdsa-sha2-\S+|sk-\S+)\s+\S+/.test(k))
  const save = async () => {
    setBusy(true)
    try {
      const u = await api.setSshKeys(keys)
      qc.setQueryData(qk.me, u)
      toast('SSH keys saved')
    } catch (e) {
      toast(errorMessage(e), 'error')
    } finally {
      setBusy(false)
    }
  }
  return (
    <section className="card section">
      <h2 className="section-title">SSH keys</h2>
      <p className="muted small">These public keys are added to the default user of every new device you create.</p>
      <Field label="Public keys (one per line)" error={bad ? `Doesn't look like an SSH public key: ${bad.slice(0, 40)}…` : undefined}>
        <textarea rows={4} className="mono" value={text} onChange={(e) => setText(e.target.value)} placeholder="ssh-ed25519 AAAA… you@laptop" spellCheck={false} />
      </Field>
      <div>
        <button className="btn btn-primary" disabled={busy || !!bad} onClick={save}>
          Save keys
        </button>
      </div>
    </section>
  )
}

function Tokens() {
  const tokens = useTokens()
  const qc = useQueryClient()
  const toast = useToast()
  const [creating, setCreating] = useState(false)
  const [name, setName] = useState('')
  const [days, setDays] = useState(0)
  const [created, setCreated] = useState<CreatedToken | null>(null)
  const [del, setDel] = useState<string | null>(null)

  const create = async (e: FormEvent) => {
    e.preventDefault()
    try {
      const t = await api.createToken(name.trim(), days)
      setCreated(t)
      setCreating(false)
      setName('')
      qc.invalidateQueries({ queryKey: qk.tokens })
    } catch (err) {
      toast(errorMessage(err), 'error')
    }
  }

  return (
    <section className="card section">
      <div className="row between">
        <h2 className="section-title">API tokens</h2>
        <button className="btn btn-sm" onClick={() => setCreating(true)}>
          <Plus size={14} /> New token
        </button>
      </div>
      <p className="muted small">
        Use as <code>Authorization: Bearer &lt;token&gt;</code> for automation such as CI. Tokens have your permissions and quotas.
      </p>
      {tokens.isPending ? (
        <Loading />
      ) : tokens.isError ? (
        <ErrorBox error={tokens.error} />
      ) : tokens.data.length === 0 ? (
        <Empty icon={<KeyRound size={28} />} title="No tokens" />
      ) : (
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th>Name</th>
                <th>Created</th>
                <th>Last used</th>
                <th>Expires</th>
                <th aria-label="Actions" />
              </tr>
            </thead>
            <tbody>
              {tokens.data.map((t) => (
                <tr key={t.id}>
                  <td>{t.name}</td>
                  <td>{fmtDate(t.created_at)}</td>
                  <td>{fmtRelative(t.last_used_at)}</td>
                  <td>{t.expires_at ? fmtDate(t.expires_at) : 'never'}</td>
                  <td className="right">
                    <button className="icon-btn" aria-label={`Delete token ${t.name}`} onClick={() => setDel(t.id)}>
                      <Trash2 size={14} />
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {creating && (
        <Modal title="New API token" onClose={() => setCreating(false)}>
          <form className="stack" onSubmit={create}>
            <Field label="Name">
              <input autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="gitlab-ci" required />
            </Field>
            <Field label="Expires in (days, 0 = never)">
              <input type="number" min={0} value={days} onChange={(e) => setDays(Math.max(0, Number(e.target.value) || 0))} />
            </Field>
            <div className="row end gap-sm">
              <button type="button" className="btn" onClick={() => setCreating(false)}>
                Cancel
              </button>
              <button className="btn btn-primary" disabled={!name.trim()}>
                Create
              </button>
            </div>
          </form>
        </Modal>
      )}
      {created && (
        <Modal title="Token created" onClose={() => setCreated(null)} footer={<button className="btn btn-primary" onClick={() => setCreated(null)}>Done</button>}>
          <p>Copy the token now. It won't be shown again.</p>
          <div className="codeblock">
            <code>{created.token}</code>
            <CopyButton value={created.token} label="Copy token" />
          </div>
        </Modal>
      )}
      {del && (
        <Confirm
          title="Delete token?"
          danger
          confirmLabel="Delete"
          text="Anything using this token will stop working."
          onClose={() => setDel(null)}
          onConfirm={async () => {
            try {
              await api.deleteToken(del)
              qc.invalidateQueries({ queryKey: qk.tokens })
              toast('Token deleted')
            } catch (e) {
              toast(errorMessage(e), 'error')
            }
            setDel(null)
          }}
        />
      )}
    </section>
  )
}

function Password() {
  const [cur, setCur] = useState('')
  const [next, setNext] = useState('')
  const [again, setAgain] = useState('')
  const [busy, setBusy] = useState(false)
  const toast = useToast()
  const mismatch = again !== '' && next !== again
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    try {
      await api.changePassword(cur, next)
      toast('Password changed')
      setCur('')
      setNext('')
      setAgain('')
    } catch (err) {
      toast(errorMessage(err), 'error')
    } finally {
      setBusy(false)
    }
  }
  return (
    <section className="card section">
      <h2 className="section-title">Password</h2>
      <form className="grid-3" onSubmit={submit}>
        <Field label="Current password">
          <input type="password" autoComplete="current-password" value={cur} onChange={(e) => setCur(e.target.value)} required />
        </Field>
        <Field label="New password">
          <input type="password" autoComplete="new-password" minLength={8} value={next} onChange={(e) => setNext(e.target.value)} required />
        </Field>
        <Field label="Repeat new password" error={mismatch ? "Passwords don't match" : undefined}>
          <input type="password" autoComplete="new-password" value={again} onChange={(e) => setAgain(e.target.value)} required />
        </Field>
        <div>
          <button className="btn btn-primary" disabled={busy || mismatch || !next}>
            Change password
          </button>
        </div>
      </form>
    </section>
  )
}

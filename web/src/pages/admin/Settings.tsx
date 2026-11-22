import { useEffect, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Loader2 } from 'lucide-react'
import { api } from '../../api/client'
import { qk, useSettings } from '../../api/hooks'
import type { ServiceName, Settings } from '../../api/types'
import { ALL_SERVICES } from '../../api/types'
import { ChipSelect, ErrorBox, Field, Loading, Switch, useToast } from '../../components/ui'
import { errorMessage } from '../../util'

export function SettingsPage() {
  const q = useSettings()
  if (q.isPending) return <Loading />
  if (q.isError) return <ErrorBox error={q.error} retry={() => q.refetch()} />
  return <SettingsForm initial={q.data} />
}

function Toggle({ label, hint, checked, onChange }: { label: string; hint?: string; checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <div className="toggle-row">
      <div>
        <div className="field-label">{label}</div>
        {hint && <div className="field-hint">{hint}</div>}
      </div>
      <Switch checked={checked} onChange={onChange} label={label} />
    </div>
  )
}

function SettingsForm({ initial }: { initial: Settings }) {
  const [s, setS] = useState<Settings>(initial)
  const [busy, setBusy] = useState(false)
  const qc = useQueryClient()
  const toast = useToast()
  useEffect(() => setS(initial), [initial])
  const set = <K extends keyof Settings>(k: K, v: Settings[K]) => setS((x) => ({ ...x, [k]: v }))
  const dirty = JSON.stringify(s) !== JSON.stringify(initial)
  const invalid = s.password_length < 20 || s.ram_overcommit_ratio < 1 || s.cpu_overcommit_ratio < 1

  const save = async () => {
    setBusy(true)
    try {
      const saved = await api.putSettings(s)
      qc.setQueryData(qk.settings, saved)
      toast('Settings saved')
    } catch (e) {
      toast(errorMessage(e), 'error')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1>Settings</h1>
          <p className="muted">Global behaviour. Domain, ports and secrets are configured via environment variables.</p>
        </div>
        <div className="row gap-sm">
          <button className="btn" disabled={!dirty || busy} onClick={() => setS(initial)}>
            Reset
          </button>
          <button className="btn btn-primary" disabled={!dirty || busy || invalid} onClick={save}>
            {busy && <Loader2 size={16} className="spin" />} Save
          </button>
        </div>
      </div>

      <div className="stack">
        <section className="card section">
          <h2 className="section-title">Capacity</h2>
          <Toggle label="RAM overcommit" hint="Allow allocating more RAM than a node's limit." checked={s.overcommit_ram} onChange={(v) => set('overcommit_ram', v)} />
          {s.overcommit_ram && (
            <Field label="RAM overcommit ratio" error={s.ram_overcommit_ratio < 1 ? 'Must be at least 1' : undefined}>
              <input type="number" step={0.1} min={1} value={s.ram_overcommit_ratio} onChange={(e) => set('ram_overcommit_ratio', Number(e.target.value))} />
            </Field>
          )}
          <Toggle label="CPU overcommit" hint="Allow CPU caps to add up to more than a node's CPU limit." checked={s.overcommit_cpu} onChange={(v) => set('overcommit_cpu', v)} />
          {s.overcommit_cpu && (
            <Field label="CPU overcommit ratio" error={s.cpu_overcommit_ratio < 1 ? 'Must be at least 1' : undefined}>
              <input type="number" step={0.5} min={1} value={s.cpu_overcommit_ratio} onChange={(e) => set('cpu_overcommit_ratio', Number(e.target.value))} />
            </Field>
          )}
          <Field label="Storage accounting" hint="Provisioned counts the full disk size (safe); actual counts real usage of thin disks (efficient).">
            <div className="seg" role="radiogroup" aria-label="Storage accounting">
              {(['provisioned', 'actual'] as const).map((v) => (
                <button key={v} type="button" role="radio" aria-checked={s.storage_accounting === v} className={s.storage_accounting === v ? 'on' : ''} onClick={() => set('storage_accounting', v)}>
                  {v}
                </button>
              ))}
            </div>
          </Field>
          <Toggle
            label="Stopped devices keep their reservation"
            hint="Guarantees a stopped device can start again. Off releases CPU and RAM while stopped."
            checked={s.stopped_keeps_reservation}
            onChange={(v) => set('stopped_keeps_reservation', v)}
          />
        </section>

        <section className="card section">
          <h2 className="section-title">Devices</h2>
          <Field label="Default services" hint="Pre-selected when creating a device (only where the template supports them).">
            <ChipSelect options={ALL_SERVICES} value={s.default_services} onChange={(v) => set('default_services', v as ServiceName[])} />
          </Field>
          <Field label="Generated password length" error={s.password_length < 20 ? 'Must be at least 20' : undefined}>
            <input type="number" min={20} value={s.password_length} onChange={(e) => set('password_length', Number(e.target.value))} />
          </Field>
          <Field label="Default TTL (hours, 0 = never)">
            <input type="number" min={0} value={s.default_ttl_hours} onChange={(e) => set('default_ttl_hours', Math.max(0, Number(e.target.value) || 0))} />
          </Field>
          <Toggle label="API-created devices require a TTL" hint="Devices created with an API token must set ttl_hours." checked={s.api_requires_ttl} onChange={(v) => set('api_requires_ttl', v)} />
          <Toggle label="Allow public VNC" hint="VNC is unencrypted. When off, VNC can only be exposed via Tailscale or internally." checked={s.public_vnc_allowed} onChange={(v) => set('public_vnc_allowed', v)} />
        </section>
      </div>
    </div>
  )
}

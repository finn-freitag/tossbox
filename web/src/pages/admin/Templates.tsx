import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { LayoutTemplate, Loader2, RefreshCw } from 'lucide-react'
import { api } from '../../api/client'
import { qk, useTemplates } from '../../api/hooks'
import type { ReloadResult } from '../../api/types'
import { Badge, Empty, ErrorBox, Loading, OsIcon, useToast } from '../../components/ui'
import { errorMessage, fmtMB } from '../../util'

export function TemplatesPage() {
  const templates = useTemplates()
  const qc = useQueryClient()
  const toast = useToast()
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState<ReloadResult | null>(null)

  const reload = async () => {
    setBusy(true)
    try {
      const r = await api.reloadTemplates()
      setResult(r)
      qc.invalidateQueries({ queryKey: qk.templates })
      qc.invalidateQueries({ queryKey: qk.createOptions })
      toast(`Loaded ${r.count} templates`, r.errors.length ? 'error' : 'ok')
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
          <h1>Templates</h1>
          <p className="muted">
            Loaded from <code>TOSSBOX_TEMPLATE_SOURCES</code> (catalog URLs or directories).
          </p>
        </div>
        <button className="btn" onClick={reload} disabled={busy}>
          {busy ? <Loader2 size={16} className="spin" /> : <RefreshCw size={16} />} Reload sources
        </button>
      </div>
      {result && result.errors.length > 0 && (
        <div className="alert alert-error" role="alert">
          <div>
            <strong>Some sources failed:</strong>
            <ul>
              {result.errors.map((e) => (
                <li key={e}>{e}</li>
              ))}
            </ul>
          </div>
        </div>
      )}
      {templates.isPending ? (
        <Loading />
      ) : templates.isError ? (
        <ErrorBox error={templates.error} retry={() => templates.refetch()} />
      ) : templates.data.length === 0 ? (
        <Empty icon={<LayoutTemplate size={40} />} title="No templates loaded" />
      ) : (
        <div className="table-wrap card">
          <table className="table">
            <thead>
              <tr>
                <th>Template</th>
                <th>Drivers</th>
                <th>Services</th>
                <th>Minimum</th>
                <th>Source</th>
              </tr>
            </thead>
            <tbody>
              {templates.data.map((t) => (
                <tr key={t.id}>
                  <td>
                    <div className="row gap-sm">
                      <OsIcon family={t.family} size={26} />
                      <div>
                        <strong>{t.name}</strong>
                        <div className="muted small">
                          {t.id} · v{t.version} {t.license && <Badge tone="info">license</Badge>}
                        </div>
                      </div>
                    </div>
                  </td>
                  <td className="small">
                    {t.compatible_drivers.map((d) => (
                      <Badge key={d} tone={t.available_drivers.includes(d) ? 'ok' : 'neutral'}>
                        {d}
                      </Badge>
                    ))}
                  </td>
                  <td className="small">{t.supported_services.join(', ')}</td>
                  <td className="small nowrap">
                    {t.min_resources.vcpus} vCPU · {fmtMB(t.min_resources.ram_mb)} · {t.min_resources.storage_gb} GB
                  </td>
                  <td className="small truncate source" title={t.source}>
                    {t.source}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <p className="muted small">Green drivers are enabled on at least one online node.</p>
    </div>
  )
}

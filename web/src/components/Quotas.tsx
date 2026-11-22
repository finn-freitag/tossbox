import type { Quota, Usage } from '../api/types'
import { fmtMB } from '../util'

/** One-line quota usage summary ("2/5 machines · 6/16 vCPU · …"); unlimited values are omitted. */
export function Quotas({ quota, usage }: { quota: Quota; usage: Usage }) {
  const parts: string[] = []
  const add = (used: number, max: number, label: string, fmt: (n: number) => string = String) =>
    parts.push(max ? `${fmt(used)} / ${fmt(max)} ${label}` : `${fmt(used)} ${label}`)
  add(usage.machines, quota.max_machines, 'machines')
  add(usage.vcpus, quota.max_vcpus, 'vCPU')
  add(usage.ram_mb, quota.max_ram_mb, 'RAM', fmtMB)
  add(usage.storage_gb, quota.max_storage_gb, 'GB disk')
  if (quota.max_phones || usage.phones) add(usage.phones, quota.max_phones, 'phones')
  return <p className="muted small">{parts.join(' · ')}</p>
}

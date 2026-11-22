import { ApiError } from './api/client'

/** DNS-safe hostname slug, same rules as the backend (lowercase, [a-z0-9-], max 63). */
export function slug(name: string): string {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, '-')
    .replace(/-{2,}/g, '-')
    .replace(/^-+/, '')
    .slice(0, 63)
    .replace(/-+$/, '')
}

export function fmtMB(mb: number): string {
  if (mb >= 1024) {
    const g = mb / 1024
    return `${Number.isInteger(g) ? g : g.toFixed(1)} GB`
  }
  return `${mb} MB`
}

export function fmtGB(gb: number): string {
  if (gb >= 1024) return `${(gb / 1024).toFixed(1)} TB`
  return `${gb} GB`
}

export function fmtRelative(iso: string | null | undefined): string {
  if (!iso) return 'never'
  const diff = new Date(iso).getTime() - Date.now()
  const abs = Math.abs(diff)
  const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' })
  const units: [Intl.RelativeTimeFormatUnit, number][] = [
    ['day', 86_400_000],
    ['hour', 3_600_000],
    ['minute', 60_000],
    ['second', 1000],
  ]
  for (const [unit, ms] of units) {
    if (abs >= ms || unit === 'second') return rtf.format(Math.round(diff / ms), unit)
  }
  return ''
}

/** Remaining time until iso as a compact countdown: "2 d 3 h", "1 h 05 min", "12 min 08 s", "45 s". */
export function fmtCountdown(iso: string, now: number = Date.now()): string {
  let s = Math.max(0, Math.floor((new Date(iso).getTime() - now) / 1000))
  const d = Math.floor(s / 86_400)
  s -= d * 86_400
  const h = Math.floor(s / 3600)
  s -= h * 3600
  const m = Math.floor(s / 60)
  s -= m * 60
  const pad = (n: number) => String(n).padStart(2, '0')
  if (d > 0) return `${d} d ${h} h`
  if (h > 0) return `${h} h ${pad(m)} min`
  if (m > 0) return `${m} min ${pad(s)} s`
  return `${s} s`
}

export function fmtDate(iso: string | null | undefined): string {
  if (!iso) return '—'
  return new Date(iso).toLocaleString()
}

export function errorMessage(e: unknown): string {
  if (e instanceof ApiError) return e.message
  if (e instanceof Error) return e.message
  return String(e)
}

export function limitText(v: number, fmt: (n: number) => string = String): string {
  return v === 0 ? 'unlimited' : fmt(v)
}

export const familyLabel: Record<string, string> = {
  linux: 'Linux',
  windows: 'Windows',
  android: 'Android',
  macos: 'macOS',
  ios: 'iOS',
}

export const serviceLabel: Record<string, string> = {
  ssh: 'SSH',
  rdp: 'RDP',
  vnc: 'VNC',
  adb: 'ADB',
  http: 'HTTP',
  https: 'HTTPS',
}

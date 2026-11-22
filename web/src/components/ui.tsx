import { createContext, useCallback, useContext, useEffect, useId, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { AlertTriangle, Check, Copy, Eye, EyeOff, Loader2, X } from 'lucide-react'
import type { DeviceState, Family } from '../api/types'

// ------------------------------------------------------------------ toasts

type Toast = { id: number; kind: 'ok' | 'error'; text: string }
const ToastCtx = createContext<(text: string, kind?: Toast['kind']) => void>(() => {})

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([])
  const push = useCallback((text: string, kind: Toast['kind'] = 'ok') => {
    const id = Date.now() + Math.random()
    setToasts((t) => [...t, { id, kind, text }])
    setTimeout(() => setToasts((t) => t.filter((x) => x.id !== id)), kind === 'error' ? 7000 : 3500)
  }, [])
  return (
    <ToastCtx.Provider value={push}>
      {children}
      <div className="toasts" role="status" aria-live="polite">
        {toasts.map((t) => (
          <div key={t.id} className={`toast toast-${t.kind}`}>
            {t.kind === 'error' ? <AlertTriangle size={16} /> : <Check size={16} />}
            <span>{t.text}</span>
            <button className="icon-btn" aria-label="Dismiss" onClick={() => setToasts((x) => x.filter((y) => y.id !== t.id))}>
              <X size={14} />
            </button>
          </div>
        ))}
      </div>
    </ToastCtx.Provider>
  )
}

export const useToast = () => useContext(ToastCtx)

// ------------------------------------------------------------------ modal

export function Modal({
  title,
  onClose,
  children,
  footer,
  wide,
}: {
  title: string
  onClose: () => void
  children: ReactNode
  footer?: ReactNode
  wide?: boolean
}) {
  const ref = useRef<HTMLDialogElement>(null)
  const titleId = useId()
  useEffect(() => {
    const d = ref.current
    if (d && !d.open) d.showModal()
  }, [])
  return (
    <dialog
      ref={ref}
      className={`modal ${wide ? 'modal-wide' : ''}`}
      aria-labelledby={titleId}
      onCancel={(e) => {
        e.preventDefault()
        onClose()
      }}
      onClick={(e) => {
        if (e.target === ref.current) onClose()
      }}
    >
      <div className="modal-inner">
        <header className="modal-head">
          <h2 id={titleId}>{title}</h2>
          <button className="icon-btn" aria-label="Close" onClick={onClose}>
            <X size={18} />
          </button>
        </header>
        <div className="modal-body">{children}</div>
        {footer && <footer className="modal-foot">{footer}</footer>}
      </div>
    </dialog>
  )
}

export function Confirm({
  title,
  text,
  confirmLabel = 'Confirm',
  danger,
  busy,
  onConfirm,
  onClose,
}: {
  title: string
  text: ReactNode
  confirmLabel?: string
  danger?: boolean
  busy?: boolean
  onConfirm: () => void
  onClose: () => void
}) {
  return (
    <Modal
      title={title}
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button className={`btn ${danger ? 'btn-danger' : 'btn-primary'}`} disabled={busy} onClick={onConfirm}>
            {busy && <Loader2 size={14} className="spin" />} {confirmLabel}
          </button>
        </>
      }
    >
      <p>{text}</p>
    </Modal>
  )
}

// ------------------------------------------------------------------ small widgets

export function CopyButton({ value, label = 'Copy' }: { value: string; label?: string }) {
  const [done, setDone] = useState(false)
  return (
    <button
      type="button"
      className="icon-btn"
      title={label}
      aria-label={label}
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(value)
        } catch {
          const ta = document.createElement('textarea')
          ta.value = value
          document.body.appendChild(ta)
          ta.select()
          document.execCommand('copy')
          ta.remove()
        }
        setDone(true)
        setTimeout(() => setDone(false), 1200)
      }}
    >
      {done ? <Check size={14} /> : <Copy size={14} />}
    </button>
  )
}

export function Secret({ value }: { value: string }) {
  const [shown, setShown] = useState(false)
  return (
    <span className="secret">
      <code>{shown ? value : '•'.repeat(Math.min(value.length, 16))}</code>
      <button type="button" className="icon-btn" aria-label={shown ? 'Hide password' : 'Show password'} onClick={() => setShown(!shown)}>
        {shown ? <EyeOff size={14} /> : <Eye size={14} />}
      </button>
      <CopyButton value={value} label="Copy password" />
    </span>
  )
}

export function Switch({
  checked,
  onChange,
  disabled,
  label,
}: {
  checked: boolean
  onChange: (v: boolean) => void
  disabled?: boolean
  label: string
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      title={label}
      disabled={disabled}
      className={`switch ${checked ? 'on' : ''}`}
      onClick={() => onChange(!checked)}
    >
      <span className="knob" />
    </button>
  )
}

const stateTone: Record<DeviceState, string> = {
  pending: 'neutral',
  provisioning: 'info',
  running: 'ok',
  stopping: 'warn',
  stopped: 'neutral',
  destroying: 'warn',
  destroyed: 'neutral',
  failed: 'error',
  offline: 'error',
}

export function StateBadge({ state, ready }: { state: DeviceState; ready?: boolean }) {
  const busy = state === 'provisioning' || state === 'stopping' || state === 'destroying' || state === 'pending'
  const label = state === 'running' && ready === false ? 'booting' : state
  return (
    <span className={`badge badge-${state === 'running' && ready === false ? 'info' : stateTone[state]}`}>
      {busy || label === 'booting' ? <Loader2 size={12} className="spin" /> : <span className="dot" />}
      {label}
    </span>
  )
}

export function Badge({ tone = 'neutral', children }: { tone?: string; children: ReactNode }) {
  return <span className={`badge badge-${tone}`}>{children}</span>
}

const familyColor: Record<Family, string> = {
  linux: 'var(--c-linux)',
  windows: 'var(--c-windows)',
  android: 'var(--c-android)',
  macos: 'var(--c-macos)',
  ios: 'var(--c-macos)',
  chromeos: 'var(--c-chromeos)',
  browser: 'var(--c-browser)',
}

/** Simple, brand-neutral OS glyph: colored tile with a short label. */
export function OsIcon({ family, size = 32 }: { family: Family; size?: number }) {
  const text = { linux: 'Lx', windows: 'Win', android: 'And', macos: 'mac', ios: 'iOS', chromeos: 'CrOS', browser: 'Web' }[family]
  return (
    <span
      className="os-icon"
      style={{ width: size, height: size, background: familyColor[family], fontSize: size * 0.34 }}
      aria-hidden="true"
    >
      {text}
    </span>
  )
}

export function Meter({ label, used, total, fmt = String }: { label: string; used: number; total: number; fmt?: (n: number) => string }) {
  const pct = total > 0 ? Math.min(100, (used / total) * 100) : 0
  const tone = pct > 90 ? 'error' : pct > 70 ? 'warn' : 'ok'
  return (
    <div className="meter">
      <div className="meter-head">
        <span>{label}</span>
        <span className="muted">
          {fmt(used)} / {total > 0 ? fmt(total) : '∞'}
        </span>
      </div>
      <div className="meter-track" role="progressbar" aria-valuenow={Math.round(pct)} aria-valuemin={0} aria-valuemax={100} aria-label={label}>
        <div className={`meter-fill tone-${tone}`} style={{ width: `${pct}%` }} />
      </div>
    </div>
  )
}

export function Loading({ text = 'Loading…' }: { text?: string }) {
  return (
    <div className="state-box">
      <Loader2 className="spin" size={20} />
      <span>{text}</span>
    </div>
  )
}

export function ErrorBox({ error, retry }: { error: unknown; retry?: () => void }) {
  const msg = error instanceof Error ? error.message : String(error)
  return (
    <div className="state-box state-error" role="alert">
      <AlertTriangle size={20} />
      <span>{msg}</span>
      {retry && (
        <button className="btn btn-sm" onClick={retry}>
          Retry
        </button>
      )}
    </div>
  )
}

export function Empty({ icon, title, children }: { icon?: ReactNode; title: string; children?: ReactNode }) {
  return (
    <div className="empty">
      {icon}
      <h3>{title}</h3>
      {children}
    </div>
  )
}

export function Field({ label, hint, children, error }: { label: string; hint?: ReactNode; error?: string; children: ReactNode }) {
  return (
    <label className="field">
      <span className="field-label">{label}</span>
      {children}
      {hint && <span className="field-hint">{hint}</span>}
      {error && <span className="field-error">{error}</span>}
    </label>
  )
}

/** Multi-select as a list of toggle chips. Empty selection = "all" when allowAllLabel is given. */
export function ChipSelect({
  options,
  value,
  onChange,
  allLabel,
}: {
  options: string[]
  value: string[]
  onChange: (v: string[]) => void
  allLabel?: string
}) {
  return (
    <div className="chips">
      {allLabel && (
        <button type="button" className={`chip ${value.length === 0 ? 'chip-on' : ''}`} aria-pressed={value.length === 0} onClick={() => onChange([])}>
          {allLabel}
        </button>
      )}
      {options.map((o) => {
        const on = value.includes(o)
        return (
          <button
            key={o}
            type="button"
            className={`chip ${on ? 'chip-on' : ''}`}
            aria-pressed={on}
            onClick={() => onChange(on ? value.filter((x) => x !== o) : [...value, o])}
          >
            {o}
          </button>
        )
      })}
    </div>
  )
}

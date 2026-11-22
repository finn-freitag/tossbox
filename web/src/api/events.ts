import { useEffect } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { MOCK } from './client'
import { qk, removeDevice, upsertDevice, upsertNode } from './hooks'
import type { Device, Node } from './types'

/** Subscribes to /api/events and keeps the query cache live. */
export function useLiveEvents(enabled: boolean) {
  const qc = useQueryClient()
  useEffect(() => {
    if (!enabled) return
    const handle = (event: string, data: unknown) => {
      if (event === 'device') upsertDevice(qc, data as Device)
      else if (event === 'device_deleted') {
        removeDevice(qc, (data as { id: string }).id)
        qc.invalidateQueries({ queryKey: qk.me })
      } else if (event === 'node') upsertNode(qc, data as Node)
    }

    if (MOCK) {
      let off: (() => void) | undefined
      let cancelled = false
      import('./mock').then((m) => {
        if (cancelled) return
        const unsub = m.mockSubscribe(handle)
        off = () => void unsub()
      })
      return () => {
        cancelled = true
        off?.()
      }
    }

    let es: EventSource | null = null
    let retry: ReturnType<typeof setTimeout> | undefined
    let opened = false
    const connect = () => {
      es = new EventSource('/api/events', { withCredentials: true })
      for (const ev of ['device', 'device_deleted', 'node']) {
        es.addEventListener(ev, (e) => {
          try {
            handle(ev, JSON.parse((e as MessageEvent).data))
          } catch {
            /* ignore malformed event */
          }
        })
      }
      es.onopen = () => {
        // Re-fetch everything after a reconnect so nothing is missed.
        if (opened) {
          qc.invalidateQueries({ queryKey: qk.devices })
          qc.invalidateQueries({ queryKey: qk.nodes })
        }
        opened = true
      }
      es.onerror = () => {
        es?.close()
        retry = setTimeout(connect, 3000)
      }
    }
    connect()
    return () => {
      clearTimeout(retry)
      es?.close()
    }
  }, [enabled, qc])
}

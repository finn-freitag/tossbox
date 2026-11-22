import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from './client'
import type { Device, Node, PatchDeviceRequest } from './types'

export const qk = {
  info: ['info'] as const,
  me: ['me'] as const,
  devices: ['devices'] as const,
  device: (id: string) => ['devices', id] as const,
  templates: ['templates'] as const,
  createOptions: ['create-options'] as const,
  nodes: ['nodes'] as const,
  users: ['users'] as const,
  settings: ['settings'] as const,
  tokens: ['tokens'] as const,
}

export const useInfo = () => useQuery({ queryKey: qk.info, queryFn: api.info, staleTime: 5 * 60_000 })
export const useMe = () => useQuery({ queryKey: qk.me, queryFn: api.me, retry: false })
export const useDevices = () => useQuery({ queryKey: qk.devices, queryFn: () => api.devices() })
export const useDevice = (id: string) => useQuery({ queryKey: qk.device(id), queryFn: () => api.device(id) })
export const useTemplates = () => useQuery({ queryKey: qk.templates, queryFn: api.templates })
export const useCreateOptions = () => useQuery({ queryKey: qk.createOptions, queryFn: api.createOptions })
export const useNodes = () => useQuery({ queryKey: qk.nodes, queryFn: api.nodes, refetchInterval: 30_000 })
export const useUsers = () => useQuery({ queryKey: qk.users, queryFn: api.users })
export const useSettings = () => useQuery({ queryKey: qk.settings, queryFn: api.settings })
export const useTokens = () => useQuery({ queryKey: qk.tokens, queryFn: api.tokens })

/** Writes a device into both the list and the detail cache. */
export function upsertDevice(qc: ReturnType<typeof useQueryClient>, d: Device) {
  qc.setQueryData<Device[]>(qk.devices, (old) => {
    if (!old) return old
    const i = old.findIndex((x) => x.id === d.id)
    if (i < 0) return [d, ...old]
    const copy = old.slice()
    copy[i] = d
    return copy
  })
  qc.setQueryData(qk.device(d.id), d)
}

export function removeDevice(qc: ReturnType<typeof useQueryClient>, id: string) {
  qc.setQueryData<Device[]>(qk.devices, (old) => old?.filter((x) => x.id !== id))
  qc.removeQueries({ queryKey: qk.device(id) })
}

export function upsertNode(qc: ReturnType<typeof useQueryClient>, n: Node) {
  qc.setQueryData<Node[]>(qk.nodes, (old) => {
    if (!old) return old
    const i = old.findIndex((x) => x.id === n.id)
    if (i < 0) return [...old, n]
    const copy = old.slice()
    copy[i] = n
    return copy
  })
}

export function usePatchDevice() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, req }: { id: string; req: PatchDeviceRequest }) => api.patchDevice(id, req),
    onSuccess: (d) => upsertDevice(qc, d),
  })
}

export function useDeleteDevice() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => api.deleteDevice(id),
    onSuccess: (_, id) => {
      qc.setQueryData<Device[]>(qk.devices, (old) =>
        old?.map((d) => (d.id === id ? { ...d, state: 'destroying' as const } : d)),
      )
      qc.invalidateQueries({ queryKey: qk.me })
    },
  })
}

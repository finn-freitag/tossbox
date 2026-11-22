import type {
  ApiErrorBody,
  ApiToken,
  CreateDeviceRequest,
  CreateOptions,
  CreateUserRequest,
  CreatedToken,
  Device,
  Info,
  Node,
  NodeEnrollment,
  PatchDeviceRequest,
  PatchUserRequest,
  ReloadResult,
  Settings,
  Template,
  User,
} from './types'

export const MOCK = import.meta.env.VITE_MOCK === '1'

export class ApiError extends Error {
  status: number
  code: string
  constructor(status: number, body: ApiErrorBody) {
    super(body.error || `Request failed (${status})`)
    this.status = status
    this.code = body.code || 'internal'
  }
}

type Method = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'

let unauthorizedHandler: (() => void) | null = null
/** Called on any 401 (except the login call itself). */
export function onUnauthorized(fn: () => void) {
  unauthorizedHandler = fn
}

async function request<T>(method: Method, path: string, body?: unknown): Promise<T> {
  if (MOCK) {
    const { mockRequest } = await import('./mock')
    return mockRequest<T>(method, path, body)
  }
  const headers: Record<string, string> = { Accept: 'application/json' }
  if (method !== 'GET') headers['X-Tossbox-CSRF'] = '1'
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  const res = await fetch('/api' + path, {
    method,
    headers,
    credentials: 'same-origin',
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (res.status === 401 && path !== '/auth/login') unauthorizedHandler?.()
  if (!res.ok) {
    let err: ApiErrorBody = { error: res.statusText, code: 'internal' }
    try {
      err = await res.json()
    } catch {
      /* non-JSON error */
    }
    throw new ApiError(res.status, err)
  }
  if (res.status === 204 || res.status === 202) {
    const text = await res.text()
    return (text ? JSON.parse(text) : undefined) as T
  }
  return (await res.json()) as T
}

/** Downloads a file endpoint (rdp, ssh-config) through the browser. */
export function downloadUrl(path: string): string {
  return '/api' + path
}

export const api = {
  info: () => request<Info>('GET', '/info'),

  login: (username: string, password: string) =>
    request<User>('POST', '/auth/login', { username, password }),
  logout: () => request<void>('POST', '/auth/logout'),
  me: () => request<User>('GET', '/auth/me'),
  setSshKeys: (ssh_keys: string[]) => request<User>('PUT', '/me/ssh-keys', { ssh_keys }),
  changePassword: (current_password: string, new_password: string) =>
    request<void>('POST', '/me/password', { current_password, new_password }),
  tokens: () => request<ApiToken[]>('GET', '/me/tokens'),
  createToken: (name: string, expires_in_days: number) =>
    request<CreatedToken>('POST', '/me/tokens', { name, expires_in_days }),
  deleteToken: (id: string) => request<void>('DELETE', `/me/tokens/${id}`),

  templates: () => request<Template[]>('GET', '/templates'),
  reloadTemplates: () => request<ReloadResult>('POST', '/templates/reload'),
  createOptions: () => request<CreateOptions>('GET', '/create-options'),

  devices: (all?: boolean) => request<Device[]>('GET', all === false ? '/devices?all=false' : '/devices'),
  device: (id: string) => request<Device>('GET', `/devices/${id}`),
  createDevice: (req: CreateDeviceRequest) => request<Device>('POST', '/devices', req),
  patchDevice: (id: string, req: PatchDeviceRequest) => request<Device>('PATCH', `/devices/${id}`, req),
  deleteDevice: (id: string) => request<void>('DELETE', `/devices/${id}`),
  guacamole: (id: string, service?: string) =>
    request<{ url: string }>('POST', `/devices/${id}/guacamole`, service ? { service } : {}),

  nodes: () => request<Node[]>('GET', '/nodes'),
  node: (id: string) => request<Node>('GET', `/nodes/${id}`),
  createNode: (name: string, address: string) => request<NodeEnrollment>('POST', '/nodes', { name, address }),
  patchNode: (id: string, req: { name?: string; address?: string }) => request<Node>('PATCH', `/nodes/${id}`, req),
  regenerateNodeToken: (id: string) => request<NodeEnrollment>('POST', `/nodes/${id}/token`),
  deleteNode: (id: string) => request<void>('DELETE', `/nodes/${id}`),

  users: () => request<User[]>('GET', '/users'),
  createUser: (req: CreateUserRequest) => request<User>('POST', '/users', req),
  patchUser: (id: string, req: PatchUserRequest) => request<User>('PATCH', `/users/${id}`, req),
  deleteUser: (id: string) => request<void>('DELETE', `/users/${id}`),

  settings: () => request<Settings>('GET', '/settings'),
  putSettings: (s: Partial<Settings>) => request<Settings>('PUT', '/settings', s),
}

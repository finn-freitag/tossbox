import { useEffect } from 'react'
import { Navigate, Route, Routes, useNavigate } from 'react-router'
import { useQueryClient } from '@tanstack/react-query'
import { onUnauthorized } from './api/client'
import { qk, useMe } from './api/hooks'
import { useLiveEvents } from './api/events'
import { Layout } from './components/Layout'
import { Loading } from './components/ui'
import { LoginPage } from './pages/Login'
import { DevicesPage } from './pages/Devices'
import { CreateDevicePage } from './pages/CreateDevice'
import { DeviceDetailPage } from './pages/DeviceDetail'
import { ProfilePage } from './pages/Profile'
import { NodesPage } from './pages/admin/Nodes'
import { UsersPage } from './pages/admin/Users'
import { TemplatesPage } from './pages/admin/Templates'
import { SettingsPage } from './pages/admin/Settings'

export function App() {
  const qc = useQueryClient()
  const navigate = useNavigate()
  const me = useMe()

  useEffect(() => {
    onUnauthorized(() => {
      qc.setQueryData(qk.me, null)
      qc.removeQueries({ predicate: (q) => q.queryKey[0] !== 'me' && q.queryKey[0] !== 'info' })
      navigate('/login')
    })
  }, [qc, navigate])

  const user = me.data ?? null
  useLiveEvents(!!user)

  if (me.isPending) return <Loading text="Loading Tossbox…" />

  if (!user) {
    return (
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route path="*" element={<Navigate to="/login" replace />} />
      </Routes>
    )
  }

  const admin = user.role === 'admin'
  return (
    <Routes>
      <Route path="/login" element={<Navigate to="/" replace />} />
      <Route element={<Layout user={user} />}>
        <Route index element={<DevicesPage />} />
        <Route path="devices/new" element={<CreateDevicePage />} />
        <Route path="devices/:id" element={<DeviceDetailPage />} />
        <Route path="profile" element={<ProfilePage />} />
        {admin && (
          <>
            <Route path="admin/nodes" element={<NodesPage />} />
            <Route path="admin/users" element={<UsersPage />} />
            <Route path="admin/templates" element={<TemplatesPage />} />
            <Route path="admin/settings" element={<SettingsPage />} />
          </>
        )}
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  )
}

import { useState } from 'react'
import type { ReactNode } from 'react'
import { NavLink, Outlet, useNavigate } from 'react-router'
import { useQueryClient } from '@tanstack/react-query'
import { Boxes, LayoutTemplate, LogOut, Menu, Monitor, Moon, Server, Settings, Sun, SunMoon, User as UserIcon, Users, X } from 'lucide-react'
import { api, MOCK } from '../api/client'
import { qk, useInfo } from '../api/hooks'
import type { User } from '../api/types'
import { useTheme } from '../theme'

export function Layout({ user }: { user: User }) {
  const [theme, cycleTheme] = useTheme()
  const [open, setOpen] = useState(false)
  const info = useInfo()
  const qc = useQueryClient()
  const navigate = useNavigate()

  const logout = async () => {
    try {
      await api.logout()
    } finally {
      qc.setQueryData(qk.me, null)
      qc.clear()
      navigate('/login')
    }
  }

  const link = (to: string, icon: ReactNode, label: string, end = false) => (
    <NavLink to={to} end={end} className={({ isActive }) => `nav-link ${isActive ? 'active' : ''}`} onClick={() => setOpen(false)}>
      {icon}
      <span>{label}</span>
    </NavLink>
  )

  const ThemeIcon = theme === 'light' ? Sun : theme === 'dark' ? Moon : SunMoon

  return (
    <div className={`shell ${open ? 'nav-open' : ''}`}>
      <header className="topbar">
        <button className="icon-btn nav-toggle" aria-label={open ? 'Close menu' : 'Open menu'} onClick={() => setOpen(!open)}>
          {open ? <X size={20} /> : <Menu size={20} />}
        </button>
        <span className="brand">
          <Boxes size={20} /> Tossbox
        </span>
      </header>
      <aside className="sidebar">
        <div className="brand brand-side">
          <Boxes size={22} /> Tossbox
          {MOCK && <span className="badge badge-warn">mock</span>}
        </div>
        <nav className="nav" aria-label="Main">
          {link('/', <Monitor size={18} />, 'Devices', true)}
          {link('/profile', <UserIcon size={18} />, 'Profile')}
          {user.role === 'admin' && (
            <>
              <div className="nav-section">Admin</div>
              {link('/admin/nodes', <Server size={18} />, 'Nodes')}
              {link('/admin/users', <Users size={18} />, 'Users')}
              {link('/admin/templates', <LayoutTemplate size={18} />, 'Templates')}
              {link('/admin/settings', <Settings size={18} />, 'Settings')}
            </>
          )}
        </nav>
        <div className="sidebar-foot">
          <div className="whoami">
            <span className="avatar">{user.username.slice(0, 1).toUpperCase()}</span>
            <span>
              <strong>{user.username}</strong>
              <small className="muted">{user.role}</small>
            </span>
          </div>
          <div className="row gap-xs">
            <button className="icon-btn" onClick={cycleTheme} aria-label={`Theme: ${theme}`} title={`Theme: ${theme}`}>
              <ThemeIcon size={18} />
            </button>
            <button className="icon-btn" onClick={logout} aria-label="Log out" title="Log out">
              <LogOut size={18} />
            </button>
          </div>
          {info.data && <small className="muted version">v{info.data.version}</small>}
        </div>
      </aside>
      <div className="scrim" onClick={() => setOpen(false)} />
      <main className="main">
        <Outlet />
      </main>
    </div>
  )
}

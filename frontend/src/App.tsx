import { NavLink, Route, Routes } from 'react-router-dom'
import DownloadPage from './pages/DownloadPage'
import JobsPage from './pages/JobsPage'
import JobDetailPage from './pages/JobDetailPage'
import SystemPage from './pages/SystemPage'

const nav = [
  { to: '/', label: '地址下载' },
  { to: '/jobs', label: '任务中心' },
  { to: '/system', label: '系统' },
]

export default function App() {
  return (
    <div className="layout">
      <aside className="sidebar">
        <div className="brand">
          <div className="brand-logo">◈</div>
          <div>
            <div className="brand-name">链上分析平台</div>
            <div className="brand-sub">Coverage-aware V1</div>
          </div>
        </div>
        <nav>
          {nav.map((n) => (
            <NavLink key={n.to} to={n.to} end={n.to === '/'}
              className={({ isActive }) => 'nav-item' + (isActive ? ' active' : '')}>
              {n.label}
            </NavLink>
          ))}
        </nav>
        <div className="sidebar-foot">数据下载 · Ankr Provider</div>
      </aside>
      <main className="content">
        <Routes>
          <Route path="/" element={<DownloadPage />} />
          <Route path="/jobs" element={<JobsPage />} />
          <Route path="/jobs/:id" element={<JobDetailPage />} />
          <Route path="/system" element={<SystemPage />} />
        </Routes>
      </main>
    </div>
  )
}

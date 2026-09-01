import { useState } from 'react'
import { NavLink, Route, Routes } from 'react-router-dom'
import DownloadPage from './pages/DownloadPage'
import JobsPage from './pages/JobsPage'
import JobDetailPage from './pages/JobDetailPage'
import SystemPage from './pages/SystemPage'
import BrowsePage from './pages/BrowsePage'
import DataExplorerPage from './pages/DataExplorerPage'
import AddressDetailPage from './pages/AddressDetailPage'
import CombinedDataPage from './pages/CombinedDataPage'
import { AddressIcon, BrowseIcon, DataIcon, OverviewIcon, TasksIcon } from './components/Icons'

const downloadNav = [
  { to: '/', label: '地址下载', Icon: AddressIcon },
  { to: '/jobs', label: '任务中心', Icon: TasksIcon },
  { to: '/system', label: '状态总览', Icon: OverviewIcon },
]

const browseNav = [
  { to: '/data-explorer', label: '数据浏览', Icon: BrowseIcon },
]

function useNavOpen(key: string) {
  const [open, setOpen] = useState(() => localStorage.getItem(key) !== '0')
  const toggle = () =>
    setOpen((o) => {
      const next = !o
      localStorage.setItem(key, next ? '1' : '0')
      return next
    })
  return [open, toggle] as const
}

export default function App() {
  const [dlOpen, toggleDl] = useNavOpen('mainnav-download-open')
  const [browseOpen, toggleBrowse] = useNavOpen('mainnav-browse-open')
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
          <button className="nav-main" onClick={toggleDl} title="点击展开/折叠">
            <span className="nav-icon"><DataIcon /></span>
            <span className="nav-label">数据下载</span>
            <span className="nav-chevron">{dlOpen ? '▾' : '▸'}</span>
          </button>
          {dlOpen && downloadNav.map((n) => (
            <NavLink key={n.to} to={n.to} end={n.to === '/'}
              className={({ isActive }) => 'nav-item sub' + (isActive ? ' active' : '')}>
              <span className="nav-icon"><n.Icon /></span>
              <span className="nav-label">{n.label}</span>
            </NavLink>
          ))}

          <button className="nav-main" onClick={toggleBrowse} title="点击展开/折叠">
            <span className="nav-icon"><BrowseIcon /></span>
            <span className="nav-label">数据浏览</span>
            <span className="nav-chevron">{browseOpen ? '▾' : '▸'}</span>
          </button>
          {browseOpen && browseNav.map((n) => (
            <NavLink key={n.to} to={n.to}
              className={({ isActive }) => 'nav-item sub' + (isActive ? ' active' : '')}>
              <span className="nav-icon"><n.Icon /></span>
              <span className="nav-label">{n.label}</span>
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
          <Route path="/browse/transactions" element={<BrowsePage dataset="transactions" />} />
          <Route path="/browse/token_transfers" element={<BrowsePage dataset="token_transfers" />} />
          <Route path="/browse/nft_transfers" element={<BrowsePage dataset="nft_transfers" />} />
          <Route path="/browse/logs" element={<BrowsePage dataset="logs" />} />
          <Route path="/data-explorer" element={<DataExplorerPage />} />
          <Route path="/data-explorer/combined" element={<CombinedDataPage />} />
          <Route path="/data-explorer/:chain/:address" element={<AddressDetailPage />} />
        </Routes>
      </main>
    </div>
  )
}

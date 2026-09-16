// =============================================================================
// Layout.jsx — Lattice app shell (sidebar + bottom tabs)
// =============================================================================

import React, { useState, useEffect } from 'react';
import { NavLink, Outlet } from 'react-router-dom';
import { useAuth } from './contexts/useAuth.js';
import SetupDismissBanner from './components/SetupDismissBanner.jsx';
import './Layout.css';

// Nav items grouped by section. Operations = day-to-day use, System = admin.
const NAV_GROUPS = [
  {
    label: 'Operations',
    items: [
      { to: '/',          label: 'Dashboard', short: 'Home' },
      { to: '/comms',     label: 'Comms',     short: 'Comms' },
      { to: '/topology',  label: 'Topology',  short: 'Topo' },
      { to: '/gps',       label: 'GPS / GNSS', short: 'GPS' },
      { to: '/blos',      label: 'BLOS',      short: 'BLOS' },
    ],
  },
  {
    label: 'System',
    items: [
      { to: '/settings',  label: 'Settings',  short: 'Config' },
    ],
  },
];

// Bottom tab bar — 4 primary tabs plus a "More" overflow for the rest.
const PRIMARY_TABS = [
  { to: '/',         short: 'Home' },
  { to: '/comms',    short: 'Comms' },
  { to: '/topology', short: 'Topo' },
  { to: '/gps',      short: 'GPS' },
];
const OVERFLOW_TABS = [
  { to: '/blos',     label: 'BLOS' },
  { to: '/settings', label: 'Settings' },
];

export default function Layout() {
  const { logout } = useAuth();
  const [collapsed, setCollapsed] = useState(false);
  const [isMobile, setIsMobile] = useState(() => typeof window !== 'undefined' && window.innerWidth < 768);
  const [sheetOpen, setSheetOpen] = useState(false);

  useEffect(() => {
    let timeoutId = null;
    const onResize = () => {
      if (timeoutId != null) return;
      timeoutId = setTimeout(() => {
        timeoutId = null;
        setIsMobile(window.innerWidth < 768);
      }, 100);
    };
    window.addEventListener('resize', onResize);
    return () => {
      window.removeEventListener('resize', onResize);
      if (timeoutId != null) clearTimeout(timeoutId);
    };
  }, []);

  // Mobile: bottom tab bar
  if (isMobile) {
    return (
      <div className="layout-mobile">
        <div className="layout-content">
          <SetupDismissBanner />
          <Outlet />
        </div>
        {sheetOpen && (
          <>
            <div className="tab-sheet-scrim" onClick={() => setSheetOpen(false)} />
            <nav className="tab-sheet" role="menu">
              {OVERFLOW_TABS.map((item) => (
                <NavLink
                  key={item.to}
                  to={item.to}
                  className={({ isActive }) => 'tab-sheet-item' + (isActive ? ' active' : '')}
                  onClick={() => setSheetOpen(false)}
                >
                  <span className="nav-icon">◇</span>
                  <span>{item.label}</span>
                </NavLink>
              ))}
              <button
                className="tab-sheet-item danger"
                onClick={() => { setSheetOpen(false); logout(); }}
                type="button"
              >
                <span className="nav-icon">×</span>
                <span>Sign Out</span>
              </button>
            </nav>
          </>
        )}
        <nav className="bottom-tab-bar">
          {PRIMARY_TABS.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.to === '/'}
              className={({ isActive }) => 'tab-item' + (isActive ? ' active' : '')}
            >
              <span className="tab-icon">◇</span>
              <span className="tab-label">{item.short}</span>
            </NavLink>
          ))}
          <button
            className={'tab-item' + (sheetOpen ? ' active' : '')}
            onClick={() => setSheetOpen((v) => !v)}
            type="button"
            aria-expanded={sheetOpen}
          >
            <span className="tab-icon">≡</span>
            <span className="tab-label">More</span>
          </button>
        </nav>
      </div>
    );
  }

  // Desktop: collapsible sidebar
  return (
    <div className="layout-desktop">
      <nav className={'sidebar' + (collapsed ? ' collapsed' : '')}>
        <div className="sidebar-header">
          {!collapsed && (
            <div className="sidebar-brand">
              <div className="brand-title">◇ OpenMANET</div>
              <div className="brand-sub">Mesh Terminal</div>
            </div>
          )}
          <button
            className="sidebar-toggle"
            onClick={() => setCollapsed(!collapsed)}
            title={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
            type="button"
          >
            {collapsed ? '▶' : '◀'}
          </button>
        </div>
        <div className="sidebar-nav">
          {NAV_GROUPS.map((group) => (
            <React.Fragment key={group.label}>
              <div className="sidebar-group-label">{group.label}</div>
              {group.items.map((item) => (
                <NavLink
                  key={item.to}
                  to={item.to}
                  end={item.to === '/'}
                  className={({ isActive }) => 'nav-item' + (isActive ? ' active' : '')}
                  title={item.label}
                >
                  <span className="nav-icon" aria-hidden="true" />
                  {!collapsed && <span className="nav-label">{item.label}</span>}
                </NavLink>
              ))}
            </React.Fragment>
          ))}
        </div>
        <div className="sidebar-footer">
          {!collapsed && <div className="operator">Operator</div>}
          <button className="sidebar-logout" onClick={logout} title="Sign out" type="button">
            {collapsed ? '×' : '× Sign Out'}
          </button>
        </div>
      </nav>
      <main className="layout-main">
        <SetupDismissBanner />
        <Outlet />
      </main>
    </div>
  );
}

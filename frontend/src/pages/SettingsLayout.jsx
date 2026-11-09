import React from 'react';
import { NavLink, Outlet, useLocation } from 'react-router-dom';
import './SettingsLayout.css';

const TABS = [
  { to: '/settings', label: 'General', end: true },
  { to: '/settings/bluetooth', label: 'Bluetooth', end: false },
  { to: '/settings/wireless', label: 'Wireless', end: false },
  { to: '/settings/network', label: 'Network', end: false },
  { to: '/settings/firmware', label: 'Firmware', end: false },
  { to: '/settings/terminal', label: 'Terminal', end: false },
  { to: '/settings/logs', label: 'Logs', end: false },
];

export default function SettingsLayout() {
  const { pathname } = useLocation();
  return (
    <div className="settings-layout">
      <div className="settings-tabs" role="tablist">
        {TABS.map((t) => {
          const active = t.end ? pathname === t.to : pathname.startsWith(t.to);
          return (
            <NavLink
              key={t.to}
              to={t.to}
              end={t.end}
              role="tab"
              aria-selected={active}
              className={({ isActive }) => `settings-tab${isActive ? ' active' : ''}`}
            >
              {t.label}
            </NavLink>
          );
        })}
      </div>
      <div className="settings-tab-body" role="tabpanel">
        <Outlet />
      </div>
    </div>
  );
}

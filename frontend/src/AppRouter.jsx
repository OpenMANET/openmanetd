// =============================================================================
// AppRouter.jsx — Route definitions
// =============================================================================
//
// All routes are wrapped in <SetupGate>, which gates on
// GetSetupStatus to decide whether the wizard should be reachable
// and whether a non-/setup request should be redirected to /setup.
// See components/SetupGate.jsx for the gate logic.

import { lazy, Suspense } from 'react';
import { Routes, Route } from 'react-router-dom';
import Layout from './Layout.jsx';
import DashboardPage from './pages/Dashboard.jsx';
import CommsPage from './pages/Comms.jsx';
import SettingsPage from './pages/Settings.jsx';
import SettingsLayout from './pages/SettingsLayout.jsx';
import GpsStatusPage from './pages/GpsStatus.jsx';
import BLOSPage from './pages/BLOS.jsx';
import TopologyPage from './pages/Topology.jsx';
import LoginPage from './pages/LoginPage.jsx';
import ProtectedRoute from './components/ProtectedRoute.jsx';
import SetupGate from './components/SetupGate.jsx';

const SettingsBluetooth = lazy(() => import('./pages/SettingsBluetooth.jsx'));
const SettingsTerminal = lazy(() => import('./pages/SettingsTerminal.jsx'));
const SettingsLogs = lazy(() => import('./pages/SettingsLogs.jsx'));
const SettingsWireless = lazy(() => import('./pages/SettingsWireless.jsx'));
const SettingsNetwork = lazy(() => import('./pages/SettingsNetwork.jsx'));
const SettingsFirmware = lazy(() => import('./pages/SettingsFirmware.jsx'));
const SetupWizard = lazy(() => import('./pages/SetupWizard.jsx'));

export default function AppRouter() {
  return (
    <Suspense fallback={<div className="lat-panel">Loading…</div>}>
      <SetupGate>
        <Routes>
          <Route path="/login" element={<LoginPage />} />
          <Route path="/setup/*" element={<SetupWizard />} />
          <Route element={<ProtectedRoute><Layout /></ProtectedRoute>}>
            <Route path="/" element={<DashboardPage />} />
            <Route path="/comms" element={<CommsPage />} />
            <Route path="/topology" element={<TopologyPage />} />
            <Route path="/gps" element={<GpsStatusPage />} />
            <Route path="/blos" element={<BLOSPage />} />
            <Route path="/settings" element={<SettingsLayout />}>
              <Route index element={<SettingsPage />} />
              <Route path="bluetooth" element={<SettingsBluetooth />} />
              <Route path="wireless" element={<SettingsWireless />} />
              <Route path="network" element={<SettingsNetwork />} />
              <Route path="firmware" element={<SettingsFirmware />} />
              <Route path="terminal" element={<SettingsTerminal />} />
              <Route path="logs" element={<SettingsLogs />} />
            </Route>
          </Route>
        </Routes>
      </SetupGate>
    </Suspense>
  );
}

import { BrowserRouter, Routes, Route } from 'react-router-dom'
import { Sidebar } from '@/components/layout/Sidebar'
import { DashboardPage } from '@/pages/DashboardPage'
import { InstancesPage } from '@/pages/InstancesPage'
import { LogsPage } from '@/pages/LogsPage'
import { SettingsPage } from '@/pages/SettingsPage'
import { Toaster } from 'sonner'

function App() {
  return (
    <BrowserRouter>
      <div className="flex min-h-screen">
        <Sidebar />
        <main className="flex-1 ml-[240px] transition-all duration-200">
          <Routes>
            <Route path="/" element={<DashboardPage />} />
            <Route path="/instances" element={<InstancesPage />} />
            <Route path="/logs" element={<LogsPage />} />
            <Route path="/settings" element={<SettingsPage />} />
          </Routes>
        </main>
      </div>
      <Toaster position="top-right" theme="dark" />
    </BrowserRouter>
  )
}

export default App

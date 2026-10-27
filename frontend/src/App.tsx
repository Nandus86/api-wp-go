import { BrowserRouter, Routes, Route } from 'react-router-dom'
import { Loader2 } from 'lucide-react'
import { Sidebar } from '@/components/layout/Sidebar'
import { DashboardPage } from '@/pages/DashboardPage'
import { InstancesPage } from '@/pages/InstancesPage'
import { LogsPage } from '@/pages/LogsPage'
import { SettingsPage } from '@/pages/SettingsPage'
import { LoginPage } from '@/pages/LoginPage'
import { ApiKeysPage } from '@/pages/ApiKeysPage'
import { WebhooksPage } from '@/pages/WebhooksPage'
import { AuditPage } from '@/pages/AuditPage'
import { MessagesPage } from '@/pages/MessagesPage'
import { SessionProvider } from '@/lib/session'
import { useSession } from '@/lib/session-context'
import { Toaster } from 'sonner'

function Shell() {
  const { me, loading } = useSession()

  if (loading) {
    return (
      <div className="min-h-screen flex items-center justify-center text-muted-foreground">
        <Loader2 className="animate-spin" />
      </div>
    )
  }
  if (!me) return <LoginPage />

  return (
    <div className="flex min-h-screen">
      <Sidebar />
      <main className="flex-1 ml-[240px] transition-all duration-200">
        <Routes>
          <Route path="/" element={<DashboardPage />} />
          <Route path="/instances" element={<InstancesPage />} />
          <Route path="/messages" element={<MessagesPage />} />
          <Route path="/webhooks" element={<WebhooksPage />} />
          <Route path="/keys" element={<ApiKeysPage />} />
          <Route path="/audit" element={<AuditPage />} />
          <Route path="/logs" element={<LogsPage />} />
          <Route path="/settings" element={<SettingsPage />} />
        </Routes>
      </main>
    </div>
  )
}

function App() {
  return (
    <BrowserRouter>
      <SessionProvider>
        <Shell />
      </SessionProvider>
      <Toaster position="top-right" theme="dark" />
    </BrowserRouter>
  )
}

export default App

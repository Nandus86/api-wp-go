import { useState } from 'react'
import { KeyRound, Loader2 } from 'lucide-react'
import { useSession } from '@/lib/session-context'

export function LoginPage() {
    const { login } = useSession()
    const [key, setKey] = useState('')
    const [error, setError] = useState<string | null>(null)
    const [busy, setBusy] = useState(false)

    const submit = async (e: React.FormEvent) => {
        e.preventDefault()
        if (!key.trim()) return
        setBusy(true)
        setError(null)
        try {
            await login(key)
        } catch {
            setError('Invalid or expired API key.')
        } finally {
            setBusy(false)
        }
    }

    return (
        <div className="min-h-screen flex items-center justify-center p-4">
            <form onSubmit={submit} className="w-full max-w-sm bg-card border border-border rounded-xl p-6 space-y-5 shadow-lg">
                <div className="flex items-center gap-3">
                    <div className="w-10 h-10 rounded-md bg-primary flex items-center justify-center text-white font-bold text-sm">WB</div>
                    <div>
                        <h1 className="text-lg font-semibold text-foreground">WhatsMeow Basileia</h1>
                        <p className="text-xs text-muted-foreground">Sign in with an API key</p>
                    </div>
                </div>

                <div className="space-y-2">
                    <label htmlFor="api-key" className="text-sm font-medium text-foreground">API key</label>
                    <div className="relative">
                        <KeyRound size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-muted-foreground" />
                        <input
                            id="api-key"
                            type="password"
                            autoFocus
                            autoComplete="current-password"
                            value={key}
                            onChange={e => setKey(e.target.value)}
                            placeholder="wmb_..."
                            className="w-full pl-9 pr-3 py-2 bg-background border border-border rounded-md text-sm font-mono focus:outline-none focus:ring-2 focus:ring-ring"
                        />
                    </div>
                    <p className="text-xs text-muted-foreground">
                        The first admin key is printed once in the server log and saved to <code className="font-mono">DATA_DIR/.api-key</code>.
                        Instance tokens also work and give access to that instance only.
                    </p>
                </div>

                {error && <p className="text-sm text-destructive" role="alert">{error}</p>}

                <button
                    type="submit"
                    disabled={busy || !key.trim()}
                    className="w-full flex items-center justify-center gap-2 py-2 bg-primary text-primary-foreground rounded-md text-sm font-medium hover:bg-primary/90 disabled:opacity-50 transition-colors"
                >
                    {busy && <Loader2 size={16} className="animate-spin" />}
                    Sign in
                </button>
            </form>
        </div>
    )
}

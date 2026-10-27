import { useCallback, useEffect, useState, type ReactNode } from 'react'
import type { Me, Role } from '@/types'
import { fetchMe } from '@/lib/api'
import { clearApiKey, setApiKey, UNAUTHORIZED_EVENT } from '@/lib/auth'
import { SessionContext } from '@/lib/session-context'

const rank: Record<string, number> = { viewer: 1, operator: 2, instance: 2, admin: 3 }

export function SessionProvider({ children }: { children: ReactNode }) {
    const [me, setMe] = useState<Me | null>(null)
    const [loading, setLoading] = useState(true)

    const refresh = useCallback(async () => {
        try {
            setMe(await fetchMe())
        } catch {
            setMe(null)
        } finally {
            setLoading(false)
        }
    }, [])

    useEffect(() => {
        refresh()
        const onUnauthorized = () => setMe(null)
        window.addEventListener(UNAUTHORIZED_EVENT, onUnauthorized)
        return () => window.removeEventListener(UNAUTHORIZED_EVENT, onUnauthorized)
    }, [refresh])

    const login = async (key: string) => {
        setApiKey(key.trim())
        const result = await fetchMe()
        setMe(result)
    }

    const logout = () => {
        clearApiKey()
        setMe(null)
    }

    const can = (role: Exclude<Role, 'instance'>) => !!me && rank[me.principal.role] >= rank[role]

    return <SessionContext.Provider value={{ me, loading, login, logout, can }}>{children}</SessionContext.Provider>
}

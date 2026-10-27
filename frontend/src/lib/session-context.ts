import { createContext, useContext } from 'react'
import type { Me, Role } from '@/types'

export interface Session {
    me: Me | null
    loading: boolean
    login: (key: string) => Promise<void>
    logout: () => void
    can: (role: Exclude<Role, 'instance'>) => boolean
}

export const SessionContext = createContext<Session | null>(null)

export function useSession(): Session {
    const ctx = useContext(SessionContext)
    if (!ctx) throw new Error('useSession must be used inside SessionProvider')
    return ctx
}

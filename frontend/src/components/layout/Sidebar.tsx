import { NavLink } from 'react-router-dom'
import { LayoutDashboard, Smartphone, ScrollText, Settings, MessageSquare, Webhook, KeyRound, ShieldCheck, LogOut } from 'lucide-react'
import { cn } from '@/lib/utils'
import { useState } from 'react'
import { useSession } from '@/lib/session-context'
import type { Role } from '@/types'

const navItems: { to: string; icon: typeof LayoutDashboard; label: string; role?: Exclude<Role, 'instance'>; global?: boolean }[] = [
    { to: '/', icon: LayoutDashboard, label: 'Dashboard' },
    { to: '/instances', icon: Smartphone, label: 'Instances' },
    { to: '/messages', icon: MessageSquare, label: 'Messages' },
    { to: '/webhooks', icon: Webhook, label: 'Webhooks' },
    { to: '/keys', icon: KeyRound, label: 'API Keys', role: 'admin', global: true },
    { to: '/audit', icon: ShieldCheck, label: 'Audit', role: 'admin' },
    { to: '/logs', icon: ScrollText, label: 'Logs', role: 'admin' },
    { to: '/settings', icon: Settings, label: 'Settings' },
]

export function Sidebar() {
    const [collapsed, setCollapsed] = useState(false)
    const { me, can, logout } = useSession()
    const scoped = !!me && (me.principal.kind === 'instance' || (me.principal.allowed_instances?.length ?? 0) > 0)

    return (
        <aside
            className={cn(
                'fixed left-0 top-0 h-screen bg-sidebar border-r border-sidebar-border flex flex-col transition-all duration-200 z-50',
                collapsed ? 'w-[60px]' : 'w-[240px]'
            )}
        >
            {/* Logo area */}
            <div className="flex items-center gap-3 px-4 h-14 border-b border-sidebar-border">
                <div className="w-8 h-8 rounded-md bg-primary flex items-center justify-center text-white font-bold text-sm shrink-0">
                    WB
                </div>
                {!collapsed && (
                    <span className="text-sm font-semibold text-sidebar-foreground truncate">
                        WhatsMeow Basileia
                    </span>
                )}
            </div>

            {/* Navigation */}
            <nav className="flex-1 py-4 flex flex-col gap-1 px-2">
                {navItems
                    .filter(item => (!item.role || can(item.role)) && (!item.global || !scoped))
                    .map(({ to, icon: Icon, label }) => (
                        <NavLink
                            key={to}
                            to={to}
                            className={({ isActive }) =>
                                cn(
                                    'flex items-center gap-3 px-3 py-2.5 rounded-md text-sm font-medium transition-colors',
                                    isActive
                                        ? 'bg-sidebar-accent text-primary'
                                        : 'text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground'
                                )
                            }
                        >
                            <Icon size={20} className="shrink-0" />
                            {!collapsed && <span>{label}</span>}
                        </NavLink>
                    ))}
            </nav>

            {/* Current credential */}
            {me && (
                <div className="px-3 py-3 border-t border-sidebar-border flex items-center gap-2">
                    {!collapsed && (
                        <div className="min-w-0 flex-1">
                            <p className="text-xs font-medium text-sidebar-foreground truncate">{me.principal.name}</p>
                            <p className="text-[10px] uppercase tracking-wider text-muted-foreground">{me.principal.role}</p>
                        </div>
                    )}
                    {me.auth_required && (
                        <button onClick={logout} title="Sign out" aria-label="Sign out" className="p-1.5 rounded-md text-muted-foreground hover:text-foreground hover:bg-sidebar-accent">
                            <LogOut size={16} />
                        </button>
                    )}
                </div>
            )}

            {/* Collapse toggle */}
            <button
                onClick={() => setCollapsed(!collapsed)}
                className="px-4 py-3 border-t border-sidebar-border text-muted-foreground hover:text-foreground text-xs transition-colors"
            >
                {collapsed ? '→' : '← Collapse'}
            </button>
        </aside>
    )
}

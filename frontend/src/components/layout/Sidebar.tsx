import { NavLink } from 'react-router-dom'
import { LayoutDashboard, Smartphone, ScrollText, Settings } from 'lucide-react'
import { cn } from '@/lib/utils'
import { useState } from 'react'

const navItems = [
    { to: '/', icon: LayoutDashboard, label: 'Dashboard' },
    { to: '/instances', icon: Smartphone, label: 'Instances' },
    { to: '/logs', icon: ScrollText, label: 'Logs' },
    { to: '/settings', icon: Settings, label: 'Settings' },
]

export function Sidebar() {
    const [collapsed, setCollapsed] = useState(false)

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
                {navItems.map(({ to, icon: Icon, label }) => (
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

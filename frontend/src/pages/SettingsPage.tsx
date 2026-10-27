import { useEffect, useState } from 'react'
import { Save, LogOut, RefreshCw } from 'lucide-react'
import { toast } from 'sonner'
import { fetchInstances, fetchInstanceSettings, fetchReady, saveInstanceSettings } from '@/lib/api'
import type { Instance, InstanceSettings, ReadyStatus } from '@/types'
import { useSession } from '@/lib/session-context'
import { cn } from '@/lib/utils'

const toggles: { key: keyof InstanceSettings; label: string; help: string }[] = [
    { key: 'reject_calls', label: 'Reject calls', help: 'Automatically decline incoming voice/video calls.' },
    { key: 'always_online', label: 'Always online', help: 'Keep the account shown as online.' },
    { key: 'read_messages', label: 'Auto read', help: 'Mark incoming messages as read (blue ticks).' },
    { key: 'ignore_groups', label: 'Ignore groups in webhooks', help: 'Do not send group messages to webhooks.' },
    { key: 'ignore_status', label: 'Ignore status in webhooks', help: 'Do not send status (stories) updates to webhooks.' },
]

export function SettingsPage() {
    const { me, can, logout } = useSession()
    const [ready, setReady] = useState<ReadyStatus | null>(null)
    const [instances, setInstances] = useState<Instance[]>([])
    const [instance, setInstance] = useState('')
    const [settings, setSettings] = useState<InstanceSettings | null>(null)
    const [saving, setSaving] = useState(false)

    const loadReady = () => fetchReady().then(setReady).catch(() => setReady(null))

    useEffect(() => {
        loadReady()
        fetchInstances().then(list => {
            setInstances(list)
            if (list.length > 0) setInstance(list[0].id)
        }).catch(() => undefined)
    }, [])

    useEffect(() => {
        if (!instance) return
        fetchInstanceSettings(instance).then(setSettings).catch(e => toast.error((e as Error).message))
    }, [instance])

    const save = async () => {
        if (!settings || !instance) return
        setSaving(true)
        try {
            setSettings(await saveInstanceSettings(instance, settings))
            toast.success('Settings saved')
        } catch (e) {
            toast.error((e as Error).message)
        } finally {
            setSaving(false)
        }
    }

    return (
        <div className="p-6 space-y-8 max-w-4xl mx-auto">
            <div>
                <h1 className="text-2xl font-semibold text-foreground">Settings</h1>
                <p className="text-sm text-muted-foreground mt-1">System health, your credential and per-instance behaviour.</p>
            </div>

            {/* Health */}
            <section className="bg-card border border-border rounded-xl overflow-hidden">
                <div className="px-6 py-4 border-b border-border bg-muted/20 flex items-center justify-between">
                    <h2 className="text-base font-semibold text-foreground">System health</h2>
                    <button onClick={loadReady} className="p-1.5 rounded-md hover:bg-muted text-muted-foreground" aria-label="Refresh health"><RefreshCw size={14} /></button>
                </div>
                <div className="p-6 grid grid-cols-2 md:grid-cols-4 gap-4">
                    {ready ? Object.entries(ready.components).map(([name, c]) => (
                        <div key={name} className="space-y-1">
                            <p className="text-xs uppercase tracking-wider text-muted-foreground">{name}</p>
                            <p className={cn('text-sm font-semibold',
                                c.status === 'ok' ? 'text-success' : c.status === 'disabled' ? 'text-muted-foreground' : 'text-destructive')}>
                                {c.status}{c.latency ? ` · ${c.latency}` : ''}
                            </p>
                            {c.error && <p className="text-xs text-destructive break-all">{c.error}</p>}
                        </div>
                    )) : <p className="text-sm text-muted-foreground">Unavailable</p>}
                </div>
                {ready && (
                    <p className="px-6 pb-5 text-xs text-muted-foreground">
                        {ready.instances.connected} of {ready.instances.total} instances connected · version {me?.version}
                    </p>
                )}
            </section>

            {/* Credential */}
            {me && (
                <section className="bg-card border border-border rounded-xl p-6 flex items-center justify-between gap-4">
                    <div>
                        <p className="text-sm font-medium text-foreground">{me.principal.name}</p>
                        <p className="text-xs text-muted-foreground mt-1">
                            Role <strong>{me.principal.role}</strong>
                            {me.principal.allowed_instances?.length ? ` · limited to ${me.principal.allowed_instances.length} instance(s)` : ''}
                            {me.principal.kind === 'instance' ? ' · instance token' : ''}
                            {!me.auth_required && ' · authentication disabled (AUTH_REQUIRED=false)'}
                        </p>
                    </div>
                    {me.auth_required && (
                        <button onClick={logout} className="flex items-center gap-2 px-4 py-2 text-sm rounded-md border border-border hover:bg-muted">
                            <LogOut size={14} /> Sign out
                        </button>
                    )}
                </section>
            )}

            {/* Instance behaviour */}
            {instances.length > 0 && settings && (
                <section className="bg-card border border-border rounded-xl overflow-hidden">
                    <div className="px-6 py-4 border-b border-border bg-muted/20 flex items-center justify-between gap-4">
                        <h2 className="text-base font-semibold text-foreground">Instance behaviour</h2>
                        <select value={instance} onChange={e => setInstance(e.target.value)} aria-label="Instance"
                            className="px-3 py-1.5 bg-background border border-border rounded-md text-sm">
                            {instances.map(i => <option key={i.id} value={i.id}>{i.name}</option>)}
                        </select>
                    </div>
                    <div className="p-6 space-y-5">
                        {toggles.map(t => (
                            <label key={t.key} className="flex items-start justify-between gap-6 cursor-pointer">
                                <span>
                                    <span className="block text-sm font-medium text-foreground">{t.label}</span>
                                    <span className="block text-xs text-muted-foreground mt-0.5">{t.help}</span>
                                </span>
                                <input type="checkbox" className="mt-1 h-4 w-4 accent-[var(--color-primary)]"
                                    checked={!!settings[t.key]} disabled={!can('operator')}
                                    onChange={e => setSettings({ ...settings, [t.key]: e.target.checked })} />
                            </label>
                        ))}
                        {settings.reject_calls && (
                            <input value={settings.reject_call_message || ''} onChange={e => setSettings({ ...settings, reject_call_message: e.target.value })}
                                placeholder="Optional message sent after rejecting a call" aria-label="Reject call message"
                                className="w-full px-3 py-2 bg-background border border-border rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-ring" />
                        )}
                        <div className="grid grid-cols-2 gap-4">
                            <label className="space-y-1.5">
                                <span className="block text-sm font-medium">Min delay between sends (ms)</span>
                                <input type="number" min={0} max={120000} value={settings.msg_delay_min}
                                    onChange={e => setSettings({ ...settings, msg_delay_min: Number(e.target.value) })}
                                    className="w-full px-3 py-2 bg-background border border-border rounded-md text-sm" />
                            </label>
                            <label className="space-y-1.5">
                                <span className="block text-sm font-medium">Max delay between sends (ms)</span>
                                <input type="number" min={0} max={120000} value={settings.msg_delay_max}
                                    onChange={e => setSettings({ ...settings, msg_delay_max: Number(e.target.value) })}
                                    className="w-full px-3 py-2 bg-background border border-border rounded-md text-sm" />
                            </label>
                        </div>
                        {can('operator') && (
                            <div className="flex justify-end">
                                <button onClick={save} disabled={saving}
                                    className="flex items-center gap-2 px-6 py-2.5 bg-primary text-primary-foreground rounded-md text-sm font-medium hover:bg-primary/90 disabled:opacity-50">
                                    <Save size={16} /> Save
                                </button>
                            </div>
                        )}
                    </div>
                </section>
            )}
        </div>
    )
}

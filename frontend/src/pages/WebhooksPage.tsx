import { useEffect, useState } from 'react'
import { Plus, RefreshCw, Send, Trash2, RotateCcw, Eye, X } from 'lucide-react'
import { toast } from 'sonner'
import {
    createWebhook, deleteWebhook, fetchDeliveries, fetchDelivery, fetchDeliveryStats, fetchInstances, fetchWebhooks,
    redriveDelivery, redriveFailed, testWebhook, updateWebhookConfig,
} from '@/lib/api'
import type { Delivery, Instance, Webhook } from '@/types'
import { useSession } from '@/lib/session-context'
import { cn } from '@/lib/utils'

const EVENTS = ['messages', 'receipts.update', 'presence.update', 'groups.update', 'calls.offer', 'connection.update']
const STATUSES = ['', 'failed', 'retrying', 'pending', 'delivered']

const statusStyles: Record<string, string> = {
    delivered: 'bg-success/10 text-success',
    failed: 'bg-destructive/10 text-destructive',
    retrying: 'bg-warning/10 text-warning',
    pending: 'bg-muted text-muted-foreground',
    delivering: 'bg-primary/10 text-primary',
}

export function WebhooksPage() {
    const { me, can } = useSession()
    const isInstanceToken = me?.principal.kind === 'instance'
    const [instances, setInstances] = useState<Instance[]>([])
    const [instance, setInstance] = useState(isInstanceToken ? me!.principal.instance_id! : '')
    const [hooks, setHooks] = useState<Webhook[]>([])
    const [deliveries, setDeliveries] = useState<Delivery[]>([])
    const [total, setTotal] = useState(0)
    const [status, setStatus] = useState('failed')
    const [stats, setStats] = useState<Record<string, number>>({})
    const [showCreate, setShowCreate] = useState(false)
    const [url, setUrl] = useState('')
    const [secret, setSecret] = useState('')
    const [events, setEvents] = useState<string[]>([])
    const [detail, setDetail] = useState<Delivery | null>(null)

    const scope = instance || undefined

    const load = async () => {
        try {
            const [h, d, s] = await Promise.all([
                fetchWebhooks(scope),
                fetchDeliveries({ instance: scope, status: status || undefined, limit: 50 }),
                fetchDeliveryStats(scope),
            ])
            setHooks(h)
            setDeliveries(d.items)
            setTotal(d.total)
            setStats(s)
        } catch (e) {
            toast.error((e as Error).message)
        }
    }

    useEffect(() => {
        fetchInstances().then(setInstances).catch(() => undefined)
    }, [])

    useEffect(() => {
        load()
        const t = setInterval(load, 10000)
        return () => clearInterval(t)
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [instance, status])

    const create = async () => {
        try {
            await createWebhook({ instance_id: instance || null, url, events, secret: secret || undefined, enabled: true })
            toast.success('Webhook created')
            setUrl('')
            setSecret('')
            setEvents([])
            setShowCreate(false)
            load()
        } catch (e) {
            toast.error((e as Error).message)
        }
    }

    const run = async (fn: () => Promise<unknown>, ok: string) => {
        try {
            await fn()
            toast.success(ok)
            load()
        } catch (e) {
            toast.error((e as Error).message)
        }
    }

    const instanceName = (id?: string | null) => (id ? instances.find(i => i.id === id)?.name || id : 'Global')

    return (
        <div className="p-6 space-y-6 max-w-6xl mx-auto">
            <div className="flex flex-wrap items-center justify-between gap-4">
                <div>
                    <h1 className="text-2xl font-semibold text-foreground">Webhooks</h1>
                    <p className="text-sm text-muted-foreground mt-1">
                        Deliveries are stored, retried with backoff and signed (X-Webhook-Signature) when a secret is set.
                    </p>
                </div>
                {!isInstanceToken && (
                    <select value={instance} onChange={e => setInstance(e.target.value)} aria-label="Instance"
                        className="px-3 py-2 bg-background border border-border rounded-md text-sm">
                        <option value="">All instances</option>
                        {instances.map(i => <option key={i.id} value={i.id}>{i.name}</option>)}
                    </select>
                )}
            </div>

            <div className="grid grid-cols-2 md:grid-cols-5 gap-3">
                {['delivered', 'pending', 'retrying', 'delivering', 'failed'].map(s => (
                    <button key={s} onClick={() => setStatus(s === status ? '' : s)}
                        className={cn('bg-card border rounded-xl p-4 text-left transition-colors', status === s ? 'border-primary' : 'border-border hover:border-muted-foreground/40')}>
                        <p className="text-xs uppercase tracking-wider text-muted-foreground">{s}</p>
                        <p className="text-2xl font-semibold text-foreground mt-1">{stats[s] ?? 0}</p>
                    </button>
                ))}
            </div>

            {/* Registered webhooks */}
            <section className="bg-card border border-border rounded-xl overflow-hidden">
                <div className="px-5 py-3 border-b border-border bg-muted/20 flex items-center justify-between">
                    <h2 className="text-sm font-semibold">Registered webhooks</h2>
                    {can('operator') && (
                        <button onClick={() => setShowCreate(!showCreate)} className="flex items-center gap-1.5 text-xs px-3 py-1.5 rounded-md bg-secondary hover:bg-secondary/80">
                            <Plus size={14} /> Add
                        </button>
                    )}
                </div>
                {showCreate && (
                    <div className="p-5 border-b border-border space-y-3">
                        <p className="text-xs text-muted-foreground">
                            Target: <strong className="text-foreground">{instance ? instanceName(instance) : 'Global (every instance, admin only)'}</strong>
                        </p>
                        <input value={url} onChange={e => setUrl(e.target.value)} placeholder="https://n8n.example.com/webhook/..." aria-label="URL"
                            className="w-full px-3 py-2 bg-background border border-border rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-ring" />
                        <input value={secret} onChange={e => setSecret(e.target.value)} placeholder="HMAC secret (optional)" aria-label="Secret"
                            className="w-full px-3 py-2 bg-background border border-border rounded-md text-sm font-mono focus:outline-none focus:ring-2 focus:ring-ring" />
                        <div className="flex flex-wrap gap-2">
                            {EVENTS.map(ev => {
                                const on = events.includes(ev)
                                return (
                                    <button key={ev} type="button" onClick={() => setEvents(on ? events.filter(e => e !== ev) : [...events, ev])}
                                        className={cn('px-3 py-1 rounded-full text-xs border', on ? 'bg-primary/15 border-primary text-primary' : 'border-border text-muted-foreground')}>
                                        {ev}
                                    </button>
                                )
                            })}
                            <span className="text-xs text-muted-foreground self-center">(none = all events)</span>
                        </div>
                        <div className="flex justify-end gap-2">
                            <button onClick={() => setShowCreate(false)} className="px-4 py-2 text-sm rounded-md hover:bg-muted">Cancel</button>
                            <button onClick={create} disabled={!url.trim()} className="px-4 py-2 text-sm rounded-md bg-primary text-primary-foreground disabled:opacity-50">Create</button>
                        </div>
                    </div>
                )}
                <div className="divide-y divide-border">
                    {hooks.length === 0 && (
                        <p className="px-5 py-6 text-sm text-muted-foreground">
                            No registered webhooks. The instance webhook URL (Instances → Edit) and WEBHOOK_URL still receive every event.
                        </p>
                    )}
                    {hooks.map(h => (
                        <div key={h.id} className="px-5 py-3 flex items-center gap-4">
                            <div className="min-w-0 flex-1">
                                <p className={cn('text-sm font-mono truncate', !h.enabled && 'line-through text-muted-foreground')}>{h.url}</p>
                                <p className="text-xs text-muted-foreground mt-0.5">
                                    {instanceName(h.instance_id)} · {h.events.length ? h.events.join(', ') : 'all events'} · {h.has_secret ? 'signed' : 'unsigned'}
                                </p>
                            </div>
                            {can('operator') && (
                                <div className="flex gap-1">
                                    <button onClick={() => run(() => testWebhook(h.id), 'Test event queued')} title="Send test" aria-label="Send test event" className="p-2 rounded-md hover:bg-muted text-muted-foreground"><Send size={15} /></button>
                                    <button onClick={() => run(() => updateWebhookConfig(h.id, { enabled: !h.enabled }), h.enabled ? 'Disabled' : 'Enabled')}
                                        className="px-2 text-xs rounded-md hover:bg-muted text-muted-foreground">{h.enabled ? 'Disable' : 'Enable'}</button>
                                    <button onClick={() => confirm('Delete this webhook?') && run(() => deleteWebhook(h.id), 'Webhook deleted')} title="Delete" aria-label="Delete webhook"
                                        className="p-2 rounded-md hover:bg-destructive/10 text-muted-foreground hover:text-destructive"><Trash2 size={15} /></button>
                                </div>
                            )}
                        </div>
                    ))}
                </div>
            </section>

            {/* Deliveries */}
            <section className="bg-card border border-border rounded-xl overflow-hidden">
                <div className="px-5 py-3 border-b border-border bg-muted/20 flex flex-wrap items-center justify-between gap-2">
                    <div className="flex items-center gap-3">
                        <h2 className="text-sm font-semibold">Deliveries</h2>
                        <select value={status} onChange={e => setStatus(e.target.value)} aria-label="Status filter"
                            className="px-2 py-1 bg-background border border-border rounded-md text-xs">
                            {STATUSES.map(s => <option key={s} value={s}>{s || 'all'}</option>)}
                        </select>
                        <span className="text-xs text-muted-foreground">{total} total</span>
                    </div>
                    <div className="flex gap-2">
                        {can('operator') && (stats.failed ?? 0) > 0 && (
                            <button onClick={() => run(() => redriveFailed(scope), 'Failed deliveries queued again')}
                                className="flex items-center gap-1.5 text-xs px-3 py-1.5 rounded-md bg-primary text-primary-foreground">
                                <RotateCcw size={14} /> Redrive all failed
                            </button>
                        )}
                        <button onClick={load} className="p-1.5 rounded-md hover:bg-muted text-muted-foreground" aria-label="Refresh"><RefreshCw size={14} /></button>
                    </div>
                </div>
                <table className="w-full text-sm">
                    <thead className="text-xs text-muted-foreground uppercase tracking-wider">
                        <tr>
                            <th className="text-left px-4 py-2 font-medium">#</th>
                            <th className="text-left px-4 py-2 font-medium">Event</th>
                            <th className="text-left px-4 py-2 font-medium">Target</th>
                            <th className="text-left px-4 py-2 font-medium">Status</th>
                            <th className="text-left px-4 py-2 font-medium">Attempts</th>
                            <th className="text-left px-4 py-2 font-medium">Last error</th>
                            <th className="px-4 py-2" />
                        </tr>
                    </thead>
                    <tbody className="divide-y divide-border">
                        {deliveries.length === 0 && <tr><td colSpan={7} className="px-4 py-8 text-center text-muted-foreground">Nothing here</td></tr>}
                        {deliveries.map(d => (
                            <tr key={d.id}>
                                <td className="px-4 py-2 text-muted-foreground">{d.id}</td>
                                <td className="px-4 py-2 font-mono text-xs">{d.event}</td>
                                <td className="px-4 py-2 font-mono text-xs max-w-[260px] truncate" title={d.url}>{d.url}</td>
                                <td className="px-4 py-2"><span className={cn('px-2 py-0.5 rounded-full text-[11px] font-semibold', statusStyles[d.status])}>{d.status}</span></td>
                                <td className="px-4 py-2 text-muted-foreground">{d.attempts}/{d.max_attempts}</td>
                                <td className="px-4 py-2 text-xs text-muted-foreground max-w-[220px] truncate" title={d.last_error}>{d.last_error || '—'}</td>
                                <td className="px-4 py-2">
                                    <div className="flex justify-end gap-1">
                                        <button onClick={async () => setDetail(await fetchDelivery(d.id))} aria-label="View payload" className="p-1.5 rounded-md hover:bg-muted text-muted-foreground"><Eye size={14} /></button>
                                        {can('operator') && (d.status === 'failed' || d.status === 'delivered') && (
                                            <button onClick={() => run(() => redriveDelivery(d.id), 'Delivery queued again')} aria-label="Redrive" className="p-1.5 rounded-md hover:bg-muted text-muted-foreground"><RotateCcw size={14} /></button>
                                        )}
                                    </div>
                                </td>
                            </tr>
                        ))}
                    </tbody>
                </table>
            </section>

            {detail && (
                <div className="fixed inset-0 bg-black/60 flex items-center justify-center z-[60] p-4" onClick={() => setDetail(null)}>
                    <div className="bg-card border border-border rounded-xl w-full max-w-3xl max-h-[80vh] flex flex-col" onClick={e => e.stopPropagation()}>
                        <div className="flex items-center justify-between px-5 py-3 border-b border-border">
                            <h3 className="text-sm font-semibold">Delivery #{detail.id} · {detail.event}</h3>
                            <button onClick={() => setDetail(null)} aria-label="Close" className="p-1 rounded-md hover:bg-muted"><X size={16} /></button>
                        </div>
                        <pre className="p-5 text-xs font-mono overflow-auto custom-scrollbar whitespace-pre-wrap break-all">
                            {(() => { try { return JSON.stringify(JSON.parse(detail.body || '{}'), null, 2) } catch { return detail.body } })()}
                        </pre>
                    </div>
                </div>
            )}
        </div>
    )
}

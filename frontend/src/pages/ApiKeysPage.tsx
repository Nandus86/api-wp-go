import { useEffect, useState } from 'react'
import { Copy, KeyRound, Plus, Trash2, Ban, RotateCcw, X } from 'lucide-react'
import { toast } from 'sonner'
import { createApiKey, deleteApiKey, fetchApiKeys, fetchInstances, updateApiKey } from '@/lib/api'
import type { ApiKey, Instance } from '@/types'
import { cn } from '@/lib/utils'

const roleStyles: Record<string, string> = {
    admin: 'bg-destructive/10 text-destructive',
    operator: 'bg-primary/10 text-primary',
    viewer: 'bg-muted text-muted-foreground',
}

function formatDate(v?: string) {
    return v ? new Date(v).toLocaleString() : '—'
}

export function ApiKeysPage() {
    const [keys, setKeys] = useState<ApiKey[]>([])
    const [instances, setInstances] = useState<Instance[]>([])
    const [showCreate, setShowCreate] = useState(false)
    const [name, setName] = useState('')
    const [role, setRole] = useState('operator')
    const [allowed, setAllowed] = useState<string[]>([])
    const [created, setCreated] = useState<string | null>(null)

    const load = async () => {
        try {
            const [k, i] = await Promise.all([fetchApiKeys(), fetchInstances()])
            setKeys(k)
            setInstances(i)
        } catch (e) {
            toast.error((e as Error).message)
        }
    }

    useEffect(() => {
        Promise.all([fetchApiKeys(), fetchInstances()])
            .then(([k, i]) => {
                setKeys(k)
                setInstances(i)
            })
            .catch(e => toast.error((e as Error).message))
    }, [])

    const create = async () => {
        if (!name.trim()) return
        try {
            const res = await createApiKey({ name: name.trim(), role, allowed_instances: allowed })
            setCreated(res.key)
            setName('')
            setAllowed([])
            setShowCreate(false)
            load()
        } catch (e) {
            toast.error((e as Error).message)
        }
    }

    const toggleRevoke = async (k: ApiKey) => {
        try {
            await updateApiKey(k.id, { revoked: !k.revoked_at })
            toast.success(k.revoked_at ? 'Key restored' : 'Key revoked')
            load()
        } catch (e) {
            toast.error((e as Error).message)
        }
    }

    const remove = async (k: ApiKey) => {
        if (!confirm(`Delete key "${k.name}" permanently?`)) return
        try {
            await deleteApiKey(k.id)
            toast.success('Key deleted')
            load()
        } catch (e) {
            toast.error((e as Error).message)
        }
    }

    const instanceName = (id: string) => instances.find(i => i.id === id)?.name || id

    return (
        <div className="p-6 space-y-6 max-w-6xl mx-auto">
            <div className="flex items-center justify-between">
                <div>
                    <h1 className="text-2xl font-semibold text-foreground">API Keys</h1>
                    <p className="text-sm text-muted-foreground mt-1">
                        Keys for integrations and the dashboard. Instance tokens keep working for their own instance.
                    </p>
                </div>
                <button
                    onClick={() => setShowCreate(true)}
                    className="flex items-center gap-2 px-4 py-2 bg-primary text-primary-foreground rounded-md text-sm font-medium hover:bg-primary/90"
                >
                    <Plus size={16} /> New key
                </button>
            </div>

            {created && (
                <div className="bg-success/10 border border-success/30 rounded-xl p-4 space-y-2">
                    <div className="flex items-start justify-between gap-4">
                        <div>
                            <p className="text-sm font-medium text-foreground">Copy this key now: it will not be shown again.</p>
                            <code className="block mt-2 text-sm font-mono break-all text-foreground">{created}</code>
                        </div>
                        <div className="flex gap-2 shrink-0">
                            <button
                                onClick={() => { navigator.clipboard.writeText(created); toast.success('Copied') }}
                                className="p-2 rounded-md hover:bg-muted" aria-label="Copy key"
                            >
                                <Copy size={16} />
                            </button>
                            <button onClick={() => setCreated(null)} className="p-2 rounded-md hover:bg-muted" aria-label="Dismiss">
                                <X size={16} />
                            </button>
                        </div>
                    </div>
                </div>
            )}

            {showCreate && (
                <section className="bg-card border border-border rounded-xl p-5 space-y-4">
                    <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                        <div className="space-y-1.5">
                            <label htmlFor="key-name" className="text-sm font-medium">Name</label>
                            <input id="key-name" value={name} onChange={e => setName(e.target.value)} placeholder="n8n production"
                                className="w-full px-3 py-2 bg-background border border-border rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-ring" />
                        </div>
                        <div className="space-y-1.5">
                            <label htmlFor="key-role" className="text-sm font-medium">Role</label>
                            <select id="key-role" value={role} onChange={e => setRole(e.target.value)}
                                className="w-full px-3 py-2 bg-background border border-border rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-ring">
                                <option value="admin">admin — everything, including keys</option>
                                <option value="operator">operator — manage instances and send</option>
                                <option value="viewer">viewer — read only</option>
                            </select>
                        </div>
                    </div>
                    <div className="space-y-1.5">
                        <p className="text-sm font-medium">Instances</p>
                        <p className="text-xs text-muted-foreground">None selected = every instance, including future ones.</p>
                        <div className="flex flex-wrap gap-2 pt-1">
                            {instances.map(i => {
                                const on = allowed.includes(i.id)
                                return (
                                    <button key={i.id} type="button"
                                        onClick={() => setAllowed(on ? allowed.filter(a => a !== i.id) : [...allowed, i.id])}
                                        className={cn('px-3 py-1 rounded-full text-xs border transition-colors',
                                            on ? 'bg-primary/15 border-primary text-primary' : 'border-border text-muted-foreground hover:text-foreground')}>
                                        {i.name}
                                    </button>
                                )
                            })}
                        </div>
                    </div>
                    <div className="flex justify-end gap-2">
                        <button onClick={() => setShowCreate(false)} className="px-4 py-2 text-sm rounded-md hover:bg-muted">Cancel</button>
                        <button onClick={create} disabled={!name.trim()}
                            className="px-4 py-2 text-sm rounded-md bg-primary text-primary-foreground hover:bg-primary/90 disabled:opacity-50">
                            Create key
                        </button>
                    </div>
                </section>
            )}

            <section className="bg-card border border-border rounded-xl overflow-hidden">
                <table className="w-full text-sm">
                    <thead className="bg-muted/20 text-xs text-muted-foreground uppercase tracking-wider">
                        <tr>
                            <th className="text-left px-4 py-3 font-medium">Name</th>
                            <th className="text-left px-4 py-3 font-medium">Role</th>
                            <th className="text-left px-4 py-3 font-medium">Instances</th>
                            <th className="text-left px-4 py-3 font-medium">Last used</th>
                            <th className="px-4 py-3" />
                        </tr>
                    </thead>
                    <tbody className="divide-y divide-border">
                        {keys.length === 0 && (
                            <tr><td colSpan={5} className="px-4 py-10 text-center text-muted-foreground">
                                <KeyRound className="mx-auto mb-2 opacity-50" />No keys yet
                            </td></tr>
                        )}
                        {keys.map(k => (
                            <tr key={k.id} className={cn(k.revoked_at && 'opacity-50')}>
                                <td className="px-4 py-3">
                                    <p className="font-medium text-foreground">{k.name}</p>
                                    <p className="text-xs font-mono text-muted-foreground">{k.prefix}…</p>
                                </td>
                                <td className="px-4 py-3">
                                    <span className={cn('px-2 py-0.5 rounded-full text-[11px] font-semibold uppercase', roleStyles[k.role])}>{k.role}</span>
                                    {k.revoked_at && <span className="ml-2 text-xs text-destructive">revoked</span>}
                                </td>
                                <td className="px-4 py-3 text-muted-foreground">
                                    {k.allowed_instances.length === 0 ? 'All' : k.allowed_instances.map(instanceName).join(', ')}
                                </td>
                                <td className="px-4 py-3 text-muted-foreground">{formatDate(k.last_used_at)}</td>
                                <td className="px-4 py-3">
                                    <div className="flex justify-end gap-1">
                                        <button onClick={() => toggleRevoke(k)} title={k.revoked_at ? 'Restore' : 'Revoke'}
                                            aria-label={k.revoked_at ? 'Restore key' : 'Revoke key'} className="p-2 rounded-md hover:bg-muted text-muted-foreground hover:text-foreground">
                                            {k.revoked_at ? <RotateCcw size={16} /> : <Ban size={16} />}
                                        </button>
                                        <button onClick={() => remove(k)} title="Delete" aria-label="Delete key"
                                            className="p-2 rounded-md hover:bg-destructive/10 text-muted-foreground hover:text-destructive">
                                            <Trash2 size={16} />
                                        </button>
                                    </div>
                                </td>
                            </tr>
                        ))}
                    </tbody>
                </table>
            </section>
        </div>
    )
}

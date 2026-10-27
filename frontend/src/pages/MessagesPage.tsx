import { useEffect, useState } from 'react'
import { RefreshCw, Search, Check, CheckCheck, Clock, AlertCircle } from 'lucide-react'
import { toast } from 'sonner'
import { fetchInstances, fetchMessages } from '@/lib/api'
import type { Instance, StoredMessage } from '@/types'
import { useSession } from '@/lib/session-context'
import { cn } from '@/lib/utils'

const PAGE = 50

function StatusIcon({ m }: { m: StoredMessage }) {
    if (!m.from_me) return null
    switch (m.status) {
        case 'read':
        case 'played':
            return <CheckCheck size={14} className="text-primary" aria-label={m.status} />
        case 'delivered':
            return <CheckCheck size={14} className="text-muted-foreground" aria-label="delivered" />
        case 'sent':
            return <Check size={14} className="text-muted-foreground" aria-label="sent" />
        case 'failed':
            return <AlertCircle size={14} className="text-destructive" aria-label="failed" />
        default:
            return <Clock size={14} className="text-muted-foreground" aria-label={m.status} />
    }
}

export function MessagesPage() {
    const { me } = useSession()
    const [instances, setInstances] = useState<Instance[]>([])
    const [instance, setInstance] = useState(me?.principal.instance_id || '')
    const [chat, setChat] = useState('')
    const [items, setItems] = useState<StoredMessage[]>([])
    const [total, setTotal] = useState(0)
    const [offset, setOffset] = useState(0)
    const [loading, setLoading] = useState(false)

    useEffect(() => {
        fetchInstances().then(list => {
            setInstances(list)
            if (!instance && list.length > 0) setInstance(list[0].id)
        }).catch(() => undefined)
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [])

    const load = async () => {
        if (!instance) return
        setLoading(true)
        try {
            const res = await fetchMessages(instance, { chat: chat.trim() || undefined, limit: PAGE, offset })
            setItems(res.items)
            setTotal(res.total)
        } catch (e) {
            toast.error((e as Error).message)
        } finally {
            setLoading(false)
        }
    }

    useEffect(() => {
        load()
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [instance, offset])

    return (
        <div className="p-6 space-y-6 max-w-6xl mx-auto">
            <div>
                <h1 className="text-2xl font-semibold text-foreground">Messages</h1>
                <p className="text-sm text-muted-foreground mt-1">Stored history of received and sent messages, with delivery status.</p>
            </div>

            <div className="flex flex-wrap gap-3">
                <select value={instance} onChange={e => { setInstance(e.target.value); setOffset(0) }} aria-label="Instance"
                    className="px-3 py-2 bg-background border border-border rounded-md text-sm">
                    {instances.map(i => <option key={i.id} value={i.id}>{i.name}</option>)}
                </select>
                <form className="relative flex-1 min-w-[220px]" onSubmit={e => { e.preventDefault(); setOffset(0); load() }}>
                    <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-muted-foreground" />
                    <input value={chat} onChange={e => setChat(e.target.value)} placeholder="Filter by chat (number or JID) and press Enter"
                        className="w-full pl-9 pr-3 py-2 bg-background border border-border rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-ring" />
                </form>
                <button onClick={load} className="p-2 rounded-md hover:bg-muted text-muted-foreground" aria-label="Refresh">
                    <RefreshCw size={16} className={cn(loading && 'animate-spin')} />
                </button>
            </div>

            <section className="bg-card border border-border rounded-xl overflow-hidden">
                <table className="w-full text-sm">
                    <thead className="bg-muted/20 text-xs text-muted-foreground uppercase tracking-wider">
                        <tr>
                            <th className="text-left px-4 py-3 font-medium">Time</th>
                            <th className="text-left px-4 py-3 font-medium">Chat</th>
                            <th className="text-left px-4 py-3 font-medium">From</th>
                            <th className="text-left px-4 py-3 font-medium">Type</th>
                            <th className="text-left px-4 py-3 font-medium">Text</th>
                            <th className="px-4 py-3" />
                        </tr>
                    </thead>
                    <tbody className="divide-y divide-border">
                        {items.length === 0 && !loading && (
                            <tr><td colSpan={6} className="px-4 py-10 text-center text-muted-foreground">No messages stored for this filter</td></tr>
                        )}
                        {items.map(m => (
                            <tr key={m.id} className={cn(m.status === 'deleted' && 'opacity-50')}>
                                <td className="px-4 py-2.5 text-muted-foreground whitespace-nowrap">{new Date(m.timestamp).toLocaleString()}</td>
                                <td className="px-4 py-2.5 font-mono text-xs">{m.chat_jid}</td>
                                <td className="px-4 py-2.5">
                                    {m.from_me ? <span className="text-primary">{m.sent_by_api ? 'API' : 'Me'}</span> : (m.push_name || m.sender_jid?.split('@')[0])}
                                </td>
                                <td className="px-4 py-2.5 text-xs text-muted-foreground">{m.type.replace('Message', '')}</td>
                                <td className="px-4 py-2.5 max-w-[360px] truncate" title={m.text}>
                                    {m.text || <span className="text-muted-foreground">—</span>}
                                    {m.edited && <span className="ml-2 text-xs text-muted-foreground">(edited)</span>}
                                </td>
                                <td className="px-4 py-2.5"><StatusIcon m={m} /></td>
                            </tr>
                        ))}
                    </tbody>
                </table>
                <div className="flex items-center justify-between px-4 py-3 border-t border-border text-xs text-muted-foreground">
                    <span>{total} messages</span>
                    <div className="flex gap-2">
                        <button disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - PAGE))}
                            className="px-3 py-1 rounded-md border border-border disabled:opacity-40 hover:bg-muted">Newer</button>
                        <button disabled={offset + PAGE >= total} onClick={() => setOffset(offset + PAGE)}
                            className="px-3 py-1 rounded-md border border-border disabled:opacity-40 hover:bg-muted">Older</button>
                    </div>
                </div>
            </section>
        </div>
    )
}

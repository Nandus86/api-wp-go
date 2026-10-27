import { useEffect, useState } from 'react'
import { RefreshCw } from 'lucide-react'
import { toast } from 'sonner'
import { fetchAudit } from '@/lib/api'
import type { AuditEntry } from '@/types'
import { cn } from '@/lib/utils'

const PAGE = 50

export function AuditPage() {
    const [items, setItems] = useState<AuditEntry[]>([])
    const [total, setTotal] = useState(0)
    const [offset, setOffset] = useState(0)
    const [loading, setLoading] = useState(true)

    const load = async (off = offset) => {
        setLoading(true)
        try {
            const res = await fetchAudit({ limit: PAGE, offset: off })
            setItems(res.items)
            setTotal(res.total)
        } catch (e) {
            toast.error((e as Error).message)
        } finally {
            setLoading(false)
        }
    }

    useEffect(() => {
        load(offset)
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [offset])

    return (
        <div className="p-6 space-y-6 max-w-6xl mx-auto">
            <div className="flex items-center justify-between">
                <div>
                    <h1 className="text-2xl font-semibold text-foreground">Audit log</h1>
                    <p className="text-sm text-muted-foreground mt-1">Every management change: who did it, when and with what result.</p>
                </div>
                <button onClick={() => load()} className="p-2 rounded-md hover:bg-muted text-muted-foreground" aria-label="Refresh">
                    <RefreshCw size={16} className={cn(loading && 'animate-spin')} />
                </button>
            </div>

            <section className="bg-card border border-border rounded-xl overflow-hidden">
                <table className="w-full text-sm">
                    <thead className="bg-muted/20 text-xs text-muted-foreground uppercase tracking-wider">
                        <tr>
                            <th className="text-left px-4 py-3 font-medium">When</th>
                            <th className="text-left px-4 py-3 font-medium">Actor</th>
                            <th className="text-left px-4 py-3 font-medium">Action</th>
                            <th className="text-left px-4 py-3 font-medium">Instance</th>
                            <th className="text-left px-4 py-3 font-medium">Result</th>
                        </tr>
                    </thead>
                    <tbody className="divide-y divide-border">
                        {items.length === 0 && !loading && (
                            <tr><td colSpan={5} className="px-4 py-10 text-center text-muted-foreground">No entries</td></tr>
                        )}
                        {items.map(e => (
                            <tr key={e.id}>
                                <td className="px-4 py-2.5 text-muted-foreground whitespace-nowrap">{new Date(e.created_at).toLocaleString()}</td>
                                <td className="px-4 py-2.5">
                                    <p className="text-foreground">{e.actor_name || e.actor_id}</p>
                                    <p className="text-xs text-muted-foreground">{e.actor_type} · {e.ip}</p>
                                </td>
                                <td className="px-4 py-2.5 font-mono text-xs">
                                    <span className="font-semibold">{e.method}</span> {e.route || e.path}
                                </td>
                                <td className="px-4 py-2.5 text-muted-foreground">{e.instance_id || '—'}</td>
                                <td className="px-4 py-2.5">
                                    <span className={cn('px-2 py-0.5 rounded-full text-xs font-semibold',
                                        e.status_code < 400 ? 'bg-success/10 text-success' : 'bg-destructive/10 text-destructive')}>
                                        {e.status_code}
                                    </span>
                                </td>
                            </tr>
                        ))}
                    </tbody>
                </table>
                <div className="flex items-center justify-between px-4 py-3 border-t border-border text-xs text-muted-foreground">
                    <span>{total} entries</span>
                    <div className="flex gap-2">
                        <button disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - PAGE))}
                            className="px-3 py-1 rounded-md border border-border disabled:opacity-40 hover:bg-muted">Previous</button>
                        <button disabled={offset + PAGE >= total} onClick={() => setOffset(offset + PAGE)}
                            className="px-3 py-1 rounded-md border border-border disabled:opacity-40 hover:bg-muted">Next</button>
                    </div>
                </div>
            </section>
        </div>
    )
}

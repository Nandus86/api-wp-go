import { useEffect, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { Plus, Search } from 'lucide-react'
import { InstanceCard } from '@/components/instances/InstanceCard'
import { QRCodeDialog } from '@/components/instances/QRCodeDialog'
import { EditInstanceDialog } from '@/components/instances/EditInstanceDialog'
import { toast } from 'sonner'
import { fetchInstances, createInstance, deleteInstance, reconnectInstance } from '@/lib/api'
import type { Instance } from '@/types'

type Filter = 'all' | 'online' | 'offline'

export function InstancesPage() {
    const [instances, setInstances] = useState<Instance[]>([])
    const [loading, setLoading] = useState(true)
    const [search, setSearch] = useState('')
    const [searchParams, setSearchParams] = useSearchParams()
    const [showCreate, setShowCreate] = useState(false)
    const [newName, setNewName] = useState('')
    const [newWebhook, setNewWebhook] = useState('')
    const [newProxy, setNewProxy] = useState('')

    // QR Code Dialog State
    const [qrDialogOpen, setQrDialogOpen] = useState(false)
    const [qrDialogId, setQrDialogId] = useState<string | null>(null)
    const [qrDialogName, setQrDialogName] = useState<string | null>(null)

    // Edit Dialog State
    const [editInstance, setEditInstance] = useState<Instance | null>(null)

    const filter = (searchParams.get('filter') as Filter) || 'all'

    const loadInstances = async () => {
        try {
            const data = await fetchInstances()
            setInstances(data || [])
        } catch (e) {
            console.error('Failed to load instances:', e)
        } finally {
            setLoading(false)
        }
    }

    useEffect(() => {
        loadInstances()
        const interval = setInterval(loadInstances, 10000)
        return () => clearInterval(interval)
    }, [])

    const filteredInstances = instances
        .filter(i => {
            if (filter === 'online') return i.status === 'connected' || i.status === 'online'
            if (filter === 'offline') return i.status !== 'connected' && i.status !== 'online'
            return true
        })
        .filter(i =>
            search === '' ||
            i.name.toLowerCase().includes(search.toLowerCase()) ||
            (i.jid && i.jid.includes(search))
        )

    const handleCreate = async () => {
        if (!newName.trim()) return
        try {
            const created = await createInstance(newName, newWebhook, newProxy)
            setNewName('')
            setNewWebhook('')
            setNewProxy('')
            setShowCreate(false)

            // Auto open QR dialog for new instances
            setQrDialogId(created.device_id)
            setQrDialogName(created.name)
            setQrDialogOpen(true)

            toast.success('Instance created successfully')
            loadInstances()
        } catch (e) {
            toast.error('Failed to create instance')
            console.error(e)
        }
    }

    const handlePair = async (id: string, name: string) => {
        setQrDialogId(id)
        setQrDialogName(name)
        setQrDialogOpen(true)
    }

    const handleReconnect = async (id: string) => {
        try {
            await reconnectInstance(id)
            toast.success('Reconnect command sent')
            loadInstances()
        } catch (e) {
            toast.error('Failed to reconnect instance')
            console.error(e)
        }
    }

    const handleDelete = async (id: string) => {
        if (!confirm('Delete this instance?')) return
        try {
            await deleteInstance(id)
            toast.success('Instance deleted')
            loadInstances()
        } catch (e) {
            toast.error('Failed to delete instance')
            console.error(e)
        }
    }

    const setFilter = (f: Filter) => {
        if (f === 'all') {
            searchParams.delete('filter')
        } else {
            searchParams.set('filter', f)
        }
        setSearchParams(searchParams)
    }

    return (
        <div className="p-6 space-y-6">
            {/* Header */}
            <div className="flex items-center justify-between">
                <h1 className="text-2xl font-semibold text-foreground">Instances</h1>
                <button
                    onClick={() => setShowCreate(true)}
                    className="flex items-center gap-2 px-4 py-2 bg-primary text-primary-foreground rounded-md text-sm font-medium hover:bg-primary/90 transition-colors"
                >
                    <Plus size={16} />
                    New Instance
                </button>
            </div>

            {/* Search and Filters */}
            <div className="flex items-center gap-4">
                <div className="relative flex-1 max-w-sm">
                    <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-muted-foreground" />
                    <input
                        type="text"
                        placeholder="Search instances..."
                        value={search}
                        onChange={e => setSearch(e.target.value)}
                        className="w-full pl-10 pr-4 py-2 bg-card border border-border rounded-md text-sm text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring"
                    />
                </div>
                <div className="flex gap-1 bg-card border border-border rounded-md p-1">
                    {(['all', 'online', 'offline'] as Filter[]).map(f => (
                        <button
                            key={f}
                            onClick={() => setFilter(f)}
                            className={`px-3 py-1.5 rounded text-xs font-medium transition-colors capitalize ${filter === f
                                ? 'bg-secondary text-foreground'
                                : 'text-muted-foreground hover:text-foreground'
                                }`}
                        >
                            {f}
                        </button>
                    ))}
                </div>
            </div>

            {/* Instance Grid */}
            {loading ? (
                <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
                    {[1, 2, 3, 4, 5, 6].map(i => (
                        <div key={i} className="bg-card rounded-lg border border-border p-4 animate-pulse h-36" />
                    ))}
                </div>
            ) : filteredInstances.length === 0 ? (
                <div className="bg-card rounded-lg border border-border p-8 text-center">
                    <p className="text-muted-foreground">
                        {instances.length === 0
                            ? 'No instances yet. Create your first one!'
                            : 'No instances match your filters.'}
                    </p>
                </div>
            ) : (
                <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
                    {filteredInstances.map(instance => (
                        <InstanceCard
                            key={instance.id}
                            instance={instance}
                            onReconnect={handleReconnect}
                            onPair={handlePair}
                            onEdit={() => setEditInstance(instance)}
                            onDelete={handleDelete}
                        />
                    ))}
                </div>
            )}

            {/* Create Instance Dialog */}
            {showCreate && (
                <div className="fixed inset-0 bg-black/60 flex items-center justify-center z-50" onClick={() => setShowCreate(false)}>
                    <div className="bg-card border border-border rounded-lg p-6 w-full max-w-md space-y-4" onClick={e => e.stopPropagation()}>
                        <h2 className="text-lg font-semibold text-foreground">New Instance</h2>

                        <div className="space-y-3">
                            <div>
                                <label className="text-sm text-muted-foreground mb-1 block">Instance Name *</label>
                                <input
                                    type="text"
                                    value={newName}
                                    onChange={e => setNewName(e.target.value)}
                                    placeholder="e.g. Farmácia João"
                                    className="w-full px-3 py-2 bg-background border border-border rounded-md text-sm text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring"
                                />
                            </div>
                            <div>
                                <label className="text-sm text-muted-foreground mb-1 block">Webhook URL</label>
                                <input
                                    type="text"
                                    value={newWebhook}
                                    onChange={e => setNewWebhook(e.target.value)}
                                    placeholder="https://n8n.example.com/webhook/..."
                                    className="w-full px-3 py-2 bg-background border border-border rounded-md text-sm text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring"
                                />
                            </div>
                            <div>
                                <label className="text-sm text-muted-foreground mb-1 block">Proxy URI</label>
                                <input
                                    type="text"
                                    value={newProxy}
                                    onChange={e => setNewProxy(e.target.value)}
                                    placeholder="socks5://user:pass@host:port"
                                    className="w-full px-3 py-2 bg-background border border-border rounded-md text-sm text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring"
                                />
                            </div>
                        </div>

                        <div className="flex justify-end gap-3 pt-2">
                            <button
                                onClick={() => setShowCreate(false)}
                                className="px-4 py-2 text-sm text-muted-foreground hover:text-foreground transition-colors"
                            >
                                Cancel
                            </button>
                            <button
                                onClick={handleCreate}
                                disabled={!newName.trim()}
                                className="px-4 py-2 bg-primary text-primary-foreground rounded-md text-sm font-medium hover:bg-primary/90 transition-colors disabled:opacity-50"
                            >
                                Create
                            </button>
                        </div>
                    </div>
                </div>
            )}

            {/* QR Code Dialog for Pairing */}
            <QRCodeDialog
                isOpen={qrDialogOpen}
                instanceId={qrDialogId}
                instanceName={qrDialogName}
                onClose={() => {
                    setQrDialogOpen(false)
                    loadInstances()
                }}
                onConnected={() => {
                    setQrDialogOpen(false)
                    loadInstances()
                }}
            />

            {/* Edit Instance Dialog */}
            <EditInstanceDialog
                instance={editInstance}
                isOpen={!!editInstance}
                onClose={() => setEditInstance(null)}
                onSaved={() => {
                    setEditInstance(null)
                    loadInstances()
                }}
            />
        </div>
    )
}

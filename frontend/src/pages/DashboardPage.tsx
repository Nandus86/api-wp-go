import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Smartphone, Wifi, WifiOff, MessageCircle } from 'lucide-react'
import { StatCard } from '@/components/dashboard/StatCard'
import { MessageChart } from '@/components/dashboard/MessageChart'
import { InstanceCard } from '@/components/instances/InstanceCard'
import { QRCodeDialog } from '@/components/instances/QRCodeDialog'
import { toast } from 'sonner'
import { fetchInstances, deleteInstance, reconnectInstance, fetchStats } from '@/lib/api'
import type { Instance } from '@/types'

export function DashboardPage() {
    const [instances, setInstances] = useState<Instance[]>([])
    const [messagesPerHour, setMessagesPerHour] = useState<number | string>('—')
    const [loading, setLoading] = useState(true)
    const navigate = useNavigate()

    // QR Code Dialog State
    const [qrDialogOpen, setQrDialogOpen] = useState(false)
    const [qrDialogId, setQrDialogId] = useState<string | null>(null)
    const [qrDialogName, setQrDialogName] = useState<string | null>(null)

    const loadInstances = async () => {
        try {
            const data = await fetchInstances()
            setInstances(data || [])

            // Also fetch stats for the stat card
            const stats = await fetchStats()
            if (stats && stats.length > 0) {
                // Get current hour out of stats (now relative to local time)
                const nowHr = new Date().getHours().toString().padStart(2, '0') + ':00'
                const total = stats.reduce((acc, curr) => {
                    const localHr = new Date(curr.hour).getHours().toString().padStart(2, '0') + ':00'
                    return localHr === nowHr ? acc + Number(curr.count) : acc
                }, 0)
                setMessagesPerHour(total)
            } else {
                setMessagesPerHour(0)
            }

        } catch (e) {
            console.error('Failed to load instances or stats:', e)
        } finally {
            setLoading(false)
        }
    }

    useEffect(() => {
        loadInstances()
        const interval = setInterval(loadInstances, 10000)
        return () => clearInterval(interval)
    }, [])

    const online = instances.filter(i => i.status === 'connected' || i.status === 'online')
    const offline = instances.filter(i => i.status !== 'connected' && i.status !== 'online')

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

    return (
        <div className="p-6 space-y-6">
            <h1 className="text-2xl font-semibold text-foreground">Dashboard</h1>

            {/* Stat Cards */}
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
                <StatCard
                    label="Total Instances"
                    value={loading ? '—' : instances.length}
                    icon={<Smartphone size={20} />}
                />
                <StatCard
                    label="Online"
                    value={loading ? '—' : online.length}
                    icon={<Wifi size={20} />}
                    variant="success"
                    onClick={() => navigate('/instances?filter=online')}
                />
                <StatCard
                    label="Offline"
                    value={loading ? '—' : offline.length}
                    icon={<WifiOff size={20} />}
                    variant={offline.length > 0 ? 'destructive' : 'default'}
                    onClick={() => navigate('/instances?filter=offline')}
                />
                <StatCard
                    label="Messages / h"
                    value={loading ? '—' : messagesPerHour}
                    icon={<MessageCircle size={20} />}
                />
            </div>

            {/* Main Content Grid */}
            <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
                {/* Chart Section */}
                <div className="lg:col-span-2">
                    <MessageChart instances={instances} />
                </div>

                {/* Recent Instances */}
                <div className="lg:col-span-1">
                    <h2 className="text-lg font-semibold text-foreground mb-4">Recent Instances</h2>
                    {loading ? (
                        <div className="flex flex-col gap-4">
                            {[1, 2, 3].map(i => (
                                <div key={i} className="bg-card rounded-lg border border-border p-4 animate-pulse h-36" />
                            ))}
                        </div>
                    ) : instances.length === 0 ? (
                        <div className="bg-card rounded-lg border border-border p-8 text-center flex flex-col justify-center h-[350px]">
                            <Smartphone size={40} className="mx-auto text-muted-foreground mb-3" />
                            <p className="text-muted-foreground mb-2">No instances yet</p>
                            <button
                                onClick={() => navigate('/instances')}
                                className="px-4 py-2 bg-primary text-primary-foreground rounded-md text-sm font-medium hover:bg-primary/90 transition-colors mx-auto"
                            >
                                + Create
                            </button>
                        </div>
                    ) : (
                        <div className="flex flex-col gap-4 h-[350px] overflow-y-auto pr-2 custom-scrollbar">
                            {instances.slice(0, 6).map(instance => (
                                <InstanceCard
                                    key={instance.id}
                                    instance={instance}
                                    onReconnect={handleReconnect}
                                    onPair={handlePair}
                                    onEdit={() => navigate('/instances')}
                                    onDelete={handleDelete}
                                />
                            ))}
                        </div>
                    )}
                </div>
            </div>

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
        </div>
    )
}

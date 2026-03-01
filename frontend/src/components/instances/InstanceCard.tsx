import { cn } from '@/lib/utils'
import type { Instance } from '@/types'
import { Wifi, WifiOff, RefreshCw, Pencil, Trash2 } from 'lucide-react'

interface InstanceCardProps {
    instance: Instance
    onReconnect: (id: string) => void
    onPair: (id: string, name: string) => void
    onEdit: (instance: Instance) => void
    onDelete: (id: string) => void
}

function StatusBadge({ status }: { status: string }) {
    const isOnline = status === 'connected' || status === 'online'
    const isReconnecting = status === 'reconnecting'

    const isUnpaired = status === 'unpaired'

    return (
        <span
            className={cn(
                'inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-medium',
                isOnline && 'bg-success/10 text-success',
                isReconnecting && 'bg-warning/10 text-warning',
                isUnpaired && 'bg-muted/50 text-muted-foreground border border-border/50',
                !isOnline && !isReconnecting && !isUnpaired && 'bg-destructive/10 text-destructive'
            )}
        >
            <span
                className={cn(
                    'w-2 h-2 rounded-full',
                    isOnline && 'bg-success',
                    isReconnecting && 'bg-warning animate-pulse',
                    !isOnline && !isReconnecting && 'bg-destructive'
                )}
            />
            {isOnline ? 'Online' : isReconnecting ? 'Reconnecting' : isUnpaired ? 'Unpaired' : 'Offline'}
        </span>
    )
}

export function InstanceCard({ instance, onReconnect, onPair, onEdit, onDelete }: InstanceCardProps) {
    const isOnline = instance.status === 'connected' || instance.status === 'online'
    const isUnpaired = instance.status === 'unpaired'

    return (
        <div className="group bg-card rounded-lg border border-border p-4 transition-all hover:border-muted-foreground/30">
            {/* Header */}
            <div className="flex items-start justify-between mb-3">
                <div className="flex items-center gap-2.5">
                    <div className={cn(
                        'w-9 h-9 rounded-md flex items-center justify-center',
                        isOnline ? 'bg-success/10' : 'bg-destructive/10'
                    )}>
                        {isOnline ? <Wifi size={18} className="text-success" /> : <WifiOff size={18} className="text-destructive" />}
                    </div>
                    <div>
                        <h3 className="text-sm font-semibold text-foreground">{instance.name}</h3>
                        <p className="text-xs text-muted-foreground">
                            {instance.jid || instance.phone || `ID: ${instance.id}`}
                        </p>
                    </div>
                </div>
                <StatusBadge status={instance.status} />
            </div>

            {/* Info rows */}
            <div className="space-y-1.5 mb-3 text-xs text-muted-foreground">
                {instance.webhook_url && (
                    <div className="flex items-center gap-1.5 truncate">
                        <span className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground/70">Webhook</span>
                        <span className="truncate">{instance.webhook_url}</span>
                    </div>
                )}
                {instance.proxy_uri && (
                    <div className="flex items-center gap-1.5 truncate">
                        <span className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground/70">Proxy</span>
                        <span className="truncate">{instance.proxy_uri}</span>
                    </div>
                )}
            </div>

            {/* Actions (always visible now) */}
            <div className="flex gap-2 mt-4 pt-3 border-t border-border/50">
                {isUnpaired ? (
                    <button
                        onClick={() => onPair(instance.id, instance.name)}
                        className="flex items-center gap-1.5 px-3 py-1.5 rounded-md bg-primary/10 text-primary text-xs font-medium hover:bg-primary/20 transition-colors"
                    >
                        <RefreshCw size={12} />
                        Pair Device
                    </button>
                ) : !isOnline && (
                    <button
                        onClick={() => onReconnect(instance.id)}
                        className="flex items-center gap-1.5 px-3 py-1.5 rounded-md bg-success/10 text-success text-xs font-medium hover:bg-success/20 transition-colors"
                    >
                        <RefreshCw size={12} />
                        Reconnect
                    </button>
                )}
                <button
                    onClick={() => onEdit(instance)}
                    className="flex items-center gap-1.5 px-3 py-1.5 rounded-md bg-secondary text-secondary-foreground text-xs font-medium hover:bg-secondary/80 transition-colors"
                >
                    <Pencil size={12} />
                    Edit
                </button>
                <button
                    onClick={() => onDelete(instance.id)}
                    className="flex items-center gap-1.5 px-3 py-1.5 rounded-md bg-destructive/10 text-destructive text-xs font-medium hover:bg-destructive/20 transition-colors"
                >
                    <Trash2 size={12} />
                    Delete
                </button>
            </div>
        </div>
    )
}

import { cn } from '@/lib/utils'

interface StatCardProps {
    label: string
    value: string | number
    icon: React.ReactNode
    variant?: 'default' | 'success' | 'destructive' | 'warning'
    onClick?: () => void
}

const variantStyles = {
    default: 'border-border',
    success: 'border-success/30 bg-success/5',
    destructive: 'border-destructive/30 bg-destructive/5',
    warning: 'border-warning/30 bg-warning/5',
}

const valueStyles = {
    default: 'text-foreground',
    success: 'text-success',
    destructive: 'text-destructive',
    warning: 'text-warning',
}

export function StatCard({ label, value, icon, variant = 'default', onClick }: StatCardProps) {
    return (
        <div
            onClick={onClick}
            className={cn(
                'bg-card rounded-lg border p-5 transition-all',
                variantStyles[variant],
                onClick && 'cursor-pointer hover:bg-card/80 hover:scale-[1.02] active:scale-[0.98]'
            )}
        >
            <div className="flex items-center justify-between mb-3">
                <span className="text-sm text-muted-foreground font-medium">{label}</span>
                <span className="text-muted-foreground">{icon}</span>
            </div>
            <p className={cn('text-3xl font-bold', valueStyles[variant])}>
                {value}
            </p>
        </div>
    )
}

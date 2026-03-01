import { useEffect, useState, useMemo } from 'react'
import { Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis, CartesianGrid } from 'recharts'
import { fetchStats } from '@/lib/api'
import type { Instance } from '@/types'
import { Loader2 } from 'lucide-react'

interface MessageChartProps {
    instances: Instance[]
}

export function MessageChart({ instances }: MessageChartProps) {
    const [stats, setStats] = useState<any[]>([])
    const [loading, setLoading] = useState(true)
    const [selectedInstance, setSelectedInstance] = useState<string>('all')

    const loadStats = async () => {
        try {
            const data = await fetchStats(selectedInstance === 'all' ? undefined : selectedInstance)
            setStats(data || [])
        } catch (e) {
            console.error('Failed to load stats:', e)
        } finally {
            setLoading(false)
        }
    }

    useEffect(() => {
        loadStats()
        const int = setInterval(loadStats, 10000)
        return () => clearInterval(int)
    }, [selectedInstance])

    // Process flat data into pivot table needed for recharts
    // e.g. [{hour: "01:00", in: 5, out: 12}]
    const chartData = useMemo(() => {
        const map = new Map<string, { name: string, sent: number, received: number }>()

        // Initialize last 24 hours to 0 to ensure continuous chart
        const now = new Date()
        for (let i = 23; i >= 0; i--) {
            const d = new Date(now.getTime() - i * 60 * 60 * 1000)
            const hStr = d.getHours().toString().padStart(2, '0') + ':00'
            map.set(hStr, { name: hStr, sent: 0, received: 0 })
        }

        stats.forEach(s => {
            const localHrStr = new Date(s.hour).getHours().toString().padStart(2, '0') + ':00'
            const pt = map.get(localHrStr) || { name: localHrStr, sent: 0, received: 0 }
            if (s.direction === 'out') {
                pt.sent += Number(s.count)
            } else {
                pt.received += Number(s.count)
            }
            map.set(localHrStr, pt)
        })

        // Return without alphabetical sort to maintain the rolling 24-hour left-to-right chronological order
        return Array.from(map.values())
    }, [stats])

    return (
        <div className="bg-card border border-border rounded-xl p-6 flex flex-col h-[350px]">
            <div className="mb-6 flex items-start justify-between">
                <div>
                    <h3 className="text-sm font-semibold text-foreground">Message Traffic</h3>
                    <p className="text-xs text-muted-foreground mt-1">Real-time metrics of sent vs received messages</p>
                </div>
                {instances.length > 0 && (
                    <select
                        value={selectedInstance}
                        onChange={(e) => setSelectedInstance(e.target.value)}
                        className="bg-background border border-border text-foreground text-xs rounded-md px-2 py-1 focus:ring-1 focus:ring-primary focus:outline-none"
                    >
                        <option value="all">All Instances</option>
                        {instances.map(inst => (
                            <option key={inst.id} value={inst.id}>{inst.name}</option>
                        ))}
                    </select>
                )}
            </div>

            <div className="flex-1 min-h-0 relative">
                {loading && stats.length === 0 ? (
                    <div className="absolute inset-0 flex items-center justify-center">
                        <Loader2 className="animate-spin text-muted-foreground" size={24} />
                    </div>
                ) : (
                    <ResponsiveContainer width="100%" height="100%">
                        <LineChart data={chartData} margin={{ top: 5, right: 10, left: -20, bottom: 0 }}>
                            <CartesianGrid strokeDasharray="3 3" vertical={false} stroke="var(--color-border)" opacity={0.5} />
                            <XAxis
                                dataKey="name"
                                axisLine={false}
                                tickLine={false}
                                tick={{ fontSize: 12, fill: 'var(--color-muted-foreground)' }}
                                dy={10}
                            />
                            <YAxis
                                axisLine={false}
                                tickLine={false}
                                tick={{ fontSize: 12, fill: 'var(--color-muted-foreground)' }}
                            />
                            <Tooltip
                                contentStyle={{
                                    backgroundColor: 'var(--color-card)',
                                    borderColor: 'var(--color-border)',
                                    borderRadius: '8px',
                                    fontSize: '12px',
                                    boxShadow: '0 4px 6px -1px rgb(0 0 0 / 0.1), 0 2px 4px -2px rgb(0 0 0 / 0.1)'
                                }}
                                itemStyle={{ color: 'var(--color-foreground)' }}
                            />
                            <Line
                                type="monotone"
                                name="Messages Sent"
                                dataKey="sent"
                                stroke="var(--color-primary)"
                                strokeWidth={2}
                                dot={false}
                                activeDot={{ r: 4, fill: 'var(--color-primary)', strokeWidth: 0 }}
                            />
                            <Line
                                type="monotone"
                                name="Messages Received"
                                dataKey="received"
                                stroke="var(--color-muted-foreground)"
                                strokeWidth={2}
                                dot={false}
                                activeDot={{ r: 4, fill: 'var(--color-muted-foreground)', strokeWidth: 0 }}
                            />
                        </LineChart>
                    </ResponsiveContainer>
                )}
            </div>
        </div>
    )
}

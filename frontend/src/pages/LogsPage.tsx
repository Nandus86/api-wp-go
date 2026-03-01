import { useState, useEffect } from 'react'
import { Search, Filter, AlertCircle, Info, CheckCircle2, RefreshCw } from 'lucide-react'
import { fetchLogs, type LogEntry } from '@/lib/api'

export function LogsPage() {
    const [search, setSearch] = useState('')
    const [logs, setLogs] = useState<LogEntry[]>([])
    const [loading, setLoading] = useState(true)

    const loadLogs = async () => {
        try {
            const data = await fetchLogs()
            setLogs(data)
        } catch (e) {
            console.error(e)
        } finally {
            setLoading(false)
        }
    }

    useEffect(() => {
        loadLogs()
        // Poll every 5 seconds for new logs
        const int = setInterval(loadLogs, 5000)
        return () => clearInterval(int)
    }, [])

    const LevelIcon = ({ level }: { level: string }) => {
        switch (level) {
            case 'info': return <Info size={16} className="text-primary" />
            case 'warn': return <AlertCircle size={16} className="text-warning" />
            case 'error': return <AlertCircle size={16} className="text-destructive" />
            case 'success': return <CheckCircle2 size={16} className="text-success" />
            default: return <Info size={16} />
        }
    }

    const LevelBadge = ({ level }: { level: string }) => {
        const styles: Record<string, string> = {
            info: 'bg-primary/10 text-primary',
            warn: 'bg-warning/10 text-warning',
            error: 'bg-destructive/10 text-destructive',
            success: 'bg-success/10 text-success',
        }
        return (
            <span className={`uppercase text - [10px] font - bold tracking - wider px - 2 py - 0.5 rounded - full ${styles[level] || styles.info} `}>
                {level}
            </span>
        )
    }

    return (
        <div className="p-6 space-y-6 max-w-6xl mx-auto">
            <div className="flex items-center justify-between">
                <div>
                    <h1 className="text-2xl font-semibold text-foreground">System Logs</h1>
                    <p className="text-sm text-muted-foreground mt-1">Monitor instance events and errors.</p>
                </div>
            </div>

            <div className="bg-card border border-border rounded-xl flex flex-col overflow-hidden">
                {/* Toolbar */}
                <div className="p-4 flex items-center gap-4 border-b border-border bg-muted/20">
                    <div className="relative flex-1 max-w-md">
                        <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-muted-foreground" />
                        <input
                            type="text"
                            placeholder="Filter logs by message or instance..."
                            value={search}
                            onChange={e => setSearch(e.target.value)}
                            className="w-full pl-10 pr-4 py-2 bg-background border border-border rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-ring"
                        />
                    </div>
                    <button className="flex items-center gap-2 px-3 py-2 bg-background border border-border rounded-md text-sm font-medium hover:bg-muted transition-colors">
                        <Filter size={16} />
                        Filter
                    </button>
                </div>

                {/* Table */}
                <div className="overflow-x-auto">
                    <table className="w-full text-sm text-left">
                        <thead className="text-xs text-muted-foreground uppercase bg-muted/30 border-b border-border">
                            <tr>
                                <th className="px-6 py-3 text-left text-[10px] font-bold tracking-wider text-muted-foreground uppercase bg-muted/30 w-32 border-b border-border">Time</th>
                                <th className="px-6 py-3 text-left text-[10px] font-bold tracking-wider text-muted-foreground uppercase bg-muted/30 w-32 border-b border-border">Level</th>
                                <th className="px-6 py-3 text-left text-[10px] font-bold tracking-wider text-muted-foreground uppercase bg-muted/30 border-b border-border">Message</th>
                            </tr>
                        </thead>
                        <tbody>
                            {loading && logs.length === 0 ? (
                                <tr>
                                    <td colSpan={3} className="px-6 py-8 text-center text-muted-foreground">
                                        <RefreshCw className="animate-spin inline-block mr-2" size={16} /> Loading logs...
                                    </td>
                                </tr>
                            ) : logs.length === 0 ? (
                                <tr>
                                    <td colSpan={4} className="px-6 py-8 text-center text-muted-foreground">
                                        No logs available
                                    </td>
                                </tr>
                            ) : logs.filter(l => l.message.toLowerCase().includes(search.toLowerCase())).map((log, i) => (
                                <tr key={i} className="border-b border-border/50 hover:bg-muted/10 transition-colors">
                                    <td className="px-6 py-4 whitespace-nowrap text-muted-foreground font-mono text-xs">
                                        {log.time}
                                    </td>
                                    <td className="px-6 py-4 whitespace-nowrap">
                                        <LevelBadge level={log.level} />
                                    </td>
                                    <td className="px-6 py-4 flex items-center gap-2 text-foreground break-words whitespace-normal text-xs">
                                        <LevelIcon level={log.level} />
                                        {log.message}
                                    </td>
                                </tr>
                            ))}
                        </tbody>
                    </table>
                </div>
            </div>
        </div>
    )
}

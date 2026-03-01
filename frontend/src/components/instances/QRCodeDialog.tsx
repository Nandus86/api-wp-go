import { useEffect, useState } from 'react'
import QRCode from 'react-qr-code'
import { X, Loader2, CheckCircle2 } from 'lucide-react'
import { toast } from 'sonner'
import { API_BASE } from '@/lib/api'

interface QRCodeDialogProps {
    isOpen: boolean
    instanceId: string | null
    instanceName: string | null
    onClose: () => void
    onConnected: () => void
}

export function QRCodeDialog({ isOpen, instanceId, instanceName, onClose, onConnected }: QRCodeDialogProps) {
    const [qrCode, setQrCode] = useState<string | null>(null)
    const [status, setStatus] = useState<'connecting' | 'waiting_scan' | 'connected' | 'error'>('connecting')
    const [errorMsg, setErrorMsg] = useState<string | null>(null)

    useEffect(() => {
        if (!isOpen || !instanceId) {
            setQrCode(null)
            setStatus('connecting')
            setErrorMsg(null)
            return
        }

        let eventSource: EventSource | null = null
        let pollInterval: number | null = null

        const connectSSE = () => {
            setStatus('connecting')
            eventSource = new EventSource(`${API_BASE}/device/${instanceId}/qr`)

            eventSource.onopen = () => {
                console.log('SSE connection opened');
                setStatus('waiting_scan')
            }

            eventSource.onmessage = (event) => {
                console.log('SSE message received', event.data);
                // Force state out of connecting when first message bursts in
                setStatus('waiting_scan')

                if (event.data && event.data.trim() !== '' && event.data !== 'connected') {
                    setQrCode(event.data)
                }
            }

            eventSource.onerror = (err) => {
                console.error('SSE Error:', err)
                setStatus('error')
                setErrorMsg('Lost connection to pairing server')
                toast.error('Lost connection to pairing server')
                eventSource?.close()
            }
        }

        const checkStatus = async () => {
            try {
                const res = await fetch(`${API_BASE}/device/${instanceId}/status`)
                if (res.ok) {
                    const data = await res.json()
                    if (data.status === 'connected') {
                        setStatus('connected')
                        toast.success('Device paired successfully!')
                        if (eventSource) {
                            eventSource.close()
                        }
                        if (pollInterval) {
                            window.clearInterval(pollInterval)
                        }
                        setTimeout(() => {
                            onConnected()
                        }, 1500)
                    }
                }
            } catch (e) {
                console.error('Status check error:', e)
            }
        }

        connectSSE()
        pollInterval = window.setInterval(checkStatus, 3000)

        return () => {
            if (eventSource) eventSource.close()
            if (pollInterval) window.clearInterval(pollInterval)
        }
    }, [isOpen, instanceId, onConnected])

    if (!isOpen) return null

    return (
        <div className="fixed inset-0 bg-black/60 flex items-center justify-center z-50 p-4" onClick={onClose}>
            <div
                className="bg-card border border-border rounded-xl shadow-lg w-full max-w-sm flex flex-col overflow-hidden animate-in fade-in zoom-in-95 duration-200"
                onClick={e => e.stopPropagation()}
            >
                {/* Header */}
                <div className="flex items-center justify-between px-6 py-4 border-b border-border bg-muted/30">
                    <div>
                        <h2 className="text-lg font-semibold text-foreground">Pair Device</h2>
                        <p className="text-xs text-muted-foreground">{instanceName}</p>
                    </div>
                    <button
                        onClick={onClose}
                        className="p-2 text-muted-foreground hover:text-foreground hover:bg-muted rounded-md transition-colors"
                    >
                        <X size={18} />
                    </button>
                </div>

                {/* Content */}
                <div className="p-8 flex flex-col items-center justify-center min-h-[300px]">
                    {status === 'connecting' && (
                        <div className="flex flex-col items-center text-muted-foreground gap-4">
                            <Loader2 className="animate-spin" size={32} />
                            <p className="text-sm font-medium">Connecting to instance...</p>
                        </div>
                    )}

                    {status === 'waiting_scan' && (
                        <div className="flex flex-col items-center gap-6 w-full">
                            {qrCode ? (
                                <div className="bg-white p-4 rounded-xl shadow-sm">
                                    <QRCode
                                        value={qrCode}
                                        size={200}
                                        level="L"
                                    />
                                </div>
                            ) : (
                                <div className="w-[200px] h-[200px] bg-muted animate-pulse rounded-xl flex items-center justify-center">
                                    <Loader2 className="animate-spin text-muted-foreground" size={24} />
                                </div>
                            )}
                            <div className="text-center space-y-1">
                                <p className="text-sm font-medium text-foreground">Open WhatsApp on your phone</p>
                                <p className="text-xs text-muted-foreground">Tap Menu or Settings and select Linked Devices</p>
                            </div>
                        </div>
                    )}

                    {status === 'connected' && (
                        <div className="flex flex-col items-center text-green-500 gap-4 animate-in zoom-in duration-300">
                            <CheckCircle2 size={48} />
                            <p className="text-lg font-medium">Device Connected!</p>
                        </div>
                    )}

                    {status === 'error' && (
                        <div className="flex flex-col items-center text-destructive gap-4 text-center">
                            <X size={32} />
                            <p className="text-sm font-medium">{errorMsg || 'Failed to generate QR Code'}</p>
                            <button
                                onClick={onClose}
                                className="mt-2 px-4 py-2 bg-secondary text-secondary-foreground rounded-md text-sm hover:bg-secondary/80"
                            >
                                Close
                            </button>
                        </div>
                    )}
                </div>
            </div>
        </div>
    )
}

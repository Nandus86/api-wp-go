import { useEffect, useState } from 'react'
import { X, Save, Eye, EyeOff } from 'lucide-react'
import { renameInstance, updateWebhook, updateProxy, updateCredentials } from '@/lib/api'
import type { Instance } from '@/types'
import { toast } from 'sonner'

interface EditInstanceDialogProps {
    instance: Instance | null
    isOpen: boolean
    onClose: () => void
    onSaved: () => void
}

export function EditInstanceDialog({ instance, isOpen, onClose, onSaved }: EditInstanceDialogProps) {
    const [name, setName] = useState('')
    const [webhookUrl, setWebhookUrl] = useState('')
    const [proxyUri, setProxyUri] = useState('')
    const [id, setId] = useState('')
    const [apiKey, setApiKey] = useState('')
    const [showApiKey, setShowApiKey] = useState(false)
    const [isSaving, setIsSaving] = useState(false)

    useEffect(() => {
        if (instance && isOpen) {
            setName(instance.name)
            setWebhookUrl(instance.webhook_url || '')
            setProxyUri(instance.proxy_uri || '')
            setId(instance.id)
            setApiKey(instance.api_key || '')
            setShowApiKey(false)
        }
    }, [instance, isOpen])

    if (!isOpen || !instance) return null

    const handleSave = async () => {
        if (!name.trim()) {
            toast.error('Instance name is required')
            return
        }
        if (!id.trim()) {
            toast.error('Device ID is required')
            return
        }
        if (!apiKey.trim()) {
            toast.error('API Key is required')
            return
        }

        setIsSaving(true)
        let hasChanges = false

        try {
            let currentId = instance.id

            if (id.trim() !== instance.id || apiKey.trim() !== (instance.api_key || '')) {
                const res = await updateCredentials(instance.id, id.trim(), apiKey.trim())
                currentId = res.device_id
                hasChanges = true
            }

            if (name !== instance.name) {
                await renameInstance(currentId, name)
                hasChanges = true
            }

            if (webhookUrl !== (instance.webhook_url || '')) {
                await updateWebhook(currentId, webhookUrl)
                hasChanges = true
            }

            if (proxyUri !== (instance.proxy_uri || '')) {
                await updateProxy(currentId, proxyUri)
                hasChanges = true
            }

            if (hasChanges) {
                toast.success(`Instance updated successfully`)
                onSaved()
            } else {
                onClose() // No changes, just close
            }
        } catch (e) {
            console.error(e)
            toast.error('Failed to update instance')
        } finally {
            setIsSaving(false)
        }
    }

    return (
        <div className="fixed inset-0 bg-black/60 flex items-center justify-center z-50 p-4" onClick={onClose}>
            <div
                className="bg-card border border-border rounded-xl shadow-lg w-full max-w-md flex flex-col overflow-hidden animate-in fade-in zoom-in-95 duration-200"
                onClick={e => e.stopPropagation()}
            >
                <div className="flex items-center justify-between px-6 py-4 border-b border-border bg-muted/30">
                    <h2 className="text-lg font-semibold text-foreground">Edit Instance</h2>
                    <button
                        onClick={onClose}
                        className="p-2 text-muted-foreground hover:text-foreground hover:bg-muted rounded-md transition-colors"
                    >
                        <X size={18} />
                    </button>
                </div>

                <div className="p-6 space-y-4">
                    <div>
                        <label className="text-sm font-medium text-foreground mb-1.5 block">Instance Name *</label>
                        <input
                            type="text"
                            value={name}
                            onChange={e => setName(e.target.value)}
                            className="w-full px-3 py-2 bg-background border border-border rounded-md text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
                        />
                    </div>
                    <div>
                        <label className="text-sm font-medium text-foreground mb-1.5 block">Device ID *</label>
                        <input
                            type="text"
                            value={id}
                            onChange={e => setId(e.target.value)}
                            className="w-full px-3 py-2 bg-background border border-border rounded-md text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-ring font-mono"
                        />
                    </div>
                    <div>
                        <label className="text-sm font-medium text-foreground mb-1.5 block">API Key *</label>
                        <div className="relative">
                            <input
                                type={showApiKey ? 'text' : 'password'}
                                value={apiKey}
                                onChange={e => setApiKey(e.target.value)}
                                className="w-full pl-3 pr-10 py-2 bg-background border border-border rounded-md text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-ring font-mono"
                            />
                            <button
                                type="button"
                                onClick={() => setShowApiKey(!showApiKey)}
                                className="absolute right-3 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground transition-colors"
                            >
                                {showApiKey ? <EyeOff size={16} /> : <Eye size={16} />}
                            </button>
                        </div>
                    </div>
                    <div>
                        <label className="text-sm font-medium text-foreground mb-1.5 block">Webhook URL</label>
                        <input
                            type="text"
                            value={webhookUrl}
                            onChange={e => setWebhookUrl(e.target.value)}
                            placeholder="https://n8n.example.com/webhook/..."
                            className="w-full px-3 py-2 bg-background border border-border rounded-md text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
                        />
                        <p className="text-xs text-muted-foreground mt-1">Leave blank to disable webhooks.</p>
                    </div>
                    <div>
                        <label className="text-sm font-medium text-foreground mb-1.5 block">Proxy URI</label>
                        <input
                            type="text"
                            value={proxyUri}
                            onChange={e => setProxyUri(e.target.value)}
                            placeholder="socks5://user:pass@host:port"
                            className="w-full px-3 py-2 bg-background border border-border rounded-md text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
                        />
                        <p className="text-xs text-muted-foreground mt-1">Requires instance restart. Supports HTTP and SOCKS5.</p>
                    </div>
                </div>

                <div className="flex justify-end gap-3 px-6 py-4 border-t border-border bg-muted/10">
                    <button
                        onClick={onClose}
                        className="px-4 py-2 text-sm font-medium text-muted-foreground hover:text-foreground transition-colors"
                        disabled={isSaving}
                    >
                        Cancel
                    </button>
                    <button
                        onClick={handleSave}
                        disabled={isSaving || !name.trim()}
                        className="flex items-center gap-2 px-4 py-2 bg-primary text-primary-foreground rounded-md text-sm font-medium hover:bg-primary/90 transition-colors disabled:opacity-50"
                    >
                        <Save size={16} />
                        {isSaving ? 'Saving...' : 'Save Changes'}
                    </button>
                </div>
            </div>
        </div>
    )
}

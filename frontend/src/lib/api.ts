import type { Instance } from '@/types'

export const API_BASE = '/api'

export async function fetchInstances(): Promise<Instance[]> {
    const res = await fetch(`${API_BASE}/device`)
    if (!res.ok) throw new Error('Failed to fetch instances')
    return res.json()
}

export async function createInstance(
    name: string,
    webhookUrl: string,
    proxyUri: string,
    id?: string,
    apiKey?: string
): Promise<{ device_id: string; api_key: string; name: string }> {
    const res = await fetch(`${API_BASE}/device`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name, webhook_url: webhookUrl, proxy_uri: proxyUri, id, api_key: apiKey }),
    })
    if (!res.ok) throw new Error('Failed to create instance')
    return res.json()
}

export async function updateCredentials(
    id: string,
    newId: string,
    newApiKey: string
): Promise<{ device_id: string; api_key: string }> {
    const res = await fetch(`${API_BASE}/device/${id}/credentials`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ new_id: newId, new_api_key: newApiKey }),
    })
    if (!res.ok) throw new Error('Failed to update credentials')
    return res.json()
}


export async function renameInstance(id: string, name: string): Promise<void> {
    const res = await fetch(`${API_BASE}/device/${id}/rename`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name }),
    })
    if (!res.ok) throw new Error('Failed to rename instance')
}

export async function reconnectInstance(id: string): Promise<void> {
    const res = await fetch(`${API_BASE}/device/${id}/reconnect`, {
        method: 'POST',
    })
    if (!res.ok) throw new Error('Failed to reconnect instance')
}

export async function updateWebhook(id: string, webhookUrl: string): Promise<void> {
    const res = await fetch(`${API_BASE}/device/${id}/webhook`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ webhook_url: webhookUrl }),
    })
    if (!res.ok) throw new Error('Failed to update webhook')
}

export async function updateProxy(id: string, proxyUri: string): Promise<void> {
    const res = await fetch(`${API_BASE}/device/${id}/proxy`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ proxy_uri: proxyUri }),
    })
    if (!res.ok) throw new Error('Failed to update proxy')
}

export async function deleteInstance(id: string): Promise<void> {
    const res = await fetch(`${API_BASE}/device/${id}`, { method: 'DELETE' })
    if (!res.ok) throw new Error('Failed to delete instance')
}

export function getQRCodeSSEUrl(id: string): string {
    return `${API_BASE}/device/${id}/qr`
}

export function getStatusSSEUrl(id: string): string {
    return `${API_BASE}/device/${id}/status`
}

export interface LogEntry {
    level: string
    message: string
    time: string
}

export async function fetchLogs(): Promise<LogEntry[]> {
    const res = await fetch(`${API_BASE}/logs`)
    if (!res.ok) throw new Error('Failed to fetch logs')
    return res.json()
}

export interface MessageStatGroup {
    hour: string
    direction: string
    count: number
}

export async function fetchStats(instanceId?: string): Promise<MessageStatGroup[]> {
    const url = instanceId ? `${API_BASE}/stats?id=${instanceId}` : `${API_BASE}/stats`
    const res = await fetch(url)
    if (!res.ok) throw new Error('Failed to fetch stats')
    return res.json()
}

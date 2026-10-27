import type {
    ApiKey,
    AuditEntry,
    Delivery,
    Instance,
    InstanceSettings,
    Me,
    Paged,
    ReadyStatus,
    StoredMessage,
    Webhook,
} from '@/types'
import { clearApiKey, currentKey, UNAUTHORIZED_EVENT } from '@/lib/auth'

export const API_BASE = '/api'

export class ApiError extends Error {
    status: number
    constructor(status: number, message: string) {
        super(message)
        this.status = status
    }
}

// apiFetch adds the API key to every request and turns error bodies into ApiError.
export async function apiFetch<T = unknown>(path: string, init: RequestInit = {}): Promise<T> {
    const headers = new Headers(init.headers)
    const key = currentKey()
    if (key) headers.set('X-API-Key', key)
    if (init.body && !(init.body instanceof FormData) && !headers.has('Content-Type')) {
        headers.set('Content-Type', 'application/json')
    }
    const res = await fetch(`${API_BASE}${path}`, { ...init, headers })
    if (res.status === 401) {
        clearApiKey()
        window.dispatchEvent(new Event(UNAUTHORIZED_EVENT))
    }
    if (!res.ok) {
        let message = res.statusText
        try {
            const text = await res.text()
            try {
                message = JSON.parse(text).error || text
            } catch {
                message = text || message
            }
        } catch {
            // keep statusText
        }
        throw new ApiError(res.status, message)
    }
    if (res.status === 204) return undefined as T
    return res.json() as Promise<T>
}

const json = (body: unknown) => JSON.stringify(body)

// ---- Auth ----

export function fetchMe(): Promise<Me> {
    return apiFetch('/auth/me')
}

export function fetchApiKeys(): Promise<ApiKey[]> {
    return apiFetch('/auth/keys')
}

export function createApiKey(input: { name: string; role: string; allowed_instances: string[] }): Promise<{ key: string; api_key: ApiKey }> {
    return apiFetch('/auth/keys', { method: 'POST', body: json(input) })
}

export function updateApiKey(id: string, input: Partial<{ name: string; role: string; allowed_instances: string[]; revoked: boolean }>): Promise<ApiKey> {
    return apiFetch(`/auth/keys/${id}`, { method: 'PUT', body: json(input) })
}

export function deleteApiKey(id: string): Promise<void> {
    return apiFetch(`/auth/keys/${id}`, { method: 'DELETE' })
}

export function fetchAudit(params: { limit?: number; offset?: number; instance?: string }): Promise<Paged<AuditEntry>> {
    const q = new URLSearchParams()
    if (params.limit) q.set('limit', String(params.limit))
    if (params.offset) q.set('offset', String(params.offset))
    if (params.instance) q.set('instance', params.instance)
    return apiFetch(`/audit?${q}`)
}

export function fetchReady(): Promise<ReadyStatus> {
    // /ready answers 503 with the same body when a dependency is down.
    return fetch(`${API_BASE}/ready`).then(r => r.json())
}

// ---- Instances ----

export async function fetchInstances(): Promise<Instance[]> {
    return apiFetch('/device')
}

export async function createInstance(
    name: string,
    webhookUrl: string,
    proxyUri: string,
    id?: string,
    apiKey?: string
): Promise<{ device_id: string; api_key: string; name: string }> {
    return apiFetch('/device', {
        method: 'POST',
        body: json({ name, webhook_url: webhookUrl, proxy_uri: proxyUri, id, api_key: apiKey }),
    })
}

export async function updateCredentials(
    id: string,
    newId: string,
    newApiKey: string
): Promise<{ device_id: string; api_key: string }> {
    return apiFetch(`/device/${id}/credentials`, {
        method: 'PUT',
        body: json({ new_id: newId, new_api_key: newApiKey }),
    })
}

export async function renameInstance(id: string, name: string): Promise<void> {
    await apiFetch(`/device/${id}/rename`, { method: 'PUT', body: json({ name }) })
}

export async function reconnectInstance(id: string): Promise<void> {
    await apiFetch(`/device/${id}/reconnect`, { method: 'POST' })
}

export async function updateWebhook(id: string, webhookUrl: string): Promise<void> {
    await apiFetch(`/device/${id}/webhook`, { method: 'PUT', body: json({ webhook_url: webhookUrl }) })
}

export async function updateProxy(id: string, proxyUri: string): Promise<void> {
    await apiFetch(`/device/${id}/proxy`, { method: 'PUT', body: json({ proxy_uri: proxyUri }) })
}

export async function deleteInstance(id: string): Promise<void> {
    await apiFetch(`/device/${id}`, { method: 'DELETE' })
}

export async function logoutInstance(id: string): Promise<void> {
    await apiFetch(`/device/${id}/logout`, { method: 'POST' })
}

export async function requestPairCode(id: string, phone: string): Promise<{ pair_code: string }> {
    return apiFetch(`/device/${id}/pair-code`, { method: 'POST', body: json({ phone }) })
}

export function fetchInstanceSettings(id: string): Promise<InstanceSettings> {
    return apiFetch(`/device/${id}/settings`)
}

export function saveInstanceSettings(id: string, settings: InstanceSettings): Promise<InstanceSettings> {
    return apiFetch(`/device/${id}/settings`, { method: 'PUT', body: json(settings) })
}

export function fetchDeviceStatus(id: string): Promise<{ status: string }> {
    return apiFetch(`/device/${id}/status`)
}

// EventSource cannot send headers, so the key goes in the query string.
export function getQRCodeSSEUrl(id: string): string {
    const key = currentKey()
    return `${API_BASE}/device/${id}/qr${key ? `?api_key=${encodeURIComponent(key)}` : ''}`
}

export function getStatusSSEUrl(id: string): string {
    return `${API_BASE}/device/${id}/status`
}

// ---- Messages ----

export function fetchMessages(id: string, params: { chat?: string; limit?: number; offset?: number }): Promise<Paged<StoredMessage>> {
    const q = new URLSearchParams()
    if (params.chat) q.set('chat', params.chat)
    if (params.limit) q.set('limit', String(params.limit))
    if (params.offset) q.set('offset', String(params.offset))
    return apiFetch(`/device/${id}/messages?${q}`)
}

// ---- Webhooks ----

export function fetchWebhooks(instance?: string): Promise<Webhook[]> {
    return apiFetch(`/webhooks${instance ? `?instance=${encodeURIComponent(instance)}` : ''}`)
}

export function createWebhook(input: Partial<Webhook> & { secret?: string }): Promise<Webhook> {
    return apiFetch('/webhooks', { method: 'POST', body: json(input) })
}

export function updateWebhookConfig(id: string, input: Partial<Webhook> & { secret?: string }): Promise<Webhook> {
    return apiFetch(`/webhooks/${id}`, { method: 'PUT', body: json(input) })
}

export function deleteWebhook(id: string): Promise<void> {
    return apiFetch(`/webhooks/${id}`, { method: 'DELETE' })
}

export function testWebhook(id: string): Promise<{ delivery_id: number }> {
    return apiFetch(`/webhooks/${id}/test`, { method: 'POST' })
}

export function fetchDeliveries(params: { instance?: string; status?: string; limit?: number; offset?: number }): Promise<Paged<Delivery>> {
    const q = new URLSearchParams()
    if (params.instance) q.set('instance', params.instance)
    if (params.status) q.set('status', params.status)
    if (params.limit) q.set('limit', String(params.limit))
    if (params.offset) q.set('offset', String(params.offset))
    return apiFetch(`/webhooks/deliveries?${q}`)
}

export function fetchDelivery(id: number): Promise<Delivery> {
    return apiFetch(`/webhooks/deliveries/${id}`)
}

export function redriveDelivery(id: number): Promise<{ redriven: number }> {
    return apiFetch(`/webhooks/deliveries/${id}/redrive`, { method: 'POST' })
}

export function redriveFailed(instance?: string): Promise<{ redriven: number }> {
    return apiFetch('/webhooks/deliveries/redrive', { method: 'POST', body: json(instance ? { instance_id: instance } : {}) })
}

export function fetchDeliveryStats(instance?: string): Promise<Record<string, number>> {
    return apiFetch(`/webhooks/stats${instance ? `?instance=${encodeURIComponent(instance)}` : ''}`)
}

// ---- Logs & stats ----

export interface LogEntry {
    level: string
    message: string
    time: string
}

export async function fetchLogs(): Promise<LogEntry[]> {
    return apiFetch('/logs')
}

export interface MessageStatGroup {
    hour: string
    direction: string
    count: number
}

export async function fetchStats(instanceId?: string): Promise<MessageStatGroup[]> {
    return apiFetch(instanceId ? `/stats?id=${instanceId}` : '/stats')
}

export interface InstanceSettings {
    reject_calls: boolean
    reject_call_message?: string
    always_online: boolean
    read_messages: boolean
    ignore_groups: boolean
    ignore_status: boolean
    msg_delay_min: number
    msg_delay_max: number
}

export interface Instance {
    id: string
    name: string
    status: string
    webhook_url: string
    proxy_uri: string
    phone?: string
    jid?: string
    api_key?: string
    created_at?: string
    paired_at?: string
    last_connected_at?: string
    last_disconnected_at?: string
    last_error?: string
    settings?: InstanceSettings
}

export type Role = 'admin' | 'operator' | 'viewer' | 'instance'

export interface Principal {
    kind: 'api_key' | 'instance' | 'anonymous'
    id: string
    name: string
    role: Role
    allowed_instances: string[] | null
    instance_id?: string
}

export interface Me {
    principal: Principal
    auth_required: boolean
    version: string
}

export interface ApiKey {
    id: string
    name: string
    prefix: string
    role: Role
    allowed_instances: string[]
    created_by?: string
    created_at: string
    last_used_at?: string
    expires_at?: string
    revoked_at?: string
}

export interface AuditEntry {
    id: number
    created_at: string
    actor_type: string
    actor_id?: string
    actor_name?: string
    method: string
    path: string
    route?: string
    instance_id?: string
    status_code: number
    ip?: string
}

export interface Paged<T> {
    total: number
    items: T[]
}

export interface Webhook {
    id: string
    instance_id: string | null
    url: string
    events: string[]
    exclude_messages: string[]
    has_secret: boolean
    headers: Record<string, string>
    enabled: boolean
    created_at: string
}

export interface Delivery {
    id: number
    webhook_id?: string
    instance_id?: string
    event: string
    url: string
    body?: string
    status: 'pending' | 'delivering' | 'retrying' | 'delivered' | 'failed'
    attempts: number
    max_attempts: number
    next_attempt_at: string
    last_status_code?: number
    last_error?: string
    duration_ms?: number
    created_at: string
    delivered_at?: string
}

export interface StoredMessage {
    instance_id: string
    id: string
    chat_jid: string
    sender_jid?: string
    from_me: boolean
    is_group: boolean
    type: string
    text?: string
    push_name?: string
    status: string
    sent_by_api: boolean
    edited: boolean
    timestamp: string
    media_key?: string
}

export interface ReadyStatus {
    status: string
    components: Record<string, { status: string; error?: string; latency?: string }>
    instances: { connected: number; total: number }
}

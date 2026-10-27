const STORAGE_KEY = 'wmb_api_key'

export const UNAUTHORIZED_EVENT = 'wmb:unauthorized'

export function getApiKey(): string {
    try {
        return localStorage.getItem(STORAGE_KEY) || ''
    } catch {
        return ''
    }
}

export function setApiKey(key: string) {
    try {
        localStorage.setItem(STORAGE_KEY, key)
    } catch {
        // storage unavailable (private mode): the key lives only for this page load
    }
    memoryKey = key
}

export function clearApiKey() {
    try {
        localStorage.removeItem(STORAGE_KEY)
    } catch {
        // ignore
    }
    memoryKey = ''
}

let memoryKey = ''

export function currentKey(): string {
    return getApiKey() || memoryKey
}

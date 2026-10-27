import { client } from '~/api/client.gen'
import type { ErrorModel } from '~/api'

let configured = false

/** Configures the generated client once: same-origin, throw on errors, notice 401s. */
export function setupApiClient() {
  if (configured) return
  configured = true
  client.setConfig({ baseUrl: '', credentials: 'same-origin', throwOnError: true })
  client.interceptors.response.use((res) => {
    if (res.status === 401 && !res.url.endsWith('/api/auth/login')) {
      useAuth().value.needLogin = true
    }
    return res
  })
}

/** errorMessage extracts a readable message from a thrown API error. */
export function errorMessage(e: unknown): string {
  if (!e) return 'Unknown error'
  if (typeof e === 'string') return e
  const m = e as Partial<ErrorModel> & { message?: string }
  if (m.detail) return m.detail
  if (m.title) return m.title
  if (m.message) return m.message
  return String(e)
}

export function useAuth() {
  return useState('auth', () => ({ needLogin: false }))
}

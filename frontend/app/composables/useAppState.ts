import { getInfo, listProviders, listSessions } from '~/api'
import type { ProviderInfo, ServerInfo, Session } from '~/api'

/** Server info (version, auth, idle timeout). */
export function useServerInfo() {
  return useState<ServerInfo | null>('serverInfo', () => null)
}

export async function loadServerInfo() {
  const info = useServerInfo()
  const { data } = await getInfo()
  info.value = data
  if (data.authRequired && !data.authenticated) useAuth().value.needLogin = true
  return data
}

/** The installed CLIs. */
export function useProviders() {
  return useState<ProviderInfo[]>('providers', () => [])
}

let providersLoading: Promise<void> | null = null

export function loadProviders(force = false) {
  const providers = useProviders()
  if (!providersLoading || force) {
    providersLoading = listProviders()
      .then(({ data }) => {
        providers.value = data
      })
      .catch((e) => {
        providersLoading = null
        report(e)
      })
  }
  return providersLoading
}

export function providerInfo(name: string): ProviderInfo {
  return (
    useProviders().value.find((p) => p.name === name) ?? {
      name,
      displayName: name,
      models: [],
      efforts: [],
      modes: [],
    }
  )
}

/** All sessions, refreshed by polling while the page is visible. */
export function useSessions() {
  return useState<Session[]>('sessions', () => [])
}

export async function refreshSessions() {
  try {
    const { data } = await listSessions()
    useSessions().value = data
  } catch {
    // shown by the auth gate or the next action
  }
}

/** Replaces one session in the list (after an action or a stream update). */
export function patchSession(s: Session) {
  const list = useSessions()
  const i = list.value.findIndex((x) => x.id === s.id)
  if (i >= 0) {
    const next = list.value.slice()
    next[i] = s
    list.value = next
  } else {
    list.value = [s, ...list.value]
  }
}

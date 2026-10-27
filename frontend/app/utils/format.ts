export function fmtSize(n?: number): string {
  if (!n) return ''
  const u = ['B', 'KB', 'MB', 'GB']
  let i = 0
  while (n >= 1024 && i < u.length - 1) {
    n /= 1024
    i++
  }
  return `${n.toFixed(i ? 1 : 0)} ${u[i]}`
}

/** relTime renders a timestamp as "3m ago" style text. */
export function relTime(iso: string | undefined, now = Date.now()): string {
  if (!iso) return ''
  const s = Math.round((now - new Date(iso).getTime()) / 1000)
  if (s < 45) return 'just now'
  if (s < 3600) return `${Math.round(s / 60)}m ago`
  if (s < 86400) return `${Math.round(s / 3600)}h ago`
  if (s < 86400 * 30) return `${Math.round(s / 86400)}d ago`
  return new Date(iso).toLocaleDateString()
}

/** countdown renders the time until iso as m:ss. */
export function countdown(iso: string, now = Date.now()): string {
  const s = Math.max(0, Math.round((new Date(iso).getTime() - now) / 1000))
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`
}

export function shortPath(path: string, home?: string): string {
  if (home && (path === home || path.startsWith(home + '/'))) return '~' + path.slice(home.length)
  return path
}

export function basename(path: string): string {
  const p = path.replace(/\/+$/, '')
  return p.slice(p.lastIndexOf('/') + 1) || p
}

export const STATUS_LABEL: Record<string, string> = {
  stopped: 'Stopped',
  starting: 'Starting',
  idle: 'Ready',
  busy: 'Working',
  waiting: 'Needs you',
}

export function decodeBase64(b64: string): Uint8Array {
  const bin = atob(b64)
  const out = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return out
}

export function fileURL(sessionId: string, fileId: string, download = false): string {
  return `/api/sessions/${encodeURIComponent(sessionId)}/files/${encodeURIComponent(fileId)}${download ? '?download=true' : ''}`
}

/** A one-line summary of a tool call's input. */
export function toolSummary(input: unknown): string {
  if (input == null) return ''
  if (typeof input === 'string') return input.split('\n')[0] ?? ''
  if (typeof input === 'object') {
    const v = input as Record<string, unknown>
    for (const k of ['command', 'file_path', 'url', 'pattern', 'path', 'description', 'query', 'prompt']) {
      if (typeof v[k] === 'string' && v[k]) return (v[k] as string).split('\n')[0] ?? ''
    }
  }
  return JSON.stringify(input)
}

const ATTACHED = 'Attached files:'
const REF = /^- \$AGENT_UPLOAD_DIR\/(.+)$/

/**
 * splitAttachments separates the "Attached files:" list that the server
 * appends to prompts with uploads from the text the user typed.
 */
export function splitAttachments(text: string): { text: string; files: string[] } {
  const at = text.lastIndexOf(ATTACHED)
  if (at < 0 || (at > 0 && !text.slice(0, at).endsWith('\n'))) return { text, files: [] }
  const lines = text.slice(at + ATTACHED.length).split('\n').filter((l) => l.trim())
  const files: string[] = []
  for (const l of lines) {
    const m = REF.exec(l.trim())
    if (!m) return { text, files: [] }
    files.push(m[1]!)
  }
  return files.length ? { text: text.slice(0, at).trimEnd(), files } : { text, files: [] }
}

export function uploadURL(sessionId: string, name: string, download = false): string {
  return `/api/sessions/${encodeURIComponent(sessionId)}/uploads/${encodeURIComponent(name)}${download ? '?download=true' : ''}`
}

export function isImageName(name: string): boolean {
  return /\.(png|jpe?g|gif|webp|bmp|svg|avif)$/i.test(name)
}

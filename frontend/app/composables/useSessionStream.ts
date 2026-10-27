import { streamEvents, streamTerminal } from '~/api'
import type { Event as SessionEvent, Replayed, Session, TerminalReset } from '~/api'

export interface SessionStreamHandlers {
  onSummary(s: Session): void
  onEvent(e: SessionEvent): void
  onReplayed(r: Replayed): void
}

/**
 * openEventStream follows a session's events with the generated SSE client.
 * It reconnects with the last seen seq, so nothing is lost or repeated.
 * Returns a function that closes the stream.
 */
export function openEventStream(id: string, h: SessionStreamHandlers): () => void {
  const ctrl = new AbortController()
  let lastSeq = 0
  ;(async () => {
    while (!ctrl.signal.aborted) {
      try {
        const { stream } = await streamEvents({
          path: { id },
          query: lastSeq ? { after: lastSeq } : undefined,
          signal: ctrl.signal,
          sseMaxRetryAttempts: 1,
          onSseEvent: (m) => {
            switch (m.event) {
              case 'summary':
                h.onSummary(m.data as unknown as Session)
                break
              case 'event': {
                const e = m.data as unknown as SessionEvent
                if (e.seq <= lastSeq) return
                lastSeq = e.seq
                h.onEvent(e)
                break
              }
              case 'replayed':
                h.onReplayed(m.data as unknown as Replayed)
                break
            }
          },
        })
        for await (const _ of stream) {
          // handled in onSseEvent, which also sees the event name
        }
      } catch {
        // reconnect below
      }
      if (!ctrl.signal.aborted) await sleep(1500)
    }
  })()
  return () => ctrl.abort()
}

export interface TerminalStreamHandlers {
  onReset(r: TerminalReset): void
  onData(data: Uint8Array): void
  onOffline(): void
}

/** openTerminalStream follows the CLI's terminal output. */
export function openTerminalStream(id: string, h: TerminalStreamHandlers): () => void {
  const ctrl = new AbortController()
  ;(async () => {
    while (!ctrl.signal.aborted) {
      try {
        const { stream } = await streamTerminal({
          path: { id },
          signal: ctrl.signal,
          sseMaxRetryAttempts: 1,
          onSseEvent: (m) => {
            const d = m.data as unknown as { data?: string }
            switch (m.event) {
              case 'reset':
                h.onReset(m.data as unknown as TerminalReset)
                break
              case 'data':
                if (d?.data) h.onData(decodeBase64(d.data))
                break
              case 'offline':
                h.onOffline()
                break
            }
          },
        })
        for await (const _ of stream) {
          // handled in onSseEvent
        }
      } catch {
        // reconnect below
      }
      if (!ctrl.signal.aborted) await sleep(1500)
    }
  })()
  return () => ctrl.abort()
}

function sleep(ms: number) {
  return new Promise((r) => setTimeout(r, ms))
}

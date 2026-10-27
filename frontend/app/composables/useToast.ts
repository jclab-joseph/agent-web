export interface Toast {
  id: number
  text: string
  kind: 'info' | 'error'
}

let nextId = 1

export function useToasts() {
  return useState<Toast[]>('toasts', () => [])
}

/** toast shows a short message; errors stay a little longer. */
export function toast(text: string, kind: Toast['kind'] = 'info') {
  const list = useToasts()
  const t = { id: nextId++, text, kind }
  list.value = [...list.value.slice(-3), t]
  setTimeout(() => {
    list.value = list.value.filter((x) => x.id !== t.id)
  }, kind === 'error' ? 6000 : 3500)
}

/** report shows an API error as a toast. */
export function report(e: unknown) {
  toast(errorMessage(e), 'error')
}

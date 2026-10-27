<script setup lang="ts">
import { terminalInput } from '~/api'
import type { Terminal } from '@xterm/xterm'

const props = defineProps<{ sessionId: string; running: boolean }>()
const emit = defineEmits<{ start: [] }>()

const host = ref<HTMLElement>()
const offline = ref(false)
let term: Terminal | null = null
let close: (() => void) | null = null
let chain: Promise<unknown> = Promise.resolve()

onMounted(async () => {
  const { Terminal } = await import('@xterm/xterm')
  if (!host.value) return
  term = new Terminal({
    fontFamily: 'ui-monospace, Menlo, Consolas, monospace',
    fontSize: 13,
    cursorBlink: true,
    scrollback: 0,
    theme: { background: '#141413', foreground: '#e8e6e1' },
  })
  term.open(host.value)
  term.onData((data) => {
    const id = props.sessionId
    chain = chain.then(() => terminalInput({ path: { id }, body: { data } }).catch(report))
  })
  close = openTerminalStream(props.sessionId, {
    onReset(r) {
      offline.value = false
      term?.reset()
      term?.resize(r.cols, r.rows)
      term?.write(decodeBase64(r.data))
    },
    onData(d) {
      term?.write(d)
    },
    onOffline() {
      offline.value = true
    },
  })
  term.focus()
})

onBeforeUnmount(() => {
  close?.()
  term?.dispose()
})
</script>

<template>
  <div class="term-wrap">
    <div ref="host" class="term" />
    <div v-if="offline || !running" class="offline">
      <p>The CLI is not running.</p>
      <button class="btn primary" @click="emit('start')">Start CLI</button>
    </div>
  </div>
</template>

<style scoped>
.term-wrap {
  position: relative;
  height: 100%;
  overflow: auto;
  background: #141413;
  padding: 10px;
}
.term {
  width: max-content;
}
.offline {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 10px;
  background: rgb(20 20 19 / 0.82);
  color: #e8e6e1;
}
</style>

<script setup lang="ts">
import { interruptSession, runCommand, sendPrompt } from '~/api'
import type { Session } from '~/api'

const props = defineProps<{ session: Session }>()
const emit = defineEmits<{ sent: [] }>()

const text = ref('')
const sending = ref(false)
const ta = ref<HTMLTextAreaElement>()
const draftKey = computed(() => `agent-web:draft:${props.session.id}`)

onMounted(() => {
  try {
    text.value = localStorage.getItem(draftKey.value) ?? ''
  } catch {
    // ignore
  }
  nextTick(resize)
})

watch(text, (v) => {
  try {
    if (v) localStorage.setItem(draftKey.value, v)
    else localStorage.removeItem(draftKey.value)
  } catch {
    // ignore
  }
  nextTick(resize)
})

function resize() {
  const el = ta.value
  if (!el) return
  el.style.height = 'auto'
  el.style.height = Math.min(el.scrollHeight, 260) + 'px'
}

const isCommand = computed(() => /^\/\S/.test(text.value.trim()) && !text.value.trim().includes('\n'))
const busy = computed(() => props.session.status === 'busy')
const placeholder = computed(() => {
  if (props.session.status === 'stopped')
    return props.session.resumable ? 'Message… (the CLI resumes the conversation)' : 'Message… (starts the CLI)'
  if (busy.value) return 'Working… you can type your next message'
  return 'Message, or /command…  (Enter to send, Shift+Enter for a new line)'
})

async function submit() {
  const value = text.value.trim()
  if (!value || sending.value) return
  sending.value = true
  const id = props.session.id
  try {
    const { data } = isCommand.value
      ? await runCommand({ path: { id }, body: { command: value } })
      : await sendPrompt({ path: { id }, body: { text: value } })
    patchSession(data)
    text.value = ''
    emit('sent')
  } catch (e) {
    report(e)
  } finally {
    sending.value = false
    ta.value?.focus()
  }
}

async function interrupt() {
  try {
    const { data } = await interruptSession({ path: { id: props.session.id } })
    patchSession(data)
  } catch (e) {
    report(e)
  }
}

function onKey(e: KeyboardEvent) {
  if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
    e.preventDefault()
    submit()
  }
}

defineExpose({ focus: () => ta.value?.focus() })
</script>

<template>
  <form class="composer" @submit.prevent="submit">
    <textarea
      ref="ta"
      v-model="text"
      class="textarea"
      :class="{ command: isCommand }"
      rows="1"
      :placeholder="placeholder"
      @keydown="onKey"
    />
    <button v-if="busy" type="button" class="btn danger" title="Interrupt the running turn (Esc)" @click="interrupt">Stop</button>
    <button class="btn primary" :disabled="sending || !text.trim()">
      {{ sending ? (session.status === 'stopped' ? 'Resuming…' : 'Sending…') : isCommand ? 'Run' : 'Send' }}
    </button>
  </form>
</template>

<style scoped>
.composer {
  display: flex;
  align-items: flex-end;
  gap: 8px;
  width: 100%;
  max-width: 900px;
  margin: 0 auto;
  padding: 10px 20px 14px;
}
.textarea {
  flex: 1;
  min-height: 40px;
  max-height: 260px;
  padding: 9px 12px;
  border-radius: var(--radius);
  resize: none;
  line-height: 1.45;
}
.textarea.command {
  font-family: var(--mono);
  font-size: 13px;
}
.btn {
  height: 40px;
}
</style>

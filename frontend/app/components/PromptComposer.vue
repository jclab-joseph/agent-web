<script setup lang="ts">
import { interruptSession, runCommand, sendPrompt, uploadFiles } from '~/api'
import type { Session, Upload } from '~/api'

const props = defineProps<{ session: Session }>()
const emit = defineEmits<{ sent: [] }>()

const text = ref('')
const sending = ref(false)
const ta = ref<HTMLTextAreaElement>()
const picker = ref<HTMLInputElement>()
const draftKey = computed(() => `agent-web:draft:${props.session.id}`)

/** A file attached to the next prompt; uploaded as soon as it is added. */
interface Pending {
  key: number
  name: string
  size: number
  preview?: string
  state: 'uploading' | 'ready' | 'error'
  upload?: Upload
  error?: string
}
const files = ref<Pending[]>([])
let nextKey = 1

onMounted(() => {
  try {
    text.value = localStorage.getItem(draftKey.value) ?? ''
  } catch {
    // ignore
  }
  nextTick(resize)
})

onBeforeUnmount(() => files.value.forEach((f) => f.preview && URL.revokeObjectURL(f.preview)))

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

/** addFiles uploads files and lists them as attachments of the next prompt. */
async function addFiles(list: FileList | File[]) {
  const batch = Array.from(list)
  if (!batch.length) return
  const items = batch.map<Pending>((f) => ({
    key: nextKey++,
    name: f.name || 'pasted',
    size: f.size,
    preview: f.type.startsWith('image/') ? URL.createObjectURL(f) : undefined,
    state: 'uploading',
  }))
  files.value = [...files.value, ...items]
  const keys = new Set(items.map((i) => i.key))
  const update = (f: (p: Pending, i: number) => Pending) => {
    let i = 0
    files.value = files.value.map((p) => (keys.has(p.key) ? f(p, i++) : p))
  }
  try {
    const { data } = await uploadFiles({ path: { id: props.session.id }, body: { files: batch } })
    update((p, i) => ({ ...p, state: 'ready', upload: data[i], name: data[i]?.name ?? p.name }))
  } catch (e) {
    const msg = errorMessage(e)
    update((p) => ({ ...p, state: 'error', error: msg }))
    report(e)
  }
}

function removeFile(key: number) {
  const f = files.value.find((x) => x.key === key)
  if (f?.preview) URL.revokeObjectURL(f.preview)
  files.value = files.value.filter((x) => x.key !== key)
}

function onPick(e: Event) {
  const input = e.target as HTMLInputElement
  if (input.files) addFiles(input.files)
  input.value = ''
}

function onPaste(e: ClipboardEvent) {
  const pasted = Array.from(e.clipboardData?.files ?? [])
  if (!pasted.length) return
  e.preventDefault()
  addFiles(pasted)
}

const uploading = computed(() => files.value.some((f) => f.state === 'uploading'))
const ready = computed(() => files.value.filter((f) => f.state === 'ready' && f.upload).map((f) => f.upload!.name))
const isCommand = computed(() => !files.value.length && /^\/\S/.test(text.value.trim()) && !text.value.trim().includes('\n'))
const busy = computed(() => props.session.status === 'busy')
const canSend = computed(() => !sending.value && !uploading.value && (!!text.value.trim() || ready.value.length > 0))
const placeholder = computed(() => {
  if (props.session.status === 'stopped')
    return props.session.resumable ? 'Message… (the CLI resumes the conversation)' : 'Message… (starts the CLI)'
  if (busy.value) return 'Working… you can type your next message'
  return 'Message, or /command…  (Enter to send, Shift+Enter for a new line, paste or drop files)'
})

async function submit() {
  const value = text.value.trim()
  if (!canSend.value) return
  sending.value = true
  const id = props.session.id
  try {
    const { data } = isCommand.value
      ? await runCommand({ path: { id }, body: { command: value } })
      : await sendPrompt({ path: { id }, body: { text: value, files: ready.value.length ? ready.value : undefined } })
    patchSession(data)
    text.value = ''
    files.value.forEach((f) => f.preview && URL.revokeObjectURL(f.preview))
    files.value = []
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

defineExpose({ focus: () => ta.value?.focus(), addFiles })
</script>

<template>
  <form class="composer" @submit.prevent="submit">
    <div v-if="files.length" class="files">
      <div v-for="f in files" :key="f.key" class="file" :class="f.state" :title="f.error || f.upload?.ref || f.name">
        <img v-if="f.preview" :src="f.preview" alt="" />
        <span v-else class="icon" aria-hidden="true">📎</span>
        <span class="meta">
          <span class="name truncate">{{ f.name }}</span>
          <span class="sub">{{ f.state === 'uploading' ? 'Uploading…' : f.state === 'error' ? 'Failed' : fmtSize(f.size) }}</span>
        </span>
        <button type="button" class="x" :aria-label="`Remove ${f.name}`" @click="removeFile(f.key)">×</button>
      </div>
    </div>
    <div class="row">
      <button type="button" class="btn icon attach" title="Attach files" aria-label="Attach files" @click="picker?.click()">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M21.44 11.05l-9.19 9.19a6 6 0 01-8.49-8.49l9.19-9.19a4 4 0 015.66 5.66l-9.2 9.19a2 2 0 01-2.83-2.83l8.49-8.48" />
        </svg>
      </button>
      <input ref="picker" type="file" multiple hidden @change="onPick" />
      <textarea
        ref="ta"
        v-model="text"
        class="textarea"
        :class="{ command: isCommand }"
        rows="1"
        :placeholder="placeholder"
        @keydown="onKey"
        @paste="onPaste"
      />
      <button v-if="busy" type="button" class="btn danger" title="Interrupt the running turn" @click="interrupt">Stop</button>
      <button class="btn primary" :disabled="!canSend">
        {{ sending ? (session.status === 'stopped' ? 'Resuming…' : 'Sending…') : isCommand ? 'Run' : 'Send' }}
      </button>
    </div>
  </form>
</template>

<style scoped>
.composer {
  display: flex;
  flex-direction: column;
  gap: 8px;
  width: 100%;
  max-width: 900px;
  margin: 0 auto;
  padding: 10px 20px 14px;
}
.row {
  display: flex;
  align-items: flex-end;
  gap: 8px;
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
.attach {
  width: 40px;
  color: var(--text-2);
}
.files {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.file {
  display: flex;
  align-items: center;
  gap: 8px;
  max-width: 260px;
  padding: 4px 4px 4px 4px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface);
}
.file.uploading {
  opacity: 0.65;
}
.file.error {
  border-color: var(--danger);
}
.file img,
.file .icon {
  flex-shrink: 0;
  width: 36px;
  height: 36px;
  border-radius: 4px;
  object-fit: cover;
}
.file .icon {
  display: grid;
  place-items: center;
  background: var(--surface-2);
}
.meta {
  display: flex;
  flex-direction: column;
  min-width: 0;
  line-height: 1.25;
}
.name {
  font-size: 12.5px;
  font-weight: 600;
}
.sub {
  font-size: 11.5px;
  color: var(--text-3);
}
.file.error .sub {
  color: var(--danger);
}
.x {
  flex-shrink: 0;
  width: 24px;
  height: 24px;
  border: 0;
  border-radius: 4px;
  background: none;
  color: var(--text-3);
  cursor: pointer;
  font-size: 16px;
}
.x:hover {
  background: var(--surface-2);
  color: var(--text);
}
</style>

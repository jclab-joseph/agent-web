<script setup lang="ts">
import { changeSettings, deleteSession, getHistory, getSession, startSession, stopSession, updateSession } from '~/api'
import type { Event as SessionEvent, Session } from '~/api'

const route = useRoute()
const id = computed(() => String(route.params.id))
const info = useServerInfo()
const now = useNow(1000)

const session = ref<Session | null>(null)
const events = shallowRef<SessionEvent[]>([])
const hasMore = ref(false)
const firstSeq = ref(0)
const loadingMore = ref(false)
const notFound = ref(false)
const tab = ref<'chat' | 'terminal'>('chat')
const starting = ref(false)
const composer = ref<{ focus(): void }>()

let closeStream: (() => void) | null = null
let replayedOnce = false

// Incoming events are applied once per frame, so a long replay does not
// copy the list for every event.
let buffer: SessionEvent[] = []
let flushQueued = false
function queueEvent(e: SessionEvent) {
  buffer.push(e)
  if (flushQueued) return
  flushQueued = true
  requestAnimationFrame(() => {
    flushQueued = false
    events.value = events.value.concat(buffer)
    buffer = []
  })
}

function connect() {
  closeStream?.()
  buffer = []
  events.value = []
  hasMore.value = false
  firstSeq.value = 0
  replayedOnce = false
  notFound.value = false
  session.value = null
  const sid = id.value
  getSession({ path: { id: sid } })
    .then(({ data }) => {
      if (sid !== id.value) return
      session.value = data
      closeStream = openEventStream(sid, {
        onSummary(s) {
          session.value = s
          patchSession(s)
        },
        onEvent: queueEvent,
        onReplayed(r) {
          if (replayedOnce) return
          replayedOnce = true
          hasMore.value = r.hasMore
          firstSeq.value = r.firstSeq
        },
      })
    })
    .catch((e) => {
      notFound.value = true
      report(e)
    })
}

async function loadMore() {
  if (!firstSeq.value) return
  loadingMore.value = true
  try {
    const { data } = await getHistory({ path: { id: id.value }, query: { before: firstSeq.value, limit: 200 } })
    events.value = [...data, ...events.value]
    hasMore.value = data.length === 200
    firstSeq.value = data[0]?.seq ?? firstSeq.value
  } catch (e) {
    report(e)
  } finally {
    loadingMore.value = false
  }
}

watch(id, connect, { immediate: true })
onBeforeUnmount(() => closeStream?.())

// --- header actions ---

const editingTitle = ref(false)
const titleDraft = ref('')
const titleInput = ref<HTMLInputElement>()

function editTitle() {
  titleDraft.value = session.value?.title ?? ''
  editingTitle.value = true
  nextTick(() => titleInput.value?.select())
}

async function saveTitle() {
  if (!editingTitle.value) return
  editingTitle.value = false
  if (titleDraft.value === session.value?.title) return
  try {
    const { data } = await updateSession({ path: { id: id.value }, body: { title: titleDraft.value } })
    session.value = data
    patchSession(data)
  } catch (e) {
    report(e)
  }
}

async function start() {
  starting.value = true
  try {
    const { data } = await startSession({ path: { id: id.value } })
    session.value = data
    patchSession(data)
  } catch (e) {
    report(e)
  } finally {
    starting.value = false
  }
}

async function stop() {
  try {
    const { data } = await stopSession({ path: { id: id.value } })
    session.value = data
    patchSession(data)
  } catch (e) {
    report(e)
  }
}

async function remove() {
  if (!confirm('Delete this session and its history here? The CLI keeps its own transcript.')) return
  try {
    await deleteSession({ path: { id: id.value } })
    useSessions().value = useSessions().value.filter((s) => s.id !== id.value)
    await navigateTo('/')
  } catch (e) {
    report(e)
  }
}

async function setting(field: 'model' | 'effort' | 'mode', value: string) {
  if (!session.value || session.value.settings[field] === value) return
  const running = session.value.running
  if (running) toast(`Applying ${field}…`)
  try {
    const { data } = await changeSettings({ path: { id: id.value }, body: { [field]: value } })
    session.value = data
    patchSession(data)
    toast(running ? `${field} set to ${value || 'default'}` : `${field} will be ${value || 'default'} when the CLI starts`)
  } catch (e) {
    report(e)
  }
}

const provider = computed(() => providerInfo(session.value?.provider ?? ''))
const modelDraft = ref('')
watch(
  () => session.value?.settings.model,
  (m) => (modelDraft.value = m ?? ''),
  { immediate: true },
)

const autoStop = computed(() => (session.value?.autoStopAt ? countdown(session.value.autoStopAt, now.value) : ''))
const title = computed(() => session.value?.title || (session.value ? basename(session.value.cwd) : ''))

useHead(() => ({ title: title.value ? `${title.value} · agent-web` : 'agent-web' }))
</script>

<template>
  <div v-if="notFound" class="gone">
    <p>This session does not exist.</p>
    <NuxtLink to="/" class="btn">Back</NuxtLink>
  </div>
  <div v-else-if="session" class="page">
    <header class="head">
      <div class="row top">
        <NuxtLink to="/" class="btn ghost icon back" aria-label="Back to sessions">←</NuxtLink>
        <input
          v-if="editingTitle"
          ref="titleInput"
          v-model="titleDraft"
          class="input title-input"
          maxlength="200"
          @keydown.enter="saveTitle"
          @keydown.esc="editingTitle = false"
          @blur="saveTitle"
        />
        <h1 v-else class="title truncate" title="Click to rename" @click="editTitle">{{ title }}</h1>
        <span class="status" :class="session.status">{{ STATUS_LABEL[session.status] }}</span>
        <span v-if="autoStop" class="chip" title="The idle CLI is stopped then; your next message resumes it">auto-stop {{ autoStop }}</span>
        <div class="spacer" />
        <a v-if="session.settings.remoteControlUrl" class="btn sm" :href="session.settings.remoteControlUrl" target="_blank" rel="noopener">Remote Control ↗</a>
        <button v-if="!session.running" class="btn sm primary" :disabled="starting" @click="start">
          {{ starting ? 'Starting…' : session.resumable ? 'Resume CLI' : 'Start CLI' }}
        </button>
        <button v-else class="btn sm" title="Stop the CLI; the next message resumes it" @click="stop">Stop CLI</button>
        <button class="btn sm danger" @click="remove">Delete</button>
      </div>
      <div class="row sub">
        <span class="chip">{{ provider.displayName }}</span>
        <span class="mono path truncate" :title="session.cwd">{{ shortPath(session.cwd, info?.home) }}</span>
        <span v-if="session.cliSessionId" class="mono muted sid" title="CLI conversation id">{{ session.cliSessionId.slice(0, 8) }}</span>
        <div class="spacer" />
        <label class="ctl">
          <span>Model</span>
          <input
            v-model="modelDraft"
            class="input sm-input"
            list="s-models"
            placeholder="Default"
            @change="setting('model', modelDraft.trim())"
          />
          <datalist id="s-models">
            <option v-for="m in provider.models.filter((m) => m.id)" :key="m.id" :value="m.id">{{ m.name }}</option>
          </datalist>
        </label>
        <label class="ctl">
          <span>Effort</span>
          <select class="select sm" :value="session.settings.effort" @change="setting('effort', ($event.target as HTMLSelectElement).value)">
            <option value="">Default</option>
            <option v-for="c in provider.efforts.filter((c) => c.id)" :key="c.id" :value="c.id">{{ c.name || c.id }}</option>
          </select>
        </label>
        <label class="ctl">
          <span>Mode</span>
          <select class="select sm" :value="session.settings.mode" @change="setting('mode', ($event.target as HTMLSelectElement).value)">
            <option value="">Default</option>
            <option v-for="c in provider.modes.filter((c) => c.id)" :key="c.id" :value="c.id" :title="c.description">{{ c.name || c.id }}</option>
          </select>
        </label>
      </div>
      <nav class="tabs" role="tablist">
        <button role="tab" :aria-selected="tab === 'chat'" :class="{ on: tab === 'chat' }" @click="tab = 'chat'">Chat</button>
        <button role="tab" :aria-selected="tab === 'terminal'" :class="{ on: tab === 'terminal' }" @click="tab = 'terminal'">Terminal</button>
      </nav>
    </header>

    <section v-show="tab === 'chat'" class="chat">
      <ChatLog :session-id="session.id" :events="events" :has-more="hasMore" :loading-more="loadingMore" class="log" @load-more="loadMore" />
      <div v-if="session.pending.length" class="requests">
        <RequestCard v-for="r in session.pending" :key="r.id" :session-id="session.id" :request="r" />
      </div>
      <PromptComposer ref="composer" :key="session.id" :session="session" />
    </section>
    <section v-if="tab === 'terminal'" class="terminal">
      <TerminalView :key="session.id" :session-id="session.id" :running="session.running" @start="start" />
    </section>
  </div>
  <div v-else class="gone muted">Loading…</div>
</template>

<style scoped>
.page {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
}
.head {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 10px 16px 0;
  border-bottom: 1px solid var(--border);
  background: var(--surface);
}
.row {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
  flex-wrap: wrap;
}
.top {
  flex-wrap: nowrap;
}
.spacer {
  flex: 1;
}
.back {
  display: none;
  text-decoration: none;
}
.title {
  margin: 0;
  font-size: 16px;
  cursor: text;
  min-width: 0;
}
.title-input {
  max-width: 420px;
  min-height: 30px;
}
.sub {
  flex-wrap: nowrap;
  font-size: 12.5px;
}
.sub > * {
  flex-shrink: 0;
}
.sub > .path {
  flex: 0 1 auto;
  min-width: 0;
  color: var(--text-2);
}
.sid {
  font-size: 11.5px;
}
.ctl {
  display: flex;
  align-items: center;
  gap: 6px;
  color: var(--text-2);
  font-size: 12px;
}
.sm-input {
  min-height: 28px;
  width: 150px;
  padding: 2px 8px;
  font-size: 12.5px;
}
.tabs {
  display: flex;
  gap: 2px;
  margin-top: 2px;
}
.tabs button {
  padding: 7px 14px;
  border: 0;
  border-bottom: 2px solid transparent;
  background: none;
  color: var(--text-2);
  font-weight: 600;
  font-size: 13px;
  cursor: pointer;
}
.tabs button.on {
  border-bottom-color: var(--accent);
  color: var(--text);
}
.chat {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;
}
.log {
  flex: 1;
  min-height: 0;
}
.requests {
  display: flex;
  flex-direction: column;
  gap: 10px;
  width: 100%;
  max-width: 900px;
  max-height: 55vh;
  overflow-y: auto;
  margin: 0 auto;
  padding: 8px 20px 0;
}
.terminal {
  flex: 1;
  min-height: 0;
}
.gone {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 12px;
  height: 100%;
}
@media (max-width: 1000px) {
  .sub {
    flex-wrap: wrap;
  }
  .sub > .path {
    flex-basis: 100%;
    order: 1;
  }
}
@media (max-width: 800px) {
  .back {
    display: inline-flex;
  }
  .head {
    padding: 8px 10px 0;
  }
  .ctl span {
    display: none;
  }
  .sm-input {
    width: 120px;
  }
}
</style>

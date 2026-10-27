<script setup lang="ts">
import type { Event as SessionEvent } from '~/api'

const props = defineProps<{ sessionId: string; events: SessionEvent[]; hasMore: boolean; loadingMore: boolean }>()
const emit = defineEmits<{ loadMore: [] }>()

// Claude's own attention notifications duplicate what the page already
// shows: a request card or the Ready status.
const QUIET_NOTICES = new Set(['idle_prompt', 'permission_prompt', 'elicitation_dialog'])

type Item =
  | { key: string; kind: 'event'; ev: SessionEvent }
  | { key: string; kind: 'tool'; call?: SessionEvent; result?: SessionEvent }

/** items pairs each tool call with its result. */
const items = computed<Item[]>(() => {
  const out: Item[] = []
  const calls = new Map<string, Extract<Item, { kind: 'tool' }>>()
  for (const ev of props.events) {
    switch (ev.type) {
      case 'turn_started':
        continue
      case 'notice':
        if (QUIET_NOTICES.has(ev.kind ?? '')) continue
        break
      case 'tool_call': {
        const it = { key: `t${ev.seq}`, kind: 'tool' as const, call: ev }
        if (ev.tool?.id) calls.set(ev.tool.id, it)
        out.push(it)
        continue
      }
      case 'tool_result': {
        const it = ev.tool?.id ? calls.get(ev.tool.id) : undefined
        if (it && !it.result) it.result = ev
        else out.push({ key: `t${ev.seq}`, kind: 'tool', result: ev })
        continue
      }
    }
    out.push({ key: `e${ev.seq}`, kind: 'event', ev })
  }
  return out
})

const LIFECYCLE_ICON: Record<string, string> = {
  started: '▶',
  resumed: '↻',
  suspended: '‖',
  stopped: '■',
  shutdown: '■',
}

const RESOLUTION: Record<string, string> = {
  client: 'answered here',
  external: 'answered in the CLI or Remote Control',
  cancelled: 'withdrawn',
}

function fmtTime(iso: string) {
  return new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

function toolTitle(it: Extract<Item, { kind: 'tool' }>) {
  return it.call?.tool?.name || it.result?.tool?.name || 'tool'
}

function json(v: unknown) {
  return typeof v === 'string' ? v : JSON.stringify(v, null, 2)
}

// --- scrolling: stay pinned to the bottom unless the user scrolled up ---

const scroller = ref<HTMLElement>()
const pinned = ref(true)

function onScroll() {
  const el = scroller.value
  if (!el) return
  pinned.value = el.scrollHeight - el.scrollTop - el.clientHeight < 120
}

function scrollToBottom() {
  const el = scroller.value
  if (el) el.scrollTop = el.scrollHeight
}

let prevFirst = 0
let prevHeight = 0
watch(
  () => props.events.length,
  async () => {
    const el = scroller.value
    const first = props.events[0]?.seq ?? 0
    const prepended = prevFirst && first < prevFirst
    prevFirst = first
    if (el && prepended) {
      // older history was loaded: keep the visible part in place
      const before = prevHeight
      await nextTick()
      el.scrollTop += el.scrollHeight - before
    } else if (pinned.value) {
      await nextTick()
      scrollToBottom()
    }
    prevHeight = el?.scrollHeight ?? 0
  },
  { flush: 'pre' },
)
watch(
  () => props.events.length,
  () => {
    prevHeight = scroller.value?.scrollHeight ?? 0
  },
  { flush: 'post' },
)

defineExpose({ scrollToBottom })
</script>

<template>
  <div ref="scroller" class="log" @scroll.passive="onScroll">
    <div class="inner">
      <div v-if="hasMore" class="more">
        <button class="btn sm" :disabled="loadingMore" @click="emit('loadMore')">
          {{ loadingMore ? 'Loading…' : 'Load earlier messages' }}
        </button>
      </div>
      <p v-if="!events.length" class="empty muted">No messages yet. Say hello below.</p>

      <template v-for="it in items" :key="it.key">
        <details v-if="it.kind === 'tool'" class="tool" :class="{ error: it.result?.tool?.isError, pending: !it.result }">
          <summary>
            <b>{{ toolTitle(it) }}</b>
            <span class="truncate mono">{{ toolSummary(it.call?.tool?.input) }}</span>
          </summary>
          <div class="tool-body">
            <pre v-if="it.call?.tool?.input != null" class="input">{{ json(it.call.tool.input) }}</pre>
            <pre v-if="it.result" class="output">{{ it.result.tool?.output || '(no output)' }}<template v-if="it.result.tool?.truncated">
… (truncated)</template></pre>
          </div>
        </details>
        <div v-if="it.kind === 'tool' && it.result?.attachments?.length" class="thumbs">
          <AttachmentView v-for="a in it.result.attachments" :key="a.id" :session-id="sessionId" :file="a" compact />
        </div>

        <template v-if="it.kind === 'event'">
          <div v-if="it.ev.type === 'user_message'" class="user">
            <template v-for="msg in [splitAttachments(it.ev.text || '')]" :key="0">
              <div v-if="msg.text" class="bubble" :class="{ command: it.ev.kind === 'command' }">{{ msg.text }}</div>
              <div v-if="msg.files.length" class="thumbs uploads">
                <UploadChip v-for="f in msg.files" :key="f" :session-id="sessionId" :name="f" />
              </div>
            </template>
            <div v-if="it.ev.attachments?.length" class="thumbs">
              <AttachmentView v-for="a in it.ev.attachments" :key="a.id" :session-id="sessionId" :file="a" compact />
            </div>
          </div>

          <div v-else-if="it.ev.type === 'assistant_text'" class="assistant md" v-html="renderMarkdown(it.ev.text || '')" />

          <details v-else-if="it.ev.type === 'thinking'" class="thinking">
            <summary>Thinking</summary>
            <div class="think-body">{{ it.ev.text }}</div>
          </details>

          <div v-else-if="it.ev.type === 'files'" class="files">
            <div class="caption">{{ it.ev.text || `Sent ${it.ev.attachments?.length ?? 0} file(s)` }}</div>
            <AttachmentView v-for="a in it.ev.attachments" :key="a.id" :session-id="sessionId" :file="a" />
          </div>

          <div v-else-if="it.ev.type === 'turn_completed'" class="turn-end">
            <span>{{ it.ev.interrupted ? 'Turn interrupted' : 'Turn finished' }} · {{ fmtTime(it.ev.time) }}</span>
          </div>

          <div v-else-if="it.ev.type === 'lifecycle'" class="lifecycle" :class="it.ev.kind">
            <span>{{ LIFECYCLE_ICON[it.ev.kind || ''] || '•' }} {{ it.ev.text }} · {{ fmtTime(it.ev.time) }}</span>
          </div>

          <div v-else-if="it.ev.type === 'request'" class="note">
            {{ it.ev.request?.kind === 'question' ? 'Question asked' : 'Permission requested' }}: {{ it.ev.request?.title }}
          </div>
          <div v-else-if="it.ev.type === 'request_resolved'" class="note">
            Request {{ RESOLUTION[it.ev.resolution || ''] || it.ev.resolution }}
          </div>
          <div v-else-if="it.ev.type === 'restarted'" class="note">CLI relaunched with the new settings</div>
          <div v-else-if="it.ev.type === 'error'" class="note error">{{ it.ev.text }}</div>
          <details v-else-if="it.ev.type === 'exited' && it.ev.screen" class="tool error">
            <summary><b>CLI exited</b><span class="truncate">{{ it.ev.text }} — last screen</span></summary>
            <div class="tool-body"><pre class="output">{{ it.ev.screen }}</pre></div>
          </details>
          <div v-else-if="it.ev.type === 'exited'" class="note error">CLI exited: {{ it.ev.text }}</div>
          <details v-else-if="it.ev.type === 'diagnostic'" class="tool error">
            <summary><b>recognizer drift</b><span class="truncate">{{ it.ev.text }}</span></summary>
            <div class="tool-body"><pre class="output">{{ it.ev.screen }}</pre></div>
          </details>
          <div v-else-if="it.ev.type === 'notice'" class="note">{{ it.ev.text }}</div>
        </template>
      </template>
      <slot />
    </div>
  </div>
</template>

<style scoped>
.log {
  height: 100%;
  overflow-y: auto;
  overscroll-behavior: contain;
}
.inner {
  display: flex;
  flex-direction: column;
  gap: 10px;
  max-width: 900px;
  margin: 0 auto;
  padding: 20px 20px 12px;
}
.more {
  text-align: center;
}
.empty {
  margin: 40px 0;
  text-align: center;
}
.user {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 6px;
}
.bubble {
  max-width: 85%;
  padding: 9px 13px;
  border-radius: 14px 14px 4px 14px;
  background: var(--user-bubble);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.bubble.command {
  font-family: var(--mono);
  font-size: 13px;
}
.assistant {
  padding: 2px 2px;
}
.thinking summary,
.tool summary {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  cursor: pointer;
  list-style: none;
}
.thinking summary::-webkit-details-marker,
.tool summary::-webkit-details-marker {
  display: none;
}
.thinking {
  color: var(--text-2);
  font-size: 13px;
}
.thinking summary {
  font-style: italic;
}
.think-body {
  margin-top: 6px;
  padding-left: 12px;
  border-left: 2px solid var(--border);
  white-space: pre-wrap;
}
.tool {
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface);
  font-size: 13px;
}
.tool summary {
  padding: 6px 10px;
}
.tool summary b {
  flex-shrink: 0;
  font-weight: 600;
}
.tool summary span {
  color: var(--text-2);
  font-size: 12px;
}
.tool.pending summary b::after {
  content: ' …';
  color: var(--text-3);
}
.tool.error {
  border-color: var(--danger);
}
.tool.error summary b {
  color: var(--danger);
}
.tool-body {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 0 10px 10px;
}
.tool-body pre {
  max-height: 320px;
  overflow: auto;
  padding: 8px 10px;
  border-radius: var(--radius-sm);
  background: var(--code-bg);
}
.uploads {
  justify-content: flex-end;
}
.thumbs {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.files {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.caption {
  font-weight: 600;
}
.turn-end,
.lifecycle {
  display: flex;
  align-items: center;
  gap: 10px;
  color: var(--text-3);
  font-size: 11.5px;
}
.turn-end::before,
.turn-end::after {
  content: '';
  flex: 1;
  border-top: 1px dashed var(--border);
}
.lifecycle {
  justify-content: center;
}
.lifecycle span {
  padding: 3px 10px;
  border-radius: 999px;
  background: var(--surface-2);
}
.lifecycle.suspended span {
  background: var(--warn-soft);
  color: var(--warn);
}
.lifecycle.resumed span,
.lifecycle.started span {
  background: var(--ok-soft);
  color: var(--ok);
}
.note {
  color: var(--text-3);
  font-size: 12.5px;
}
.note.error {
  color: var(--danger);
}
</style>

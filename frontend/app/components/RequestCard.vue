<script setup lang="ts">
import { respondRequest } from '~/api'
import type { Request as AgentRequest, Response as AgentResponse } from '~/api'

const props = defineProps<{ sessionId: string; request: AgentRequest }>()
const busy = ref(false)
const note = ref('')

// One answer per question: chosen labels and free text.
const answers = reactive(props.request.questions?.map(() => ({ selected: [] as string[], text: '' })) ?? [])

async function send(response: AgentResponse) {
  busy.value = true
  try {
    const { data } = await respondRequest({ path: { id: props.sessionId }, body: { requestId: props.request.id, response } })
    patchSession(data)
  } catch (e) {
    report(e)
    busy.value = false
  }
}

function pick(qi: number, label: string, multi: boolean) {
  const a = answers[qi]!
  if (multi) {
    a.selected = a.selected.includes(label) ? a.selected.filter((l) => l !== label) : [...a.selected, label]
  } else {
    a.selected = [label]
    a.text = ''
  }
}

function typed(qi: number, multi: boolean) {
  if (!multi && answers[qi]!.text) answers[qi]!.selected = []
}

function submit() {
  const out = answers.map((a) => ({ selected: a.selected, text: a.text.trim() || undefined }))
  if (out.some((a) => !a.selected.length && !a.text)) {
    toast('Answer every question first', 'error')
    return
  }
  send({ answers: out })
}

const detail = computed(() => {
  const p = props.request.permission
  if (!p) return ''
  if (p.detail) return p.detail
  return p.input ? JSON.stringify(p.input, null, 2) : ''
})
</script>

<template>
  <div class="req">
    <template v-if="request.kind === 'permission' && request.permission">
      <div class="title">
        <span class="chip">{{ request.permission.toolName || 'Permission' }}</span>
        <strong>{{ request.title }}</strong>
      </div>
      <pre v-if="detail" class="detail">{{ detail }}</pre>
      <div class="opts">
        <button
          v-for="o in request.permission.options"
          :key="o.id"
          class="btn"
          :class="{ primary: o.kind === 'allow_once', danger: o.kind === 'deny' }"
          :disabled="busy"
          @click="send({ optionId: o.id, message: o.kind === 'deny' ? note : undefined })"
        >
          {{ o.label }}
        </button>
      </div>
      <input
        v-if="request.permission.toolName && request.permission.options.some((o) => o.kind === 'deny')"
        v-model="note"
        class="input"
        placeholder="Optional note for Deny (what to do instead)"
        :disabled="busy"
      />
    </template>

    <template v-else>
      <div class="title"><strong>{{ (request.questions?.length ?? 0) > 1 ? 'Questions' : 'Question' }}</strong></div>
      <div v-for="(q, qi) in request.questions" :key="qi" class="q">
        <div class="q-head">
          <span v-if="q.header" class="chip">{{ q.header }}</span>
          <strong>{{ q.text }}</strong>
          <span v-if="q.multiSelect" class="muted hint">choose any</span>
        </div>
        <label v-for="o in q.options" :key="o.label" class="choice" :class="{ on: answers[qi]!.selected.includes(o.label) }">
          <input
            :type="q.multiSelect ? 'checkbox' : 'radio'"
            :name="`${request.id}-${qi}`"
            :checked="answers[qi]!.selected.includes(o.label)"
            :disabled="busy"
            @change="pick(qi, o.label, !!q.multiSelect)"
          />
          <span>
            {{ o.label }}
            <small v-if="o.description" class="muted">{{ o.description }}</small>
          </span>
        </label>
        <input
          v-if="q.allowText"
          v-model="answers[qi]!.text"
          class="input"
          :placeholder="q.multiSelect ? 'Add your own answer (optional)' : 'Or type your own answer'"
          :disabled="busy"
          @input="typed(qi, !!q.multiSelect)"
        />
      </div>
      <div class="opts end">
        <button class="btn" :disabled="busy" @click="send({ cancel: true })">Dismiss</button>
        <button class="btn primary" :disabled="busy" @click="submit">Submit</button>
      </div>
    </template>
  </div>
</template>

<style scoped>
.req {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 14px;
  border: 1px solid var(--accent);
  border-radius: var(--radius);
  background: var(--surface);
  box-shadow: 0 0 0 3px var(--accent-soft);
}
.title,
.q-head {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.detail {
  max-height: 220px;
  overflow: auto;
  padding: 8px 10px;
  border-radius: var(--radius-sm);
  background: var(--code-bg);
}
.opts {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}
.opts .btn {
  height: auto;
  min-height: 32px;
  max-width: 100%;
  padding-top: 5px;
  padding-bottom: 5px;
  white-space: normal;
  overflow-wrap: anywhere;
  text-align: left;
}
.opts.end {
  justify-content: flex-end;
}
.q {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.choice {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 7px 10px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  cursor: pointer;
}
.choice.on {
  border-color: var(--accent);
  background: var(--accent-soft);
}
.choice input {
  margin-top: 3px;
  accent-color: var(--accent);
}
.choice small {
  display: block;
  font-size: 12px;
}
</style>

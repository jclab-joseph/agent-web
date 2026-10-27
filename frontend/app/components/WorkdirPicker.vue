<script setup lang="ts">
import { deleteWorkdir, listDirs, listWorkdirs } from '~/api'
import type { DirEntry, Workdir } from '~/api'

/**
 * WorkdirPicker picks a working directory: one used before (the recent list)
 * or any path typed in, with directory autocompletion.
 */
const model = defineModel<string>({ required: true })
const exists = defineModel<boolean | null>('exists', { default: null })

const info = useServerInfo()
const recent = ref<Workdir[]>([])
const suggestions = ref<DirEntry[]>([])
const open = ref(false)
const active = ref(-1)
const input = ref<HTMLInputElement>()
const now = useNow(60000)

onMounted(async () => {
  try {
    recent.value = (await listWorkdirs()).data
  } catch (e) {
    report(e)
  }
  if (model.value) lookup(model.value, false)
})

let seq = 0
let debounce: ReturnType<typeof setTimeout> | undefined

async function lookup(path: string, showList = true) {
  const mine = ++seq
  if (!path.trim()) {
    suggestions.value = []
    exists.value = null
    return
  }
  try {
    const { data } = await listDirs({ query: { path } })
    if (mine !== seq) return
    exists.value = data.exists
    // Typing "/a/b" completes siblings of b; "/a/b/" lists b's children.
    suggestions.value = data.exists && !path.endsWith('/') ? [] : data.entries
    open.value = showList && suggestions.value.length > 0
    active.value = -1
  } catch {
    if (mine === seq) {
      exists.value = null
      suggestions.value = []
    }
  }
}

function onInput() {
  clearTimeout(debounce)
  debounce = setTimeout(() => lookup(model.value), 120)
}

function choose(e: DirEntry) {
  model.value = e.path + '/'
  lookup(model.value)
  input.value?.focus()
}

function pickRecent(w: Workdir) {
  model.value = w.path
  open.value = false
  lookup(w.path, false)
}

async function forget(w: Workdir) {
  try {
    await deleteWorkdir({ query: { path: w.path } })
    recent.value = recent.value.filter((x) => x.path !== w.path)
  } catch (e) {
    report(e)
  }
}

function onKey(ev: KeyboardEvent) {
  if (!open.value || !suggestions.value.length) {
    if (ev.key === 'ArrowDown' && suggestions.value.length) open.value = true
    return
  }
  if (ev.key === 'ArrowDown') {
    active.value = (active.value + 1) % suggestions.value.length
    ev.preventDefault()
  } else if (ev.key === 'ArrowUp') {
    active.value = (active.value - 1 + suggestions.value.length) % suggestions.value.length
    ev.preventDefault()
  } else if ((ev.key === 'Enter' || ev.key === 'Tab') && (active.value >= 0 || suggestions.value.length === 1)) {
    const pick = suggestions.value[Math.max(active.value, 0)]
    if (pick) {
      choose(pick)
      ev.preventDefault()
    }
  } else if (ev.key === 'Escape') {
    open.value = false
    ev.stopPropagation()
    ev.preventDefault()
  }
}

function onBlur() {
  setTimeout(() => (open.value = false), 150)
}
</script>

<template>
  <div class="picker">
    <div class="combo">
      <input
        ref="input"
        v-model="model"
        class="input mono"
        placeholder="/path/to/project or ~/project"
        spellcheck="false"
        autocomplete="off"
        role="combobox"
        :aria-expanded="open"
        @input="onInput"
        @keydown="onKey"
        @focus="lookup(model)"
        @blur="onBlur"
      />
      <ul v-if="open" class="suggest" role="listbox">
        <li
          v-for="(e, i) in suggestions"
          :key="e.path"
          role="option"
          :aria-selected="i === active"
          :class="{ active: i === active }"
          @mousedown.prevent="choose(e)"
        >
          <span class="mono">{{ e.name }}/</span>
        </li>
      </ul>
    </div>
    <p v-if="model.trim()" class="state hint">
      <span v-if="exists === true" class="ok">Existing directory</span>
      <span v-else-if="exists === false" class="new">This directory does not exist yet</span>
    </p>

    <div v-if="recent.length" class="recent">
      <div class="label">Recently used</div>
      <ul>
        <li v-for="w in recent" :key="w.path" :class="{ selected: model.replace(/\/+$/, '') === w.path, missing: !w.exists }">
          <button type="button" class="pick" :disabled="!w.exists" :title="w.path" @click="pickRecent(w)">
            <span class="name">{{ basename(w.path) }}</span>
            <span class="full mono truncate">{{ shortPath(w.path, info?.home) }}</span>
            <span class="meta muted">{{ w.exists ? relTime(w.lastUsedAt, now) : 'missing' }} · {{ w.useCount }}×</span>
          </button>
          <button type="button" class="btn ghost sm icon" title="Forget this directory" @click="forget(w)">×</button>
        </li>
      </ul>
    </div>
  </div>
</template>

<style scoped>
.picker {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.combo {
  position: relative;
}
.suggest {
  position: absolute;
  z-index: 10;
  top: calc(100% + 4px);
  left: 0;
  right: 0;
  max-height: 220px;
  overflow-y: auto;
  margin: 0;
  padding: 4px;
  list-style: none;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: var(--surface);
  box-shadow: var(--shadow);
}
.suggest li {
  padding: 5px 8px;
  border-radius: 4px;
  cursor: pointer;
  font-size: 13px;
}
.suggest li.active,
.suggest li:hover {
  background: var(--accent-soft);
}
.state {
  margin: 0;
}
.ok {
  color: var(--ok);
}
.new {
  color: var(--warn);
}
.recent {
  margin-top: 6px;
}
.recent ul {
  display: flex;
  flex-direction: column;
  gap: 2px;
  max-height: 210px;
  overflow-y: auto;
  margin: 4px 0 0;
  padding: 0;
  list-style: none;
}
.recent li {
  display: flex;
  align-items: center;
  gap: 4px;
  border-radius: var(--radius-sm);
}
.recent li.selected {
  background: var(--accent-soft);
}
.recent li.missing {
  opacity: 0.55;
}
.pick {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: baseline;
  gap: 10px;
  flex: 1;
  min-width: 0;
  padding: 6px 8px;
  border: 0;
  border-radius: var(--radius-sm);
  background: none;
  text-align: left;
  cursor: pointer;
}
.pick:hover:not(:disabled) {
  background: var(--surface-2);
}
.pick:disabled {
  cursor: default;
}
.name {
  font-weight: 600;
}
.full {
  font-size: 12px;
  color: var(--text-2);
}
.meta {
  font-size: 11.5px;
  white-space: nowrap;
}
</style>

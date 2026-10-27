<script setup lang="ts">
import { createSession, listDirs } from '~/api'
import type { LaunchOptions } from '~/api'

const { state, close } = useNewSession()
const providers = useProviders()
const dialog = ref<HTMLDialogElement>()

const form = reactive({
  provider: 'claude',
  cwd: '',
  title: '',
  model: '',
  effort: '',
  mode: '',
  createDir: false,
  options: {
    trustWorkspace: false,
    remoteControl: 'default',
    chrome: 'default',
    interaction: 'hook',
    screenReader: false,
    bridge: 'auto',
    search: false,
  } as Required<LaunchOptions>,
})
const exists = ref<boolean | null>(null)
const busy = ref(false)

const info = computed(() => providerInfo(form.provider))

const PREFS = 'agent-web:new-session'

function loadPrefs() {
  try {
    const p = JSON.parse(localStorage.getItem(PREFS) || '{}')
    if (p.provider) form.provider = p.provider
    if (p.options) Object.assign(form.options, p.options)
  } catch {
    // ignore
  }
}

function savePrefs() {
  try {
    localStorage.setItem(PREFS, JSON.stringify({ provider: form.provider, options: form.options }))
  } catch {
    // ignore
  }
}

watch(
  () => state.value.visible,
  async (v) => {
    if (!v) {
      dialog.value?.close()
      return
    }
    loadPrefs()
    await loadProviders()
    if (providers.value.length && !providers.value.some((p) => p.name === form.provider)) {
      form.provider = providers.value[0]!.name
    }
    form.cwd = state.value.cwd
    form.title = ''
    form.createDir = false
    dialog.value?.showModal()
  },
)

watch(
  () => form.provider,
  () => {
    form.model = ''
    form.effort = ''
    form.mode = ''
  },
)

async function submit() {
  // The picker checks as you type; check again in case it is behind.
  try {
    exists.value = (await listDirs({ query: { path: form.cwd } })).data.exists
  } catch {
    // the server validates anyway
  }
  if (exists.value === false && !form.createDir) {
    toast('The directory does not exist. Tick "Create it" or pick another one.', 'error')
    return
  }
  busy.value = true
  savePrefs()
  try {
    const opts: LaunchOptions = { ...form.options }
    if (form.provider !== 'claude') {
      delete opts.remoteControl
      delete opts.chrome
      delete opts.interaction
      delete opts.bridge
      delete opts.screenReader
    }
    const { data } = await createSession({
      body: {
        provider: form.provider as 'claude' | 'codex',
        cwd: form.cwd,
        createDir: form.createDir,
        title: form.title || undefined,
        model: form.model || undefined,
        effort: form.effort || undefined,
        mode: form.mode || undefined,
        options: opts,
      },
    })
    patchSession(data)
    close()
    await navigateTo(`/sessions/${data.id}`)
  } catch (e) {
    report(e)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <dialog ref="dialog" class="dlg" @close="close()" @cancel="close()">
    <form class="body" @submit.prevent="submit">
      <header>
        <h2>New session</h2>
        <button type="button" class="btn ghost icon" aria-label="Close" @click="close()">×</button>
      </header>

      <div class="field">
        <label>Agent</label>
        <div class="seg">
          <label v-for="p in providers" :key="p.name" :class="{ on: form.provider === p.name }">
            <input v-model="form.provider" type="radio" name="provider" :value="p.name" />
            <span>{{ p.displayName }}</span>
            <small v-if="p.version" class="muted">{{ p.version }}</small>
          </label>
        </div>
        <p v-if="!providers.length" class="hint">No supported CLI found on the server's PATH (claude, codex).</p>
      </div>

      <div class="field">
        <label>Working directory</label>
        <WorkdirPicker v-model="form.cwd" v-model:exists="exists" />
        <label v-if="exists === false" class="check">
          <input v-model="form.createDir" type="checkbox" />
          <span>Create it</span>
        </label>
      </div>

      <div class="grid">
        <div class="field">
          <label for="ns-model">Model</label>
          <input id="ns-model" v-model="form.model" class="input" list="ns-models" placeholder="Default" />
          <datalist id="ns-models">
            <option v-for="m in info.models.filter((m) => m.id)" :key="m.id" :value="m.id">{{ m.name }}</option>
          </datalist>
        </div>
        <div class="field">
          <label for="ns-effort">Effort</label>
          <select id="ns-effort" v-model="form.effort" class="select">
            <option value="">Default</option>
            <option v-for="c in info.efforts.filter((c) => c.id)" :key="c.id" :value="c.id">{{ c.name || c.id }}</option>
          </select>
        </div>
        <div class="field">
          <label for="ns-mode">Mode</label>
          <select id="ns-mode" v-model="form.mode" class="select">
            <option value="">Default</option>
            <option v-for="c in info.modes.filter((c) => c.id)" :key="c.id" :value="c.id" :title="c.description">{{ c.name || c.id }}</option>
          </select>
        </div>
      </div>

      <div class="field">
        <label for="ns-title">Title <span class="muted">(optional; defaults to the first message)</span></label>
        <input id="ns-title" v-model="form.title" class="input" maxlength="200" />
      </div>

      <details class="adv">
        <summary>Advanced</summary>
        <div class="adv-body">
          <label class="check">
            <input v-model="form.options.trustWorkspace" type="checkbox" />
            <span>Trust the workspace automatically <span class="hint">(otherwise the trust prompt is shown here)</span></span>
          </label>
          <template v-if="form.provider === 'claude'">
            <div class="grid">
              <div class="field">
                <label>Remote Control</label>
                <select v-model="form.options.remoteControl" class="select">
                  <option value="default">User setting</option>
                  <option value="on">On</option>
                  <option value="off">Off</option>
                </select>
              </div>
              <div class="field">
                <label>Claude in Chrome</label>
                <select v-model="form.options.chrome" class="select">
                  <option value="default">User setting</option>
                  <option value="on">On</option>
                  <option value="off">Off</option>
                </select>
              </div>
              <div class="field">
                <label>Answer prompts via</label>
                <select v-model="form.options.interaction" class="select">
                  <option value="hook">Hooks (structured)</option>
                  <option value="terminal">TUI dialog</option>
                </select>
              </div>
            </div>
            <label class="check">
              <input v-model="form.options.screenReader" type="checkbox" />
              <span>Screen-reader rendering (<code>--ax-screen-reader</code>)</span>
            </label>
            <label class="check">
              <input :checked="form.options.bridge === 'off'" type="checkbox" @change="form.options.bridge = ($event.target as HTMLInputElement).checked ? 'off' : 'auto'" />
              <span>Disable the bridge mod (keyboard only)</span>
            </label>
          </template>
          <label v-if="form.provider === 'codex'" class="check">
            <input v-model="form.options.search" type="checkbox" />
            <span>Web search</span>
          </label>
        </div>
      </details>

      <footer>
        <button type="button" class="btn" @click="close()">Cancel</button>
        <button class="btn primary" :disabled="busy || !form.cwd.trim() || !providers.length">
          {{ busy ? 'Starting…' : 'Start session' }}
        </button>
      </footer>
    </form>
  </dialog>
</template>

<style scoped>
.dlg {
  width: min(640px, calc(100vw - 24px));
  max-height: calc(100vh - 24px);
}
.body {
  display: flex;
  flex-direction: column;
  gap: 16px;
  padding: 18px 20px 20px;
}
header,
footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
footer {
  justify-content: flex-end;
}
h2 {
  margin: 0;
  font-size: 17px;
}
.grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 10px;
}
.seg {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}
.seg label {
  display: flex;
  align-items: baseline;
  gap: 6px;
  padding: 6px 12px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  cursor: pointer;
  font-weight: 600;
  font-size: 13px;
}
.seg label.on {
  border-color: var(--accent);
  background: var(--accent-soft);
}
.seg input {
  position: absolute;
  opacity: 0;
  pointer-events: none;
}
.seg small {
  font-weight: 400;
  font-size: 11.5px;
}
.adv summary {
  cursor: pointer;
  font-weight: 600;
  font-size: 13px;
  color: var(--text-2);
}
.adv-body {
  display: flex;
  flex-direction: column;
  gap: 10px;
  margin-top: 10px;
}
@media (max-width: 560px) {
  .grid {
    grid-template-columns: 1fr;
  }
}
</style>

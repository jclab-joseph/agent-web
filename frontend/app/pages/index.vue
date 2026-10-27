<script setup lang="ts">
import { listWorkdirs } from '~/api'
import type { Workdir } from '~/api'

const info = useServerInfo()
const providers = useProviders()
const sessions = useSessions()
const newSession = useNewSession()
const recent = ref<Workdir[]>([])

onMounted(async () => {
  try {
    recent.value = (await listWorkdirs()).data.filter((w) => w.exists).slice(0, 6)
  } catch {
    // the auth gate handles 401
  }
})

const idleText = computed(() => {
  const s = info.value?.idleTimeoutSeconds ?? 0
  if (!s) return 'Idle CLIs keep running.'
  return `A CLI idle for ${s >= 60 ? `${Math.round(s / 60)} min` : `${s} s`} is stopped and resumed with your next message.`
})
</script>

<template>
  <div class="home">
    <div class="hero">
      <h1>Run coding agents from the browser</h1>
      <p class="muted">
        Sessions run the real Claude Code / Codex CLI on this machine and are kept in a local database. {{ idleText }}
      </p>
      <div class="actions">
        <button class="btn primary" @click="newSession.open()">+ New session</button>
        <span class="muted providers">
          <template v-if="providers.length">
            Available: <strong v-for="(p, i) in providers" :key="p.name">{{ p.displayName }}{{ p.version ? ` ${p.version}` : '' }}{{ i < providers.length - 1 ? ', ' : '' }}</strong>
          </template>
        </span>
      </div>
    </div>

    <section v-if="recent.length" class="recent">
      <h2>Start in a recent directory</h2>
      <div class="cards">
        <button v-for="w in recent" :key="w.path" class="card" @click="newSession.open(w.path)">
          <span class="name">{{ basename(w.path) }}</span>
          <span class="path mono truncate">{{ shortPath(w.path, info?.home) }}</span>
        </button>
      </div>
    </section>

    <p v-if="sessions.length" class="muted foot">Pick a session on the left to continue it.</p>
  </div>
</template>

<style scoped>
.home {
  max-width: 760px;
  margin: 0 auto;
  padding: 64px 28px;
}
.hero h1 {
  margin: 0 0 10px;
  font-size: 26px;
  letter-spacing: -0.01em;
}
.hero p {
  margin: 0;
  max-width: 600px;
}
.actions {
  display: flex;
  align-items: center;
  gap: 14px;
  flex-wrap: wrap;
  margin-top: 22px;
}
.providers {
  font-size: 13px;
}
.recent {
  margin-top: 44px;
}
.recent h2 {
  margin: 0 0 12px;
  font-size: 14px;
  color: var(--text-2);
}
.cards {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(210px, 1fr));
  gap: 10px;
}
.card {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 12px 14px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface);
  text-align: left;
  cursor: pointer;
  min-width: 0;
}
.card:hover {
  border-color: var(--accent);
}
.name {
  font-weight: 600;
}
.path {
  font-size: 12px;
  color: var(--text-3);
}
.foot {
  margin-top: 36px;
  font-size: 13px;
}
</style>

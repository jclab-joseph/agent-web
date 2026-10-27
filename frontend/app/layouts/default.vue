<script setup lang="ts">
import { logout } from '~/api'

const route = useRoute()
const info = useServerInfo()
const newSession = useNewSession()
const inSession = computed(() => route.path.startsWith('/sessions/'))

let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  refreshSessions()
  loadProviders()
  timer = setInterval(() => {
    if (document.visibilityState === 'visible') refreshSessions()
  }, 3000)
})
onUnmounted(() => clearInterval(timer))

async function doLogout() {
  await logout().catch(() => {})
  useAuth().value.needLogin = true
}
</script>

<template>
  <div class="shell" :class="{ 'in-session': inSession }">
    <aside class="sidebar">
      <header class="side-head">
        <NuxtLink to="/" class="brand">
          <img src="/favicon.svg" alt="" width="22" height="22" />
          <span>agent-web</span>
        </NuxtLink>
        <button class="btn primary sm" @click="newSession.open()">+ New session</button>
      </header>
      <SessionList />
      <footer class="side-foot">
        <VersionTag />
        <button v-if="info?.authRequired" class="btn ghost sm" @click="doLogout">Log out</button>
      </footer>
    </aside>
    <main class="main">
      <slot />
    </main>
    <NewSessionDialog />
  </div>
</template>

<style scoped>
.shell {
  display: grid;
  grid-template-columns: var(--sidebar-w) minmax(0, 1fr);
  height: 100%;
}
.sidebar {
  display: flex;
  flex-direction: column;
  min-height: 0;
  border-right: 1px solid var(--border);
  background: var(--surface);
}
.side-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 12px 12px 10px 14px;
  border-bottom: 1px solid var(--border);
}
.brand {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--text);
  font-weight: 700;
  font-size: 15px;
  text-decoration: none;
}
.side-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 8px 12px 8px 14px;
  border-top: 1px solid var(--border);
  min-height: 42px;
}
.main {
  min-width: 0;
  min-height: 0;
  height: 100%;
}

@media (max-width: 800px) {
  .shell {
    grid-template-columns: 1fr;
  }
  .shell:not(.in-session) .main {
    display: none;
  }
  .shell.in-session .sidebar {
    display: none;
  }
  .sidebar {
    border-right: 0;
  }
}
</style>

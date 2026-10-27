<script setup lang="ts">
const sessions = useSessions()
const info = useServerInfo()
const route = useRoute()
const now = useNow(30000)

function label(s: { title: string; cwd: string }) {
  return s.title || basename(s.cwd)
}
</script>

<template>
  <nav class="list">
    <p v-if="!sessions.length" class="empty muted">No sessions yet.</p>
    <NuxtLink
      v-for="s in sessions"
      :key="s.id"
      :to="`/sessions/${s.id}`"
      class="item"
      :class="{ active: route.params.id === s.id }"
    >
      <span class="row">
        <span class="title truncate">{{ label(s) }}</span>
        <span v-if="s.pending.length" class="badge">{{ s.pending.length }}</span>
      </span>
      <span class="row meta">
        <span class="status" :class="s.status">{{ STATUS_LABEL[s.status] }}</span>
        <span class="chip">{{ s.provider }}</span>
        <span class="when muted">{{ relTime(s.lastActiveAt, now) }}</span>
      </span>
      <span class="path mono truncate" :title="s.cwd">{{ shortPath(s.cwd, info?.home) }}</span>
    </NuxtLink>
  </nav>
</template>

<style scoped>
.list {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 6px;
}
.empty {
  padding: 16px 10px;
  font-size: 13px;
}
.item {
  display: flex;
  flex-direction: column;
  gap: 3px;
  padding: 9px 10px;
  border-radius: 8px;
  color: var(--text);
  text-decoration: none;
}
.item:hover {
  background: var(--surface-2);
}
.item.active {
  background: var(--accent-soft);
}
.row {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
.title {
  flex: 1;
  font-weight: 600;
  font-size: 13.5px;
}
.meta {
  font-size: 12px;
}
.when {
  margin-left: auto;
  font-size: 11.5px;
}
.path {
  font-size: 11.5px;
  color: var(--text-3);
}
.badge {
  min-width: 18px;
  height: 18px;
  padding: 0 5px;
  border-radius: 9px;
  background: var(--accent);
  color: var(--accent-text);
  font-size: 11px;
  font-weight: 700;
  text-align: center;
  line-height: 18px;
}
</style>

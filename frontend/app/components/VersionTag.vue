<script setup lang="ts">
const info = useServerInfo()
const title = computed(() => {
  const v = info.value?.version
  if (!v) return ''
  return [v.commit && `commit ${v.commit}`, v.date && `built ${v.date}`, v.goVersion, v.platform].filter(Boolean).join('\n')
})
</script>

<template>
  <span v-if="info" class="version mono" :title="title">agent-web {{ info.version.version }}</span>
</template>

<style scoped>
.version {
  font-size: 11.5px;
  color: var(--text-3);
}
</style>

<script setup lang="ts">
/** UploadChip shows an uploaded file: a thumbnail for images, else its name. */
const props = defineProps<{ sessionId: string; name: string }>()
const url = computed(() => uploadURL(props.sessionId, props.name))
const broken = ref(false)
</script>

<template>
  <a v-if="isImageName(name) && !broken" class="thumb" :href="url" target="_blank" rel="noopener" :title="name">
    <img :src="url" :alt="name" loading="lazy" @error="broken = true" />
  </a>
  <a v-else class="chip-file" :href="uploadURL(sessionId, name, true)" :title="`$AGENT_UPLOAD_DIR/${name}`">
    <span aria-hidden="true">📎</span>
    <span class="truncate">{{ name }}</span>
  </a>
</template>

<style scoped>
.thumb img {
  display: block;
  max-width: 180px;
  max-height: 120px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  object-fit: cover;
}
.chip-file {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  max-width: 260px;
  padding: 4px 10px;
  border: 1px solid var(--border);
  border-radius: 999px;
  background: var(--surface);
  color: var(--text);
  font-size: 12.5px;
  text-decoration: none;
}
.chip-file:hover {
  border-color: var(--accent);
}
</style>

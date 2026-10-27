<script setup lang="ts">
import type { Attachment } from '~/api'

const props = defineProps<{ sessionId: string; file: Attachment; compact?: boolean }>()

const url = computed(() => fileURL(props.sessionId, props.file.id))
const download = computed(() => fileURL(props.sessionId, props.file.id, true))
const TEXT_PREVIEW_MAX = 256 * 1024

const text = ref<string | null>(null)
const textError = ref('')
const isMarkdown = computed(() => /markdown/.test(props.file.mediaType))

onMounted(async () => {
  if (props.compact || props.file.kind !== 'text') return
  if (props.file.size && props.file.size > TEXT_PREVIEW_MAX) return
  try {
    const res = await fetch(url.value, { credentials: 'same-origin' })
    if (!res.ok) throw new Error(res.statusText || `HTTP ${res.status}`)
    text.value = await res.text()
  } catch (e) {
    textError.value = errorMessage(e)
  }
})
</script>

<template>
  <a v-if="compact && file.kind === 'image'" class="thumb" :href="url" target="_blank" rel="noopener">
    <img :src="url" :alt="file.name" loading="lazy" />
  </a>
  <div v-else class="file" :class="file.kind">
    <div class="head">
      <span class="name truncate" :title="file.path || file.name">{{ file.name }}</span>
      <span class="meta muted">{{ [file.mediaType, fmtSize(file.size)].filter(Boolean).join(' · ') }}</span>
      <a :href="download">Download</a>
    </div>
    <a v-if="file.kind === 'image'" :href="url" target="_blank" rel="noopener"><img :src="url" :alt="file.name" loading="lazy" /></a>
    <video v-else-if="file.kind === 'video'" :src="url" controls preload="metadata" />
    <audio v-else-if="file.kind === 'audio'" :src="url" controls preload="metadata" />
    <a v-else-if="file.kind === 'pdf'" class="open" :href="url" target="_blank" rel="noopener">Open PDF</a>
    <template v-else-if="file.kind === 'text'">
      <div v-if="text !== null && isMarkdown" class="preview md" v-html="renderMarkdown(text)" />
      <pre v-else-if="text !== null" class="preview">{{ text }}</pre>
      <p v-else-if="textError" class="muted err">Preview unavailable: {{ textError }}</p>
    </template>
  </div>
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
.file {
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface);
  overflow: hidden;
}
.head {
  display: flex;
  align-items: baseline;
  gap: 10px;
  padding: 8px 12px;
  border-bottom: 1px solid var(--border);
  font-size: 13px;
}
.name {
  font-weight: 600;
  min-width: 0;
}
.meta {
  margin-right: auto;
  font-size: 12px;
  white-space: nowrap;
}
.file img,
.file video {
  display: block;
  max-width: 100%;
  max-height: 480px;
}
.file audio {
  width: 100%;
  padding: 8px;
}
.open {
  display: inline-block;
  padding: 10px 12px;
}
.preview {
  max-height: 360px;
  overflow: auto;
  padding: 10px 12px;
  font-size: 12.5px;
}
.err {
  padding: 8px 12px;
  margin: 0;
  font-size: 12.5px;
}
</style>

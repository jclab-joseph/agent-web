<script setup lang="ts">
setupApiClient()

const auth = useAuth()
const info = useServerInfo()
const ready = ref(false)
const failed = ref('')

onMounted(async () => {
  try {
    await loadServerInfo()
  } catch (e) {
    failed.value = errorMessage(e)
  }
  ready.value = true
})
</script>

<template>
  <div v-if="!ready" class="boot muted">Loading…</div>
  <div v-else-if="failed && !info" class="boot">
    <p>Could not reach the agent-web server: {{ failed }}</p>
  </div>
  <LoginForm v-else-if="auth.needLogin" />
  <NuxtLayout v-else>
    <NuxtPage />
  </NuxtLayout>
  <ToastStack />
</template>

<style scoped>
.boot {
  display: grid;
  place-items: center;
  height: 100%;
  padding: 24px;
  text-align: center;
}
</style>

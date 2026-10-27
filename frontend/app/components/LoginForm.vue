<script setup lang="ts">
import { login } from '~/api'

const token = ref('')
const busy = ref(false)
const error = ref('')

async function submit() {
  busy.value = true
  error.value = ''
  try {
    await login({ body: { token: token.value } })
    useAuth().value.needLogin = false
    await loadServerInfo()
  } catch (e) {
    error.value = errorMessage(e)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="login">
    <form class="card" @submit.prevent="submit">
      <h1>agent-web</h1>
      <p class="muted">This server requires an access token.</p>
      <div class="field">
        <label for="token">Access token</label>
        <input id="token" v-model="token" class="input mono" type="password" autocomplete="current-password" autofocus required />
      </div>
      <p v-if="error" class="error">{{ error }}</p>
      <button class="btn primary" :disabled="busy || !token">{{ busy ? 'Checking…' : 'Log in' }}</button>
      <VersionTag />
    </form>
  </div>
</template>

<style scoped>
.login {
  display: grid;
  place-items: center;
  min-height: 100%;
  padding: 16px;
}
.card {
  display: flex;
  flex-direction: column;
  gap: 14px;
  width: min(360px, 100%);
  padding: 28px;
  border: 1px solid var(--border);
  border-radius: 14px;
  background: var(--surface);
  box-shadow: var(--shadow);
}
h1 {
  margin: 0;
  font-size: 20px;
}
p {
  margin: 0;
}
.error {
  color: var(--danger);
  font-size: 13px;
}
</style>

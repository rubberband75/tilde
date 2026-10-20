<script setup lang="ts">
// Login with a host account. The server checks the password through PAM and
// starts (or rejoins) that user's session.
import { nextTick, onMounted, ref } from 'vue'
import { ApiError } from '@/api/client'
import { useSession } from '@/stores/session'

const session = useSession()
const username = ref('')
const password = ref('')
const error = ref('')
const busy = ref(false)
const userInput = ref<HTMLInputElement | null>(null)
const passInput = ref<HTMLInputElement | null>(null)

async function submit() {
  if (busy.value || !username.value || !password.value) return
  busy.value = true
  error.value = ''
  try {
    await session.login(username.value.trim(), password.value)
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : 'Could not reach the server.'
    password.value = ''
    await nextTick()
    passInput.value?.focus()
  } finally {
    busy.value = false
  }
}

onMounted(() => userInput.value?.focus())
</script>

<template>
  <main class="login">
    <form class="card" @submit.prevent="submit">
      <div class="avatar" aria-hidden="true">{{ (username || '~').slice(0, 1).toUpperCase() }}</div>
      <h1>Welcome</h1>
      <p v-if="session.notice" class="notice">{{ session.notice }}</p>
      <input
        ref="userInput"
        v-model="username"
        class="entry"
        name="username"
        autocomplete="username"
        autocapitalize="off"
        spellcheck="false"
        placeholder="User name"
        aria-label="User name"
        @keydown.enter.prevent="passInput?.focus()"
      />
      <input
        ref="passInput"
        v-model="password"
        class="entry"
        type="password"
        name="password"
        autocomplete="current-password"
        placeholder="Password"
        aria-label="Password"
      />
      <p v-if="error" class="error" role="alert">{{ error }}</p>
      <button class="btn suggested" type="submit" :disabled="busy || !username || !password">
        {{ busy ? 'Logging in…' : 'Log In' }}
      </button>
    </form>
  </main>
</template>

<style scoped>
.login {
  position: fixed;
  inset: 0;
  display: grid;
  place-items: center;
  background: var(--desktop-bg);
}
.card {
  display: flex;
  flex-direction: column;
  align-items: stretch;
  gap: 10px;
  width: 320px;
  padding: 28px 28px 24px;
  border-radius: 10px;
  background: rgba(34, 34, 38, 0.92);
  box-shadow: 0 10px 40px rgba(0, 0, 0, 0.55);
  user-select: text;
}
.avatar {
  align-self: center;
  display: grid;
  place-items: center;
  width: 72px;
  height: 72px;
  border-radius: 50%;
  background: var(--accent);
  color: var(--accent-fg);
  font-size: 32px;
  font-weight: bold;
}
h1 {
  margin: 4px 0 6px;
  font-size: 18px;
  font-weight: normal;
  text-align: center;
  color: var(--menu-fg);
}
.entry {
  height: 36px;
}
.btn {
  height: 36px;
  margin-top: 4px;
}
.error {
  margin: 0;
  color: var(--danger);
  text-align: center;
}
.notice {
  margin: 0 0 4px;
  color: var(--warning);
  text-align: center;
}
</style>

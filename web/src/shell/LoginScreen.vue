<script setup lang="ts">
// Login with a host account. The server checks the password through PAM and
// starts (or rejoins) that user's session. If PAM asks more, such as a
// verification code (D43), its question takes the password field's place.
import { nextTick, onMounted, ref } from 'vue'
import { ApiError } from '@/api/client'
import { rememberedUser, rememberUser } from '@/lib/rememberUser'
import { useSession } from '@/stores/session'
import AuthQuestion from './AuthQuestion.vue'

const session = useSession()
const username = ref(rememberedUser())
// Only the name is remembered, never the password (D42).
const remember = ref(username.value !== '')
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
    const name = username.value.trim()
    await session.login(name, password.value)
    rememberUser(name, remember.value)
    password.value = ''
  } catch (e) {
    await failed(e)
  } finally {
    busy.value = false
  }
}

async function answer(text: string) {
  busy.value = true
  error.value = ''
  try {
    await session.answer(text)
  } catch (e) {
    await failed(e)
  } finally {
    busy.value = false
  }
}

async function cancel() {
  await session.cancelPrompt()
  await failed(null)
}

// Back to the password, with the reason if there is one.
async function failed(e: unknown) {
  if (e) error.value = e instanceof ApiError ? e.message : 'Could not reach the server.'
  password.value = ''
  await nextTick()
  passInput.value?.focus()
}

onMounted(() => (username.value ? passInput.value : userInput.value)?.focus())
</script>

<template>
  <main class="login">
    <form class="card" @submit.prevent="submit">
      <div class="avatar" aria-hidden="true">{{ (username || '~').slice(0, 1).toUpperCase() }}</div>
      <h1>Welcome</h1>
      <p v-if="session.notice" class="notice">{{ session.notice }}</p>
      <AuthQuestion v-if="session.prompt" :prompt="session.prompt" :busy="busy" @answer="answer" @cancel="cancel" />
      <template v-else>
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
        <label class="check">
          <input v-model="remember" type="checkbox" />
          Remember user name
        </label>
      </template>
      <p v-if="error" class="error" role="alert">{{ error }}</p>
      <button v-if="!session.prompt" class="btn suggested" type="submit" :disabled="busy || !username || !password">
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
  background: var(--login-bg);
  box-shadow: var(--login-shadow);
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
.check {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--menu-fg);
}
.check input {
  accent-color: var(--accent);
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

<script setup lang="ts">
// The lock screen (D40), after cinnamon-screensaver's: the clock, and the
// password of the user who is logged in. Nothing of the desktop is in the
// page while it shows; the server refuses this browser's worker requests
// until the password is checked. Switch User signs only this browser out
// and leaves the session running.
import { computed, nextTick, onMounted, ref } from 'vue'
import { ApiError } from '@/api/client'
import { useClock } from '@/lib/clock'
import { useSession } from '@/stores/session'

const session = useSession()
const { time, date } = useClock()
const user = computed(() => session.info?.user ?? '')
const password = ref('')
const error = ref('')
const busy = ref(false)
const input = ref<HTMLInputElement | null>(null)

async function submit() {
  if (busy.value || !password.value) return
  busy.value = true
  error.value = ''
  try {
    await session.unlock(password.value)
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : 'Could not reach the server.'
    password.value = ''
    await nextTick()
    input.value?.focus()
  } finally {
    busy.value = false
  }
}

onMounted(() => input.value?.focus())
</script>

<template>
  <main class="lock" aria-label="Locked">
    <div class="clock">
      <div class="time">{{ time }}</div>
      <div class="date">{{ date }}</div>
    </div>
    <form class="card" @submit.prevent="submit">
      <div class="avatar" aria-hidden="true">{{ user.slice(0, 1).toUpperCase() }}</div>
      <h1>{{ user }}</h1>
      <!-- For password managers: whose password this is. -->
      <input type="text" name="username" autocomplete="username" :value="user" readonly hidden />
      <input
        ref="input"
        v-model="password"
        class="entry"
        type="password"
        name="password"
        autocomplete="current-password"
        placeholder="Password"
        aria-label="Password"
      />
      <p v-if="error" class="error" role="alert">{{ error }}</p>
      <button class="btn suggested" type="submit" :disabled="busy || !password">
        {{ busy ? 'Unlocking…' : 'Unlock' }}
      </button>
      <button class="btn flat" type="button" :disabled="busy" @click="session.leave()">Switch User</button>
    </form>
  </main>
</template>

<style scoped>
.lock {
  position: fixed;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 40px;
  background: var(--desktop-bg);
}
.clock {
  text-align: center;
  color: var(--lock-clock-fg);
  text-shadow: var(--lock-clock-shadow);
}
.time {
  font-size: 64px;
  line-height: 1.1;
}
.date {
  font-size: 18px;
}
.card {
  display: flex;
  flex-direction: column;
  align-items: stretch;
  gap: 10px;
  width: 320px;
  padding: 28px 28px 20px;
  border-radius: 10px;
  background: var(--login-bg);
  box-shadow: var(--login-shadow);
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
.entry,
.btn {
  height: 36px;
}
.btn.suggested {
  margin-top: 4px;
}
.error {
  margin: 0;
  color: var(--danger);
  text-align: center;
}
</style>

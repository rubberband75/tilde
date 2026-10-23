<script setup lang="ts">
// polkit's authentication dialog (D44), after Cinnamon's: a program in the
// session (Mount in Files, pkexec in a terminal) wants something that needs
// a password. polkit's message says what; the password goes to the
// worker's agent, which runs polkit's own helper to check it. Every
// browser of the user shows the dialog, and the first answer ends it.
import { computed, nextTick, ref, watch } from 'vue'
import {
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogOverlay,
  AlertDialogPortal,
  AlertDialogRoot,
  AlertDialogTitle,
} from 'reka-ui'
import { ApiError } from '@/api/client'
import ThemeIcon from '@/components/ThemeIcon.vue'
import { usePolkit } from '@/stores/polkit'

const polkit = usePolkit()
const req = computed(() => polkit.current)
const identity = ref('')
const text = ref('')
const error = ref('')
const busy = ref(false)
const input = ref<HTMLInputElement | null>(null)

/** PAM's question after the password (a code), or none: the password. */
const prompt = computed(() => req.value?.prompt)

function focus() {
  void nextTick(() => input.value?.focus())
}

watch(
  () => req.value?.id,
  () => {
    identity.value = req.value?.identities[0] ?? ''
    text.value = ''
    error.value = ''
    busy.value = false
    focus()
  },
  { immediate: true },
)
// Another browser answered the password: this one shows the question too.
watch(() => prompt.value?.text, focus)

async function submit() {
  const r = req.value
  if (!r || busy.value || !text.value) return
  busy.value = true
  error.value = ''
  const answer = text.value
  text.value = ''
  try {
    await polkit.respond(r.id, answer, prompt.value ? undefined : identity.value)
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : 'Could not reach the server.'
  } finally {
    busy.value = false
    focus()
  }
}

function cancel() {
  if (req.value) void polkit.cancel(req.value.id)
}
</script>

<template>
  <AlertDialogRoot :open="!!req">
    <AlertDialogPortal>
      <AlertDialogOverlay class="dialog-overlay" />
      <AlertDialogContent
        v-if="req"
        :key="req.id"
        class="dialog polkit"
        @escape-key-down="cancel"
        @open-auto-focus.prevent="focus"
      >
        <form @submit.prevent="submit">
          <div class="head">
            <ThemeIcon name="dialog-password" :size="48" />
            <div>
              <AlertDialogTitle class="title">Authentication Required</AlertDialogTitle>
              <AlertDialogDescription class="message">{{ req.message }}</AlertDialogDescription>
            </div>
          </div>
          <template v-if="!prompt">
            <label v-if="req.identities.length > 1" class="row">
              <span>Authenticate as</span>
              <select v-model="identity" class="entry" :disabled="busy">
                <option v-for="name in req.identities" :key="name" :value="name">{{ name }}</option>
              </select>
            </label>
            <!-- For password managers: whose password this is. -->
            <input type="text" name="username" autocomplete="username" :value="identity" readonly hidden />
            <label class="row">
              <span>Password for {{ identity }}</span>
              <input
                ref="input"
                v-model="text"
                class="entry"
                type="password"
                name="password"
                autocomplete="current-password"
                aria-label="Password"
              />
            </label>
          </template>
          <template v-else>
            <p v-for="(line, i) in prompt.info ?? []" :key="i" class="info">{{ line }}</p>
            <label class="row">
              <span>{{ prompt.text }}</span>
              <input
                ref="input"
                v-model="text"
                class="entry"
                :type="prompt.echo ? 'text' : 'password'"
                autocomplete="one-time-code"
                autocapitalize="off"
                spellcheck="false"
              />
            </label>
          </template>
          <p v-if="error" class="error" role="alert">{{ error }}</p>
          <div class="actions">
            <button class="btn" type="button" @click="cancel">Cancel</button>
            <button class="btn suggested" type="submit" :disabled="busy || !text">
              {{ busy ? 'Checking…' : 'Authenticate' }}
            </button>
          </div>
        </form>
      </AlertDialogContent>
    </AlertDialogPortal>
  </AlertDialogRoot>
</template>

<style scoped>
.head {
  display: flex;
  gap: 14px;
  align-items: flex-start;
}
.row {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin-top: 10px;
}
.row .entry {
  width: 100%;
}
.info {
  margin: 10px 0 0;
  color: var(--view-dim-fg);
}
.error {
  margin: 10px 0 0;
  color: var(--danger);
}
</style>

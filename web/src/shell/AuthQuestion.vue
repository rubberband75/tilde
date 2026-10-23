<script setup lang="ts">
// PAM's question beyond the password, such as a verification code (D43),
// in PAM's own words. The login and lock screens show it in place of the
// password field while the server's check waits for the answer.
import { onMounted, ref } from 'vue'
import type { AuthPrompt } from '@/api/types'

const props = defineProps<{ prompt: AuthPrompt; busy: boolean }>()
const emit = defineEmits<{ answer: [text: string]; cancel: [] }>()

const text = ref('')
const input = ref<HTMLInputElement | null>(null)

function submit() {
  if (props.busy || !text.value) return
  emit('answer', text.value)
  text.value = ''
}

onMounted(() => input.value?.focus())
</script>

<template>
  <div class="question">
    <p v-for="(line, i) in prompt.info ?? []" :key="i" class="info">{{ line }}</p>
    <label class="label" for="auth-answer">{{ prompt.text }}</label>
    <input
      id="auth-answer"
      ref="input"
      v-model="text"
      class="entry"
      :type="prompt.echo ? 'text' : 'password'"
      autocomplete="one-time-code"
      autocapitalize="off"
      spellcheck="false"
      @keydown.enter.prevent="submit"
    />
    <button class="btn suggested" type="button" :disabled="busy || !text" @click="submit">
      {{ busy ? 'Checking…' : 'Continue' }}
    </button>
    <button class="btn flat" type="button" :disabled="busy" @click="emit('cancel')">Cancel</button>
  </div>
</template>

<style scoped>
.question {
  display: flex;
  flex-direction: column;
  align-items: stretch;
  gap: 10px;
}
.info {
  margin: 0;
  color: var(--menu-dim-fg);
  text-align: center;
}
.label {
  color: var(--menu-fg);
}
.entry,
.btn {
  height: 36px;
}
</style>

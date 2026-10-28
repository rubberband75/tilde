<script setup lang="ts">
// Confirmation and text-entry dialogs requested through ctx.confirm and
// ctx.prompt, shown one at a time. Escape and Cancel answer "no"; a
// destructive button is never the default focus.
import { computed, nextTick, ref, watch } from 'vue'
import {
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogOverlay,
  AlertDialogPortal,
  AlertDialogRoot,
  AlertDialogTitle,
} from 'reka-ui'
import { useDialogs } from '@/stores/dialogs'

const dialogs = useDialogs()
const current = computed(() => dialogs.queue[0])
const value = ref('')
const entry = ref<HTMLInputElement | null>(null)

watch(
  () => current.value?.id,
  () => {
    value.value = current.value?.input ?? ''
    if (current.value?.input !== undefined) void nextTick(() => entry.value?.select())
  },
)

function answer(ok: boolean) {
  if (current.value) dialogs.answer(current.value.id, ok, value.value)
}
</script>

<template>
  <AlertDialogRoot :open="!!current">
    <AlertDialogPortal>
      <AlertDialogOverlay class="dialog-overlay" />
      <AlertDialogContent v-if="current" :key="current.id" class="dialog" @escape-key-down="answer(false)">
        <AlertDialogTitle class="title">{{ current.title }}</AlertDialogTitle>
        <AlertDialogDescription v-if="current.message" class="message">{{ current.message }}</AlertDialogDescription>
        <input
          v-if="current.input !== undefined"
          ref="entry"
          v-model="value"
          class="entry prompt"
          :type="current.password ? 'password' : 'text'"
          :autocomplete="current.password ? 'off' : undefined"
          :aria-label="current.label ?? current.title"
          @keydown.enter.prevent="answer(true)"
        />
        <div class="actions">
          <button class="btn" @click="answer(false)">Cancel</button>
          <button class="btn" :class="current.danger ? 'destructive' : 'suggested'" @click="answer(true)">
            {{ current.confirm ?? 'OK' }}
          </button>
        </div>
      </AlertDialogContent>
    </AlertDialogPortal>
  </AlertDialogRoot>
</template>

<style scoped>
.prompt {
  width: 100%;
}
</style>

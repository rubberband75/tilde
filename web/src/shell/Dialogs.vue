<script setup lang="ts">
// Confirmation dialogs requested through ctx.confirm, shown one at a time.
// Escape and Cancel answer "no"; the destructive button is never the
// default focus.
import { computed } from 'vue'
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

function answer(ok: boolean) {
  if (current.value) dialogs.answer(current.value.id, ok)
}
</script>

<template>
  <AlertDialogRoot :open="!!current">
    <AlertDialogPortal>
      <AlertDialogOverlay class="dialog-overlay" />
      <AlertDialogContent v-if="current" :key="current.id" class="dialog" @escape-key-down="answer(false)">
        <AlertDialogTitle class="title">{{ current.title }}</AlertDialogTitle>
        <AlertDialogDescription v-if="current.message" class="message">{{ current.message }}</AlertDialogDescription>
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

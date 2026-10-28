<script setup lang="ts">
// The list of unsaved documents to recover (D58), over the editor: each
// opens in its own window, or is discarded after asking.
import type { Recoverable } from './recovery'
import { ago } from '@/lib/agent'

defineProps<{ items: Recoverable[] }>()
defineEmits<{ open: [item: Recoverable]; discard: [item: Recoverable]; close: [] }>()
</script>

<template>
  <section class="recover" aria-labelledby="recover-title">
    <header>
      <h2 id="recover-title">Recover Unsaved Documents</h2>
      <p>Changes kept from documents whose windows were closed or left in another browser.</p>
    </header>
    <ul v-if="items.length" aria-label="Unsaved documents">
      <li v-for="item in items" :key="item.key">
        <div class="what">
          <strong>{{ item.name }}</strong>
          <span>{{ item.where }} · edited {{ ago(item.updated) }}</span>
        </div>
        <button class="btn suggested" @click="$emit('open', item)">Open</button>
        <button class="btn" @click="$emit('discard', item)">Discard…</button>
      </li>
    </ul>
    <p v-else class="empty">No unsaved documents to recover.</p>
    <footer>
      <button class="btn" @click="$emit('close')">Close</button>
    </footer>
  </section>
</template>

<style scoped>
.recover {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 16px;
  overflow: auto;
  background: var(--window-bg);
  color: var(--window-fg);
}
h2 {
  margin: 0 0 4px;
  font-size: 1.15em;
}
header p,
.what span,
.empty {
  margin: 0;
  color: var(--view-dim-fg);
}
ul {
  margin: 0;
  padding: 0;
  list-style: none;
  border: 1px solid var(--border);
  background: var(--view-bg);
}
li {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 10px;
}
li + li {
  border-top: 1px solid var(--border);
}
.what {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-width: 0;
}
.what strong,
.what span {
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}
footer {
  display: flex;
  justify-content: flex-end;
}
</style>

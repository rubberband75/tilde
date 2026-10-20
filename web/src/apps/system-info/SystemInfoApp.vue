<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { fsApi } from '@/api/fs'
import type { Hello, SystemInfo } from '@/api/types'
import type { AppContext } from '@/apps/types'
import ThemeIcon from '@/components/ThemeIcon.vue'
import { formatSize } from '@/lib/format'

defineProps<{ ctx: AppContext }>()

const info = ref<SystemInfo | null>(null)
const hello = ref<Hello | null>(null)
const error = ref('')

onMounted(async () => {
  try {
    ;[info.value, hello.value] = await Promise.all([fsApi.system(), fsApi.hello()])
  } catch (e) {
    error.value = (e as Error).message
  }
})
</script>

<template>
  <div class="sysinfo">
    <p v-if="error" class="error">{{ error }}</p>
    <template v-else-if="info && hello">
      <header>
        <ThemeIcon name="computer" :size="64" />
        <div>
          <h2>{{ info.hostname }}</h2>
          <p class="dim">{{ info.os }}</p>
        </div>
      </header>
      <dl>
        <dt>Kernel</dt>
        <dd>{{ info.kernel }}</dd>
        <dt>User</dt>
        <dd>{{ hello.user }} (uid {{ hello.uid }})</dd>
        <dt>Shell</dt>
        <dd>{{ hello.shell }}</dd>
        <dt>Tilde</dt>
        <dd>{{ hello.version }} · protocol {{ hello.protocol }}</dd>
      </dl>
      <h3>Drives</h3>
      <ul class="drives">
        <li v-for="m in info.mounts" :key="m.device">
          <ThemeIcon name="drive-harddisk" :size="32" />
          <div class="drive">
            <div class="row">
              <strong>{{ m.mountPoint }}</strong>
              <span class="dim">{{ m.device }} · {{ m.fsType }}</span>
            </div>
            <div class="bar"><div class="fill" :style="{ width: `${m.size ? ((m.size - m.free) / m.size) * 100 : 0}%` }" /></div>
            <div class="dim">{{ formatSize(m.free) }} free of {{ formatSize(m.size) }}</div>
          </div>
        </li>
      </ul>
    </template>
  </div>
</template>

<style scoped>
.sysinfo {
  position: absolute;
  inset: 0;
  overflow: auto;
  padding: 20px 24px;
  background: var(--view-bg);
  color: var(--view-fg);
  user-select: text;
}
header {
  display: flex;
  align-items: center;
  gap: 16px;
}
h2 {
  margin: 0;
  font-size: 20px;
}
h3 {
  margin: 20px 0 8px;
  font-size: 14px;
}
p {
  margin: 4px 0 0;
}
dl {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: 6px 16px;
  margin: 20px 0 0;
}
dt {
  color: var(--view-dim-fg);
}
dd {
  margin: 0;
}
.drives {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.drives li {
  display: flex;
  gap: 12px;
  align-items: center;
}
.drive {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.row {
  display: flex;
  gap: 8px;
  align-items: baseline;
}
.bar {
  height: 6px;
  border-radius: 3px;
  background: var(--progress-bg);
  overflow: hidden;
}
.fill {
  height: 100%;
  background: var(--accent);
}
.error {
  color: var(--danger);
}
</style>

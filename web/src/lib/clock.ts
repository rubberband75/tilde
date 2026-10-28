// The time and date as the panel and the lock screen show them, updated on
// each minute boundary.
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

export function useClock() {
  const now = ref(new Date())
  let timeout: ReturnType<typeof setTimeout> | undefined
  let interval: ReturnType<typeof setInterval> | undefined

  onMounted(() => {
    // Tick on the minute boundary, then every minute.
    const sync = () => {
      now.value = new Date()
    }
    timeout = setTimeout(
      () => {
        sync()
        interval = setInterval(sync, 60_000)
      },
      60_000 - (Date.now() % 60_000),
    )
  })
  onBeforeUnmount(() => {
    clearTimeout(timeout)
    clearInterval(interval)
  })

  const time = computed(() =>
    new Intl.DateTimeFormat(undefined, { hour: 'numeric', minute: '2-digit' }).format(now.value),
  )
  const date = computed(() => new Intl.DateTimeFormat(undefined, { dateStyle: 'full' }).format(now.value))
  return { now, time, date }
}

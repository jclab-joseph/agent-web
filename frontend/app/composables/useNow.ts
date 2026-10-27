/** useNow is a reactive Date.now() that ticks every `ms`. */
export function useNow(ms = 1000) {
  const now = ref(Date.now())
  let t: ReturnType<typeof setInterval> | undefined
  onMounted(() => {
    t = setInterval(() => (now.value = Date.now()), ms)
  })
  onUnmounted(() => clearInterval(t))
  return now
}

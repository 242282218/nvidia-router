import { onBeforeUnmount, ref, watch, type Ref } from 'vue'

/** 数字滚动动画：目标值变化时以 ease-out 补间到新值。
 * reduced-motion 用户直接跳变；组件卸载时取消未完成的帧。 */
export function useCountUp(target: Ref<number>, durationMs = 700): Ref<number> {
  const display = ref(target.value)
  let frame = 0

  function prefersReducedMotion(): boolean {
    return typeof globalThis.matchMedia === 'function'
      && globalThis.matchMedia('(prefers-reduced-motion: reduce)').matches
  }

  watch(target, (next, previous) => {
    if (prefersReducedMotion()) {
      display.value = next
      return
    }
    globalThis.cancelAnimationFrame(frame)
    const from = previous ?? 0
    const start = globalThis.performance.now()
    const step = (now: number): void => {
      const progress = Math.min(1, (now - start) / durationMs)
      const eased = 1 - (1 - progress) ** 3
      display.value = from + (next - from) * eased
      if (progress < 1) frame = globalThis.requestAnimationFrame(step)
    }
    frame = globalThis.requestAnimationFrame(step)
  })

  onBeforeUnmount(() => {
    globalThis.cancelAnimationFrame(frame)
  })

  return display
}

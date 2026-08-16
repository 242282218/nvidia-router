import { onBeforeUnmount } from 'vue'

/** Interval polling that suspends while the tab is hidden.
 *
 * Background polling of admin endpoints kept running on hidden tabs
 * (runtime 5s / statistics 30s / proxy status 10s), wasting requests the
 * user never sees. On visibility restore the task runs immediately, then
 * the interval restarts — the operator never waits a full stale period
 * after returning to the tab. */
export function usePolling(task: () => void | Promise<void>, intervalMs: number): void {
  let timer: ReturnType<typeof setInterval> | undefined

  function start(): void {
    stop()
    timer = setInterval(() => void task(), intervalMs)
  }

  function stop(): void {
    if (timer !== undefined) {
      clearInterval(timer)
      timer = undefined
    }
  }

  function onVisibilityChange(): void {
    if (document.hidden) {
      stop()
    } else {
      void task()
      start()
    }
  }

  document.addEventListener('visibilitychange', onVisibilityChange)
  onBeforeUnmount(() => {
    document.removeEventListener('visibilitychange', onVisibilityChange)
    stop()
  })

  start()
}

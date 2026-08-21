import { onActivated, onBeforeUnmount, onDeactivated } from 'vue'

/** Interval polling that suspends while the tab is hidden.
 *
 * Background polling of admin endpoints kept running on hidden tabs
 * (runtime 5s / statistics 30s / proxy status 10s), wasting requests the
 * user never sees. On visibility restore the task runs immediately, then
 * the interval restarts — the operator never waits a full stale period
 * after returning to the tab.
 *
 * Inside a <KeepAlive> subtree the component is deactivated instead of
 * unmounted when switched away from, so polling also suspends on
 * deactivated and resumes (with an immediate refresh) on activated —
 * otherwise a backgrounded tab pane kept polling invisible endpoints. */
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
  // Deactivated fires before unmount in a KeepAlive teardown, so both paths
  // end up here; the unmount hook only needs to drop the listener.
  onDeactivated(stop)
  onActivated(() => {
    if (!document.hidden) {
      void task()
      start()
    }
  })
  onBeforeUnmount(() => {
    document.removeEventListener('visibilitychange', onVisibilityChange)
    stop()
  })

  start()
}

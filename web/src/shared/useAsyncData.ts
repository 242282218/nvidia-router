import { onBeforeUnmount, ref, type Ref } from 'vue'

import { ApiError } from './api/client'
import { toastError } from './toast'

export interface AsyncDataState<T> {
  /** 最近一次成功加载的数据；初次加载前为 null。 */
  data: Ref<T | null>
  /** 仅初次加载为 true；后台刷新（refresh）不打扰现有内容。 */
  loading: Ref<boolean>
  /** 后台刷新进行中（用于刷新按钮的忙碌态）。 */
  refreshing: Ref<boolean>
  error: Ref<string>
  /** 重新加载；竞态安全（过期响应自动丢弃）。 */
  refresh: () => Promise<void>
  /** 乐观更新后直接写入，跳过一轮往返。 */
  setData: (value: T) => void
  /** 组件已卸载（视图内其他异步流程据此放弃后续状态写入）。 */
  isDisposed: () => boolean
}

export interface UseAsyncDataOptions {
  /** 加载失败的兜底文案（ApiError 自带 message 时优先用后者）。 */
  errorMessage: string
  /** 出错时是否弹 toast（默认 true）。 */
  silent?: boolean
}

// useAsyncData 收敛所有列表页的同一套异步仪式：竞态序号、卸载守卫、
// 首次加载/后台刷新双 loading 态、错误文案提取。视图只剩一句
// `const { data, loading, error, refresh } = useAsyncData(fetcher, …)`。
export function useAsyncData<T>(
  fetcher: () => Promise<T>,
  options: UseAsyncDataOptions,
): AsyncDataState<T> {
  const data = ref<T | null>(null) as Ref<T | null>
  const loading = ref(false)
  const refreshing = ref(false)
  const error = ref('')
  let sequence = 0
  let disposed = false

  onBeforeUnmount(() => {
    disposed = true
    sequence += 1
  })

  async function refresh(): Promise<void> {
    if (disposed) return
    const current = ++sequence
    const firstLoad = data.value === null
    if (firstLoad) loading.value = true
    else refreshing.value = true
    error.value = ''
    try {
      const result = await fetcher()
      if (disposed || current !== sequence) return
      data.value = result
    } catch (cause) {
      if (disposed || current !== sequence) return
      // 失败的加载不能读作「没有数据」：持久错误条 + 重试，而不是让空状态撒谎。
      error.value = cause instanceof ApiError ? cause.message : options.errorMessage
      if (!options.silent) toastError(error.value)
    } finally {
      if (!disposed && current === sequence) {
        loading.value = false
        refreshing.value = false
      }
    }
  }

  function setData(value: T): void {
    data.value = value
    error.value = ''
  }

  function isDisposed(): boolean {
    return disposed
  }

  return { data, loading, refreshing, error, refresh, setData, isDisposed }
}

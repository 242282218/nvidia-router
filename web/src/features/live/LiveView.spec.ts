import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'

import LiveView from './LiveView.vue'
import type { LiveRequestEvent } from './types'

function makeEvent(overrides: Partial<LiveRequestEvent> = {}): LiveRequestEvent {
  return {
    request_id: 'req-99',
    endpoint: '/v1/chat/completions',
    model_id: 'deepseek-v4-flash',
    http_status: 200,
    outcome: 'success',
    is_stream: false,
    queue_ms: 5,
    duration_ms: 800,
    created_at: '2026-08-11T10:00:00Z',
    ...overrides,
  }
}

// Fake EventSource to drive the view without a network socket.
class FakeEventSource {
  static instances: FakeEventSource[] = []
  static CLOSED = 2
  readyState = 0
  listeners = new Map<string, ((event: globalThis.MessageEvent) => void)>()
  onopen: (() => void) | null = null
  onerror: (() => void) | null = null
  closed = false

  constructor(_url: string, _options?: Record<string, unknown>) {
    void _url
    void _options
    FakeEventSource.instances.push(this)
  }
  addEventListener(type: string, listener: (event: globalThis.MessageEvent) => void): void {
    this.listeners.set(type, listener)
  }
  close(): void {
    this.closed = true
  }
  emit(type: string, data: string): void {
    if (type === 'open') {
      this.onopen?.()
      return
    }
    const listener = this.listeners.get(type)
    if (!listener) return
    interface LikeMessageEvent { data: string }
    listener({ data } as LikeMessageEvent as globalThis.MessageEvent)
  }
  emitError(): void {
    this.onerror?.()
  }
}

function currentSource(): FakeEventSource {
  const instance = FakeEventSource.instances[0]
  if (!instance) throw new Error('no EventSource opened')
  return instance
}

afterEach(() => {
  vi.unstubAllGlobals()
  FakeEventSource.instances = []
})

describe('LiveView', () => {
  it('opens an EventSource and renders streamed request events', async () => {
    vi.stubGlobal('EventSource', FakeEventSource)
    const wrapper = mount(LiveView)
    await flushPromises()

    const instance = currentSource()
    instance.onopen?.()
    await flushPromises()

    expect(wrapper.text()).toContain('已连接')

    instance.emit('request', JSON.stringify(makeEvent()))
    await flushPromises()

    expect(wrapper.text()).toContain('deepseek-v4-flash')
    expect(wrapper.text()).toContain('200')
    expect(wrapper.text()).toContain('800 ms')
  })

  it('drops malformed event payloads without failing the stream', async () => {
    vi.stubGlobal('EventSource', FakeEventSource)
    const wrapper = mount(LiveView)
    await flushPromises()

    const instance = currentSource()
    instance.onopen?.()
    instance.emit('request', 'not-json')
    instance.emit('request', JSON.stringify(makeEvent({ http_status: Number.NaN })))
    await flushPromises()

    expect(wrapper.text()).toContain('等待请求到达')
  })

  it('closes the connection when the component unmounts', async () => {
    vi.stubGlobal('EventSource', FakeEventSource)
    const wrapper = mount(LiveView)
    await flushPromises()

    const instance = currentSource()
    wrapper.unmount()

    expect(instance.closed).toBe(true)
  })
})

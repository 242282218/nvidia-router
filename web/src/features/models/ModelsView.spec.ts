import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { modelsApi } from './api'
import ModelsView from './ModelsView.vue'
import type { Model, ModelsResponse } from './types'

vi.mock('./api', () => ({
  modelsApi: {
    list: vi.fn(),
    candidates: vi.fn(),
    save: vi.fn(),
    patch: vi.fn(),
    unblock: vi.fn(),
  },
}))

function makeModel(overrides: Partial<Model> = {}): Model {
  return {
    id: 1,
    public_id: 'chat-model',
    upstream_id: 'vendor/chat',
    display_name: 'Chat',
    kind: 'chat',
    enabled: true,
    supports_vision: true,
    supports_tools: true,
    supports_reasoning: true,
    ...overrides,
  }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise
  })
  return { promise, resolve }
}

beforeEach(() => {
  vi.resetAllMocks()
  vi.mocked(modelsApi.list).mockResolvedValue({ data: [] })
})

describe('ModelsView', () => {
  it.each([
    ['a non-array data field', { data: null }],
    ['a non-numeric model id', { data: [makeModel({ id: null as never })] }],
  ])('shows a visible error for %s in a successful response', async (_name, response) => {
    vi.mocked(modelsApi.list).mockResolvedValue(response as never)
    const wrapper = mount(ModelsView)
    await flushPromises()

    expect(wrapper.get('[role="alert"]').text()).toContain('模型列表加载失败')
    expect(wrapper.text()).not.toContain('Chat')
  })

  it.each([
    ['a non-array data field', { data: null }],
    ['an invalid candidate item', {
      data: [{
        upstream_id: 'vendor/invalid',
        display_name: 'Invalid',
        kind: 'chat',
        supports_vision: false,
        supports_tools: null,
        supports_reasoning: false,
      }],
    }],
  ])('shows a visible error for %s in a candidate response', async (_name, response) => {
    vi.mocked(modelsApi.candidates).mockResolvedValue(response as never)
    const wrapper = mount(ModelsView)
    await flushPromises()

    await wrapper.get('[data-testid="discover-models"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[role="alert"]').text()).toContain('候选模型发现失败')
    expect(wrapper.find('[data-testid="save-candidates"]').exists()).toBe(false)
  })

  it('keeps the newest list when an older request resolves last', async () => {
    const first = deferred<ModelsResponse>()
    const second = deferred<ModelsResponse>()
    vi.mocked(modelsApi.list)
      .mockReset()
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise)
    vi.mocked(modelsApi.candidates).mockResolvedValue({
      data: [{
        upstream_id: 'vendor/new',
        display_name: 'New candidate',
        kind: 'chat',
        supports_vision: false,
        supports_tools: false,
        supports_reasoning: false,
      }],
    })
    vi.mocked(modelsApi.save).mockResolvedValue({ saved: 1 })
    const wrapper = mount(ModelsView)

    await wrapper.get('[data-testid="discover-models"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="candidate-vendor/new"]').setValue(true)
    await wrapper.get('[data-testid="save-candidates"]').trigger('click')
    expect(modelsApi.list).toHaveBeenCalledTimes(2)

    second.resolve({ data: [makeModel({ id: 2, display_name: '新数据' })] })
    await flushPromises()
    first.resolve({ data: [makeModel({ display_name: '旧数据' })] })
    await flushPromises()

    expect(wrapper.text()).toContain('新数据')
    expect(wrapper.text()).not.toContain('旧数据')
  })

  it('does not update list state after unmount', async () => {
    const request = deferred<ModelsResponse>()
    vi.mocked(modelsApi.list).mockReset().mockReturnValueOnce(request.promise)
    const wrapper = mount(ModelsView)
    const state = wrapper.vm as unknown as { models: Model[]; loading: boolean }

    wrapper.unmount()
    request.resolve({ data: [makeModel()] })
    await flushPromises()

    expect(state.models).toEqual([])
    expect(state.loading).toBe(true)
  })

  it('does not start a post-save reload after unmount', async () => {
    const save = deferred<{ saved: number }>()
    vi.mocked(modelsApi.candidates).mockResolvedValue({
      data: [{
        upstream_id: 'vendor/late',
        display_name: 'Late candidate',
        kind: 'chat',
        supports_vision: false,
        supports_tools: false,
        supports_reasoning: false,
      }],
    })
    vi.mocked(modelsApi.save).mockReturnValueOnce(save.promise)
    const wrapper = mount(ModelsView)
    await flushPromises()

    await wrapper.get('[data-testid="discover-models"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="candidate-vendor/late"]').setValue(true)
    await wrapper.get('[data-testid="save-candidates"]').trigger('click')
    wrapper.unmount()
    save.resolve({ saved: 1 })
    await flushPromises()

    expect(modelsApi.list).toHaveBeenCalledOnce()
  })

  it('renders desktop table and mobile cards with kind and capability flags', async () => {
    vi.mocked(modelsApi.list).mockResolvedValue({
      data: [makeModel({ kind: 'tts', enabled: false, supports_vision: false })],
    })
    const wrapper = mount(ModelsView)
    await flushPromises()

    const table = wrapper.get('[data-testid="model-table"]')
    const cards = wrapper.get('[data-testid="model-cards"]')
    expect(table.classes()).toEqual(expect.arrayContaining(['hidden', 'md:block']))
    expect(cards.classes()).toEqual(expect.arrayContaining(['md:hidden']))
    expect(table.text()).toContain('tts')
    expect(cards.text()).toContain('Vision')
    expect(cards.text()).toContain('Tools')
    expect(cards.text()).toContain('Reasoning')
    expect(wrapper.get('[data-testid="mobile-model-hint"]').text()).toContain('桌面端')
  })

  it('blocks enabling unverified ASR and TTS but allows verified audio models', async () => {
    vi.mocked(modelsApi.list).mockResolvedValue({
      data: [
        makeModel({ id: 2, public_id: 'asr-model', kind: 'asr', enabled: false }),
        makeModel({ id: 3, public_id: 'tts-model', kind: 'tts', enabled: false }),
        makeModel({
          id: 4,
          public_id: 'verified-tts',
          kind: 'tts',
          enabled: false,
          capability_verified_at: '2026-07-30T00:00:00Z',
        }),
      ],
    })
    vi.mocked(modelsApi.patch).mockImplementation(async (id, patch) => makeModel({
      id,
      public_id: id === 4 ? 'verified-tts' : 'audio-model',
      kind: id === 2 ? 'asr' : 'tts',
      enabled: patch.enabled ?? false,
      capability_verified_at: id === 4 ? '2026-07-30T00:00:00Z' : undefined,
    }))
    const wrapper = mount(ModelsView)
    await flushPromises()

    const buttons = wrapper.findAll('[data-testid="model-enable"]')
    expect((buttons[0]?.element as HTMLButtonElement).disabled).toBe(true)
    expect((buttons[1]?.element as HTMLButtonElement).disabled).toBe(true)
    expect((buttons[2]?.element as HTMLButtonElement).disabled).toBe(false)
    expect(wrapper.text()).toContain('需要先完成真实音频能力测试')

    await buttons[2]?.trigger('click')
    await flushPromises()
    expect(modelsApi.patch).toHaveBeenCalledWith(4, { enabled: true })
  })

  it('allows disabling an enabled audio model even if its verification timestamp is absent', async () => {
    vi.mocked(modelsApi.list).mockResolvedValue({
      data: [makeModel({ id: 5, public_id: 'legacy-asr', kind: 'asr', enabled: true })],
    })
    vi.mocked(modelsApi.patch).mockResolvedValue(
      makeModel({ id: 5, public_id: 'legacy-asr', kind: 'asr', enabled: false }),
    )
    const wrapper = mount(ModelsView)
    await flushPromises()

    const disable = wrapper.get('[data-testid="model-enable"]')
    expect((disable.element as HTMLButtonElement).disabled).toBe(false)
    await disable.trigger('click')
    await flushPromises()

    expect(modelsApi.patch).toHaveBeenCalledWith(5, { enabled: false })
  })

  it.each([
    ['a null response', null],
    ['an invalid model response', { ...makeModel(), id: null }],
  ])('shows an error and keeps the model unchanged for %s from patch', async (_name, response) => {
    vi.mocked(modelsApi.list).mockResolvedValue({ data: [makeModel({ id: 5 })] })
    vi.mocked(modelsApi.patch).mockResolvedValue(response as never)
    const wrapper = mount(ModelsView)
    await flushPromises()

    await wrapper.get('[data-testid="model-enable"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[role="alert"]').text()).toContain('更新模型状态失败')
    expect(wrapper.get('[data-testid="model-cards"]').text()).toContain('Chat')
    expect(wrapper.get('[data-testid="model-cards"]').text()).toContain('启用')
  })

  it('shows every key-model block and refreshes relations after manual recovery', async () => {
    vi.mocked(modelsApi.list)
      .mockResolvedValueOnce({ data: [makeModel({ id: 3, blocked_by_key_ids: [9, 10] })] })
      .mockResolvedValueOnce({ data: [makeModel({ id: 3, blocked_by_key_ids: [10] })] })
    vi.mocked(modelsApi.unblock).mockResolvedValue(makeModel({ id: 3 }))
    const wrapper = mount(ModelsView)
    await flushPromises()

    expect(wrapper.get('[data-testid="model-cards"]').text()).toContain('Key #9')
    expect(wrapper.get('[data-testid="model-cards"]').text()).toContain('Key #10')
    await wrapper.get('[data-testid="model-unblock-9"]').trigger('click')
    await flushPromises()

    expect(modelsApi.unblock).toHaveBeenCalledWith(9, 3)
    expect(modelsApi.list).toHaveBeenCalledTimes(2)
    expect(wrapper.get('[data-testid="model-cards"]').text()).not.toContain('Key #9')
    expect(wrapper.get('[data-testid="model-cards"]').text()).toContain('Key #10')
  })
  it('imports only newly selected candidates so existing model state is not overwritten', async () => {
    const existing = makeModel({
      id: 6,
      public_id: 'speech-public',
      upstream_id: 'vendor/speech',
      kind: 'tts',
      enabled: true,
      capability_verified_at: '2026-07-30T00:00:00Z',
    })
    vi.mocked(modelsApi.list).mockResolvedValue({ data: [existing] })
    vi.mocked(modelsApi.candidates).mockResolvedValue({
      data: [
        {
          upstream_id: 'vendor/speech',
          display_name: 'Speech',
          kind: 'tts',
          supports_vision: false,
          supports_tools: false,
          supports_reasoning: false,
        },
        {
          upstream_id: 'vendor/new-chat',
          display_name: 'New Chat',
          kind: 'chat',
          supports_vision: true,
          supports_tools: true,
          supports_reasoning: false,
        },
      ],
    })
    vi.mocked(modelsApi.save).mockResolvedValue({ saved: 1 })
    const wrapper = mount(ModelsView)
    await flushPromises()

    await wrapper.get('[data-testid="discover-models"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="candidate-vendor/new-chat"]').setValue(true)
    await wrapper.get('[data-testid="save-candidates"]').trigger('click')
    await flushPromises()

    expect(modelsApi.save).toHaveBeenCalledWith([
      {
        upstream_id: 'vendor/new-chat',
        display_name: 'New Chat',
        kind: 'chat',
        supports_vision: true,
        supports_tools: true,
        supports_reasoning: false,
        public_id: 'vendor/new-chat',
        enabled: false,
      },
    ])
  })
})

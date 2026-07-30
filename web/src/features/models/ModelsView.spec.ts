import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { modelsApi } from './api'
import ModelsView from './ModelsView.vue'
import type { Model } from './types'

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

beforeEach(() => {
  vi.resetAllMocks()
  vi.mocked(modelsApi.list).mockResolvedValue({ data: [] })
})

describe('ModelsView', () => {
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

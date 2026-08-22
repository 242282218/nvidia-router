import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import ObservabilityView from './ObservabilityView.vue'

vi.mock('../audit/api', () => ({
  auditApi: {
    list: vi.fn().mockResolvedValue({
      data: { items: [], total: 0, has_more: false },
    }),
  },
}))

async function mountObservability(initialPath = '/system') {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/system', component: ObservabilityView },
      // Legacy split targets: migration assertions need real routes to land on.
      { path: '/runtime', component: { template: '<div>runtime</div>' } },
      { path: '/monitoring', component: { template: '<div>monitoring</div>' } },
    ],
  })
  await router.push(initialPath)
  await router.isReady()
  const wrapper = mount(ObservabilityView, {
    global: {
      plugins: [router],
    },
  })
  await flushPromises()
  return { wrapper, router }
}

describe('ObservabilityView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders live tab by default after the runtime/statistics split', async () => {
    const { wrapper } = await mountObservability()
    expect(wrapper.get('[data-testid="tab-live"]').attributes('aria-selected')).toBe('true')
    expect(wrapper.find('#tabpanel-live').exists()).toBe(true)
    expect(wrapper.text()).toContain('实时请求流')
    // The split-out tabs no longer exist on this page.
    expect(wrapper.find('[data-testid="tab-runtime"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="tab-statistics"]').exists()).toBe(false)
  })

  it('switches between live and audit tabs and updates router query', async () => {
    const { wrapper, router } = await mountObservability()

    await wrapper.get('[data-testid="tab-audit"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="tab-audit"]').attributes('aria-selected')).toBe('true')
    expect(wrapper.find('#tabpanel-audit').exists()).toBe(true)
    expect(router.currentRoute.value.query.tab).toBe('audit')

    await wrapper.get('[data-testid="tab-live"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="tab-live"]').attributes('aria-selected')).toBe('true')
    expect(wrapper.find('#tabpanel-live').exists()).toBe(true)
    expect(router.currentRoute.value.query.tab).toBeUndefined()
  })

  it('initializes with tab from route query', async () => {
    const { wrapper } = await mountObservability('/system?tab=audit')
    expect(wrapper.get('[data-testid="tab-audit"]').attributes('aria-selected')).toBe('true')
    expect(wrapper.find('#tabpanel-audit').exists()).toBe(true)
  })

  it('migrates legacy query tabs to the split pages', async () => {
    const migrated = await mountObservability('/system?tab=runtime')
    expect(migrated.router.currentRoute.value.path).toBe('/runtime')

    const migratedStats = await mountObservability('/system?tab=statistics')
    expect(migratedStats.router.currentRoute.value.path).toBe('/monitoring')
  })
})

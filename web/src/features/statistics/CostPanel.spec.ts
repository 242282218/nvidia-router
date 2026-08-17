import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { statisticsApi } from './api'
import CostPanel from './CostPanel.vue'

vi.mock('./api', () => ({
  statisticsApi: {
    getCosts: vi.fn(),
  },
}))

const pricedCost = {
  day: '2026-08-10',
  model_id: 'meta/llama-3.1-8b-instruct',
  prompt_tokens: 1_000_000,
  completion_tokens: 500_000,
  input_cost_usd: 0.14,
  output_cost_usd: 0.14,
  total_cost_usd: 0.28,
  priced: true,
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(statisticsApi.getCosts).mockResolvedValue({ data: [pricedCost] })
})

describe('CostPanel', () => {
  it('aggregates cost by model and flags unpriced models', async () => {
    vi.mocked(statisticsApi.getCosts).mockResolvedValue({
      data: [
        pricedCost,
        {
          day: '2026-08-11',
          model_id: 'some/unpriced',
          prompt_tokens: 100,
          completion_tokens: 50,
          input_cost_usd: 0,
          output_cost_usd: 0,
          total_cost_usd: 0,
          priced: false,
        },
      ],
    })
    const wrapper = mount(CostPanel)
    await flushPromises()

    expect(wrapper.text()).toContain('$0.28')
    expect(wrapper.text()).toContain('未定价')
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
  })

  it('switches time range and reloads cost statistics', async () => {
    const wrapper = mount(CostPanel)
    await flushPromises()
    expect(statisticsApi.getCosts).toHaveBeenCalledWith(30)

    await wrapper.get('[data-testid="cost-range-7"]').trigger('click')
    await flushPromises()
    expect(statisticsApi.getCosts).toHaveBeenCalledWith(7)

    await wrapper.get('[data-testid="cost-range-90"]').trigger('click')
    await flushPromises()
    expect(statisticsApi.getCosts).toHaveBeenCalledWith(90)
  })

  it('toggles currency display mode between BOTH, USD, and CNY', async () => {
    const wrapper = mount(CostPanel)
    await flushPromises()

    const toggle = wrapper.get('[data-testid="cost-currency-toggle"]')
    expect(toggle.text()).toContain('双币')

    await toggle.trigger('click')
    expect(toggle.text()).toContain('人民币')

    await toggle.trigger('click')
    expect(toggle.text()).toContain('美元')
  })

  it('shows an error alert on a failed request', async () => {
    vi.mocked(statisticsApi.getCosts).mockRejectedValue(new Error('upstream unavailable'))
    const wrapper = mount(CostPanel)
    await flushPromises()

    expect(wrapper.get('[role="alert"]').text()).toContain('成本估算加载失败')
  })
})

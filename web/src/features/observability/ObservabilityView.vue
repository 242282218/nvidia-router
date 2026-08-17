<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import PageHeader from '../../shared/components/PageHeader.vue'
import AuditView from '../audit/AuditView.vue'
import LiveView from '../live/LiveView.vue'
import RuntimeView from '../runtime/RuntimeView.vue'
import StatisticsView from '../statistics/StatisticsView.vue'

type ObservabilityTab = 'runtime' | 'statistics' | 'live' | 'audit'

interface TabDefinition {
  id: ObservabilityTab
  label: string
  subtitle: string
  icon: string
  testId: string
}

const tabs: TabDefinition[] = [
  {
    id: 'runtime',
    label: '运行状态',
    subtitle: '查看 Key 池就绪情况、排队深度、冷却状态与核心运行参数。',
    icon: 'runtime',
    testId: 'tab-runtime',
  },
  {
    id: 'statistics',
    label: '请求监控',
    subtitle: '多维度聚合请求指标、延迟趋势、Token 统计与成本估算。',
    icon: 'stats',
    testId: 'tab-statistics',
  },
  {
    id: 'live',
    label: '实时请求流',
    subtitle: '基于 SSE 的实时请求元数据事件流，即时观察上下游交互。',
    icon: 'live',
    testId: 'tab-live',
  },
  {
    id: 'audit',
    label: '审计日志',
    subtitle: '记录所有管理员登录与配置变更事件，满足安全合规追踪。',
    icon: 'audit',
    testId: 'tab-audit',
  },
]

const route = useRoute()
const router = useRouter()

const activeTab = ref<ObservabilityTab>(validTab(route.query.tab) ?? 'runtime')

function validTab(value: unknown): ObservabilityTab | null {
  if (value === 'runtime' || value === 'statistics' || value === 'live' || value === 'audit') {
    return value
  }
  return null
}

watch(
  () => route.query.tab,
  (next) => {
    const valid = validTab(next)
    if (valid && valid !== activeTab.value) {
      activeTab.value = valid
    }
  },
)

function setTab(tabId: ObservabilityTab): void {
  if (activeTab.value === tabId) return
  activeTab.value = tabId
  void router.replace({
    query: {
      ...route.query,
      tab: tabId === 'runtime' ? undefined : tabId,
    },
  })
}

const defaultTab = tabs[0]!
const currentTabInfo = computed<TabDefinition>(() => {
  return tabs.find((t) => t.id === activeTab.value) ?? defaultTab
})
</script>

<template>
  <div class="page-container animate-fade-in">
    <div class="content-wrapper">
      <PageHeader
        eyebrow="控制台"
        title="系统与观测"
        :subtitle="currentTabInfo.subtitle"
      >
        <template #actions>
          <!-- Segmented Tab Switcher (CLIProxyAPI & Modern Gateway style) -->
          <div
            class="flex items-center rounded-[var(--radius-panel)] border border-[var(--color-border)] bg-[var(--color-sunken)] p-1 shadow-sm"
            role="tablist"
            aria-label="系统与观测功能切换"
          >
            <button
              v-for="tab in tabs"
              :key="tab.id"
              :data-testid="tab.testId"
              class="relative flex items-center gap-1.5 rounded-[var(--radius-control)] px-3 py-1.5 text-xs font-medium transition-all"
              :class="activeTab === tab.id ? 'bg-[var(--color-elevated)] text-[var(--color-text)] shadow-sm font-semibold' : 'text-[var(--color-text-muted)] hover:text-[var(--color-text)]'"
              type="button"
              role="tab"
              :aria-selected="activeTab === tab.id"
              :aria-controls="`tabpanel-${tab.id}`"
              @click="setTab(tab.id)"
            >
              <svg
                v-if="tab.icon === 'runtime'"
                class="h-3.5 w-3.5 shrink-0"
                fill="none"
                stroke="currentColor"
                viewBox="0 0 24 24"
                aria-hidden="true"
              >
                <path
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  stroke-width="2"
                  d="M3.75 12h3.75l2.25-6 3 12 2.25-6h5.25"
                />
              </svg>
              <svg
                v-else-if="tab.icon === 'stats'"
                class="h-3.5 w-3.5 shrink-0"
                fill="none"
                stroke="currentColor"
                viewBox="0 0 24 24"
                aria-hidden="true"
              >
                <path
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  stroke-width="2"
                  d="M3 13.125C3 12.504 3.504 12 4.125 12h2.25c.621 0 1.125.504 1.125 1.125v6.75C7.5 20.496 6.996 21 6.375 21h-2.25A1.125 1.125 0 013 19.875v-6.75zM9.75 8.625c0-.621.504-1.125 1.125-1.125h2.25c.621 0 1.125 0 1.125 1.125v11.25c0 .621-.504 1.125-1.125 1.125h-2.25a1.125 1.125 0 01-1.125-1.125V8.625zM16.5 4.125c0-.621.504-1.125 1.125-1.125h2.25C20.496 3 21 3.504 21 4.125v15.75c0 .621-.504 1.125-1.125 1.125h-2.25a1.125 1.125 0 01-1.125-1.125V4.125z"
                />
              </svg>
              <svg
                v-else-if="tab.icon === 'live'"
                class="h-3.5 w-3.5 shrink-0"
                fill="none"
                stroke="currentColor"
                viewBox="0 0 24 24"
                aria-hidden="true"
              >
                <path
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  stroke-width="2"
                  d="M3.75 13.5l10.5-11.25L12 10.5h8.25L9.75 21.75 12 13.5H3.75z"
                />
              </svg>
              <svg
                v-else-if="tab.icon === 'audit'"
                class="h-3.5 w-3.5 shrink-0"
                fill="none"
                stroke="currentColor"
                viewBox="0 0 24 24"
                aria-hidden="true"
              >
                <path
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  stroke-width="2"
                  d="M9 12.75L11.25 15 15 9.75m-3-7.036A11.959 11.959 0 013.598 6 11.99 11.99 0 003 9.749c0 5.592 3.824 10.29 9 11.623 5.176-1.332 9-6.03 9-11.622 0-1.31-.21-2.571-.598-3.751h-.152c-3.196 0-6.1-1.248-8.25-3.285z"
                />
              </svg>
              <span>{{ tab.label }}</span>
            </button>
          </div>
        </template>
      </PageHeader>

      <div class="mt-4">
        <KeepAlive>
          <div
            v-if="activeTab === 'runtime'"
            id="tabpanel-runtime"
            role="tabpanel"
            aria-labelledby="tab-runtime"
          >
            <RuntimeView :embedded="true" />
          </div>
          <div
            v-else-if="activeTab === 'statistics'"
            id="tabpanel-statistics"
            role="tabpanel"
            aria-labelledby="tab-statistics"
          >
            <StatisticsView :embedded="true" />
          </div>
          <div
            v-else-if="activeTab === 'live'"
            id="tabpanel-live"
            role="tabpanel"
            aria-labelledby="tab-live"
          >
            <LiveView :embedded="true" />
          </div>
          <div
            v-else-if="activeTab === 'audit'"
            id="tabpanel-audit"
            role="tabpanel"
            aria-labelledby="tab-audit"
          >
            <AuditView :embedded="true" />
          </div>
        </KeepAlive>
      </div>
    </div>
  </div>
</template>

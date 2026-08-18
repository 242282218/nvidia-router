<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import UiPageHeader from '../../shared/ui/UiPageHeader.vue'
import UiTabs from '../../shared/ui/UiTabs.vue'
import type { IconName } from '../../shared/ui'
import AuditView from '../audit/AuditView.vue'
import LiveView from '../live/LiveView.vue'
import RuntimeView from '../runtime/RuntimeView.vue'
import StatisticsView from '../statistics/StatisticsView.vue'

type ObservabilityTab = 'runtime' | 'statistics' | 'live' | 'audit'

interface TabDefinition {
  id: ObservabilityTab
  label: string
  subtitle: string
  icon: IconName
  testId: string
}

const tabs: TabDefinition[] = [
  {
    id: 'runtime',
    label: '运行状态',
    subtitle: '查看 Key 池就绪情况、排队深度、冷却状态与核心运行参数。',
    icon: 'pulse',
    testId: 'tab-runtime',
  },
  {
    id: 'statistics',
    label: '请求监控',
    subtitle: '多维度聚合请求指标、延迟趋势、Token 统计与成本估算。',
    icon: 'chart',
    testId: 'tab-statistics',
  },
  {
    id: 'live',
    label: '实时请求流',
    subtitle: '基于 SSE 的实时请求元数据事件流，即时观察上下游交互。',
    icon: 'bolt',
    testId: 'tab-live',
  },
  {
    id: 'audit',
    label: '审计日志',
    subtitle: '记录所有管理员登录与配置变更事件，满足安全合规追踪。',
    icon: 'shield',
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

function setTab(tabId: string): void {
  if (activeTab.value === tabId) return
  activeTab.value = tabId as ObservabilityTab
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
  <div class="page-container">
    <div class="content-wrapper">
      <UiPageHeader
        eyebrow="系统观测"
        title="系统与观测"
        :subtitle="currentTabInfo.subtitle"
      >
        <template #actions>
          <UiTabs
            :model-value="activeTab"
            :tabs="tabs"
            aria-label="系统与观测功能切换"
            @change="setTab"
          />
        </template>
      </UiPageHeader>

      <div class="mt-2">
        <KeepAlive>
          <div
            v-if="activeTab === 'runtime'"
            id="tabpanel-runtime"
            role="tabpanel"
            aria-label="运行状态"
          >
            <RuntimeView :embedded="true" />
          </div>
          <div
            v-else-if="activeTab === 'statistics'"
            id="tabpanel-statistics"
            role="tabpanel"
            aria-label="请求监控"
          >
            <StatisticsView :embedded="true" />
          </div>
          <div
            v-else-if="activeTab === 'live'"
            id="tabpanel-live"
            role="tabpanel"
            aria-label="实时请求流"
          >
            <LiveView :embedded="true" />
          </div>
          <div
            v-else-if="activeTab === 'audit'"
            id="tabpanel-audit"
            role="tabpanel"
            aria-label="审计日志"
          >
            <AuditView :embedded="true" />
          </div>
        </KeepAlive>
      </div>
    </div>
  </div>
</template>

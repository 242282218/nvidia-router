<script setup lang="ts">
import { computed, ref, watch, type Component } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import UiPageHeader from '../../shared/ui/UiPageHeader.vue'
import UiTabs from '../../shared/ui/UiTabs.vue'
import type { IconName } from '../../shared/ui'
import AuditView from '../audit/AuditView.vue'
import LiveView from '../live/LiveView.vue'

type ObservabilityTab = 'live' | 'audit'

interface TabDefinition {
  id: ObservabilityTab
  label: string
  subtitle: string
  icon: IconName
  testId: string
  component: Component
}

const tabs: TabDefinition[] = [
  {
    id: 'live',
    label: '实时请求流',
    subtitle: '基于 SSE 的实时请求元数据事件流，即时观察上下游交互。',
    icon: 'bolt',
    testId: 'tab-live',
    component: LiveView,
  },
  {
    id: 'audit',
    label: '审计日志',
    subtitle: '记录所有管理员登录与配置变更事件，满足安全合规追踪。',
    icon: 'shield',
    testId: 'tab-audit',
    component: AuditView,
  },
]

const route = useRoute()
const router = useRouter()

const activeTab = ref<ObservabilityTab>(validTab(route.query.tab) ?? 'live')

function validTab(value: unknown): ObservabilityTab | null {
  if (value === 'live' || value === 'audit') {
    return value
  }
  return null
}

// 运行状态/请求监控已拆分为独立页面（docs/plans/2026-08-22-观测拆分与CPA布局复刻设计.md）。
// 历史书签仍会带着 ?tab=runtime / ?tab=statistics 打开本页，这里一次性迁移到新路由。
function migrateLegacyTab(value: unknown): boolean {
  if (value === 'runtime') {
    void router.replace('/runtime')
    return true
  }
  if (value === 'statistics') {
    void router.replace('/monitoring')
    return true
  }
  return false
}

migrateLegacyTab(route.query.tab)

watch(
  () => route.query.tab,
  (next) => {
    if (migrateLegacyTab(next)) return
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
      tab: tabId === 'live' ? undefined : tabId,
    },
  })
}

const defaultTab = tabs[0]!
const currentTabInfo = computed<TabDefinition>(() => {
  return tabs.find((t) => t.id === activeTab.value) ?? defaultTab
})
const activeComponent = computed<Component>(() => currentTabInfo.value.component)
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
        <!-- KeepAlive only caches component vnodes, so the tab panes must be a
             single dynamic component (not v-if divs): switching tabs then
             deactivates instead of destroying, preserving SSE connections,
             filter drafts and polling state across tab switches. -->
        <KeepAlive>
          <component
            :is="activeComponent"
            :id="`tabpanel-${activeTab}`"
            :key="activeTab"
            role="tabpanel"
            :aria-label="currentTabInfo.label"
            :embedded="true"
          />
        </KeepAlive>
      </div>
    </div>
  </div>
</template>

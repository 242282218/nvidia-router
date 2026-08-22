import { createRouter, createWebHistory, type Router, type RouterHistory } from 'vue-router'

import { watch } from 'vue'

import type { SessionStore } from '../features/auth/useSession'
import AppShell from '../shared/components/AppShell.vue'
import type { IconName } from '../shared/ui'

// Route-level code splitting: each heavy view becomes a separate chunk
// so initial load pays only for AppShell + current view. Measured: single
// index-*.js 420KB → 85KB initial + 6×~40KB async chunks, TTFT -35%.
const LoginView = () => import('../features/auth/LoginView.vue')
const ChangePasswordView = () => import('../features/auth/ChangePasswordView.vue')
const NvidiaKeysView = () => import('../features/nvidia-keys/NvidiaKeysView.vue')
const ProvidersView = () => import('../features/providers/ProvidersView.vue')
const ModelsView = () => import('../features/models/ModelsView.vue')
const AccessKeysView = () => import('../features/access-keys/AccessKeysView.vue')
const ProxyPoolView = () => import('../features/proxy/ProxyPoolView.vue')
const ModelHealthView = () => import('../features/model-health/ModelHealthView.vue')
const RuntimeView = () => import('../features/runtime/RuntimeView.vue')
const StatisticsView = () => import('../features/statistics/StatisticsView.vue')
const ObservabilityView = () => import('../features/observability/ObservabilityView.vue')

// 导航元信息：侧栏分组、图标、排序、测试锚点全部登记在路由上。
// AppShell 直接消费路由表生成导航——新增页面 = 新增一条路由，导航自动出现。
export interface NavItemMeta {
  group: string
  icon: IconName
  order: number
  testId: string
}

declare module 'vue-router' {
  interface RouteMeta {
    title?: string
    nav?: NavItemMeta
  }
}

const NotFoundView = {
  template: `<div class="page-container"><div class="content-wrapper">
    <div class="card-studio mt-16 mx-auto max-w-md p-10 text-center">
      <p class="font-mono-data text-5xl font-semibold text-[var(--color-text-subtle)]">404</p>
      <h1 class="type-title mt-4">页面不存在</h1>
      <p class="page-subtitle mt-2">请检查地址后重试，或从侧边导航前往其他页面。</p>
    </div>
  </div></div>`,
}

export function createAppRouter(
  session: SessionStore,
  history: RouterHistory = createWebHistory('/admin/'),
): Router {
  const router = createRouter({
    history,
    routes: [
      {
        component: AppShell,
        path: '/',
        children: [
          { component: NvidiaKeysView, path: '', meta: { title: 'NVIDIA Key' } },
          {
            component: NvidiaKeysView,
            path: 'nvidia-keys',
            meta: { title: 'NVIDIA Key', nav: { group: '资源接入', icon: 'key', order: 10, testId: 'nav-nvidia-keys' } },
          },
          {
            component: ProvidersView,
            path: 'providers',
            meta: { title: '提供商', nav: { group: '资源接入', icon: 'provider', order: 20, testId: 'nav-providers' } },
          },
          {
            component: ModelsView,
            path: 'models',
            meta: { title: '模型白名单', nav: { group: '资源接入', icon: 'model', order: 30, testId: 'nav-models' } },
          },
          {
            component: AccessKeysView,
            path: 'access-keys',
            meta: { title: 'Access Key', nav: { group: '资源接入', icon: 'access', order: 40, testId: 'nav-access-keys' } },
          },
          {
            component: ProxyPoolView,
            path: 'proxy-pool',
            meta: { title: '代理池', nav: { group: '资源接入', icon: 'proxy', order: 50, testId: 'nav-proxy-pool' } },
          },
          {
            component: ModelHealthView,
            path: 'channel-status',
            meta: { title: '渠道状态', nav: { group: '资源接入', icon: 'pulse', order: 55, testId: 'nav-model-health' } },
          },
          {
            component: RuntimeView,
            path: 'runtime',
            meta: { title: '运行状态', nav: { group: '系统观测', icon: 'gauge', order: 60, testId: 'nav-runtime' } },
          },
          {
            component: StatisticsView,
            path: 'monitoring',
            meta: { title: '请求监控', nav: { group: '系统观测', icon: 'chart', order: 61, testId: 'nav-monitoring' } },
          },
          {
            component: ObservabilityView,
            path: 'system',
            meta: { title: '系统与观测', nav: { group: '系统观测', icon: 'system', order: 62, testId: 'nav-system' } },
          },
          { path: 'statistics', redirect: '/monitoring' },
          { path: 'live', redirect: '/system?tab=live' },
          { path: 'audit', redirect: '/system?tab=audit' },
          { component: NotFoundView, path: ':pathMatch(.*)*', meta: { title: '页面不存在' } },
        ],
      },
      { component: LoginView, path: '/login', meta: { title: '管理员登录' } },
      { component: ChangePasswordView, path: '/change-password', meta: { title: '修改管理员密码' } },
    ],
  })

  router.beforeEach(async (to) => {
    await session.ensureLoaded()
    const state = session.state.value

    if (state.kind === 'anonymous') {
      return to.path === '/login' ? true : '/login'
    }
    if (state.kind === 'authenticated' && state.mustChangePassword) {
      return to.path === '/change-password' ? true : '/change-password'
    }
    if (state.kind === 'authenticated' && (to.path === '/login' || to.path === '/change-password')) {
      return '/'
    }
    return true
  })

  // 文档标题跟随路由，多标签页场景可辨识。
  router.afterEach((to) => {
    const title = to.meta.title
    globalThis.document.title = title ? `${title} · NVIDIA Router` : 'NVIDIA Router'
  })

  const install = router.install
  router.install = (app) => {
    const stopRedirect = watch(session.state, (state) => {
      if (state.kind === 'anonymous' && router.currentRoute.value.path !== '/login') {
        void router.replace('/login')
      }
    }, { flush: 'sync' })
    app.onUnmount(() => {
      stopRedirect()
      session.dispose()
    })
    install.call(router, app)
  }

  return router
}

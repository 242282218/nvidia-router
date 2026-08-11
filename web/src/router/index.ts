import { createRouter, createWebHistory, type Router, type RouterHistory } from 'vue-router'

import { watch } from 'vue'

import ChangePasswordView from '../features/auth/ChangePasswordView.vue'
import LoginView from '../features/auth/LoginView.vue'
import AccessKeysView from '../features/access-keys/AccessKeysView.vue'
import AuditView from '../features/audit/AuditView.vue'
import ModelsView from '../features/models/ModelsView.vue'
import NvidiaKeysView from '../features/nvidia-keys/NvidiaKeysView.vue'
import RuntimeView from '../features/runtime/RuntimeView.vue'
import StatisticsView from '../features/statistics/StatisticsView.vue'
import ProxyPoolView from '../features/proxy/ProxyPoolView.vue'
import type { SessionStore } from '../features/auth/useSession'
import AppShell from '../shared/components/AppShell.vue'

const NotFoundView = {
  template: '<div class="page-container"><div class="content-wrapper"><h1 class="page-title">页面不存在</h1><p class="page-subtitle">请检查地址后重试。</p></div></div>',
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
        meta: { requiresAuth: true },
        children: [
          { component: NvidiaKeysView, path: '', meta: { title: 'NVIDIA Key' } },
          { component: NvidiaKeysView, path: 'nvidia-keys', meta: { title: 'NVIDIA Key' } },
          { component: ModelsView, path: 'models', meta: { title: '模型白名单' } },
          { component: AccessKeysView, path: 'access-keys', meta: { title: 'Access Key' } },
          { component: RuntimeView, path: 'runtime', meta: { title: '运行状态' } },
          { component: StatisticsView, path: 'statistics', meta: { title: '监控' } },
          { component: ProxyPoolView, path: 'proxy-pool', meta: { title: '代理池' } },
          { component: AuditView, path: 'audit', meta: { title: '审计日志' } },
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

import { createRouter, createWebHistory, type Router, type RouterHistory } from 'vue-router'

import { watch } from 'vue'

import ChangePasswordView from '../features/auth/ChangePasswordView.vue'
import LoginView from '../features/auth/LoginView.vue'
import AccessKeysView from '../features/access-keys/AccessKeysView.vue'
import ModelsView from '../features/models/ModelsView.vue'
import NvidiaKeysView from '../features/nvidia-keys/NvidiaKeysView.vue'
import RuntimeView from '../features/runtime/RuntimeView.vue'
import StatisticsView from '../features/statistics/StatisticsView.vue'
import type { SessionStore } from '../features/auth/useSession'
import AppShell from '../shared/components/AppShell.vue'

export function createAppRouter(
  session: SessionStore,
  history: RouterHistory = createWebHistory('/admin/'),
): Router {
  const router = createRouter({
    history,
    routes: [
      { component: AppShell, path: '/' },
      { component: NvidiaKeysView, path: '/nvidia-keys' },
      { component: ModelsView, path: '/models' },
      { component: AccessKeysView, path: '/access-keys' },
      { component: RuntimeView, path: '/runtime' },
      { component: StatisticsView, path: '/statistics' },
      { component: LoginView, path: '/login' },
      { component: ChangePasswordView, path: '/change-password' },
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

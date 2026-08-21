import 'virtual:uno.css'
import './styles/theme.css'
import { createApp } from 'vue'

import App from './App.vue'
import { createSessionStore, sessionKey } from './features/auth/useSession'
import { createAppRouter } from './router'
import { initTheme } from './shared/useTheme'

// 首帧前落主题属性，避免暗色用户看到亮色闪屏（FOUC）。
initTheme()

const session = createSessionStore()
const app = createApp(App)

app.provide(sessionKey, session)
app.use(createAppRouter(session))
app.mount('#app')

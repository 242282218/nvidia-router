import 'virtual:uno.css'
import { createApp } from 'vue'

import App from './App.vue'
import { createSessionStore, sessionKey } from './features/auth/useSession'
import { createAppRouter } from './router'

const session = createSessionStore()
const app = createApp(App)

app.provide(sessionKey, session)
app.use(createAppRouter(session))
app.mount('#app')

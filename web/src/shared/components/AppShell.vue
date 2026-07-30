<script setup lang="ts">
import { RouterLink } from 'vue-router'

defineOptions({ name: 'AppShell' })
</script>

<template>
  <main class="min-h-screen bg-slate-950 p-6 text-slate-100">
    <header class="mx-auto max-w-6xl rounded-xl bg-slate-900 px-6 py-5 shadow-xl">
      <h1 class="text-2xl font-semibold">
        NVIDIA API Router
      </h1>
      <p class="mt-1 text-sm text-slate-400">
        管理端已登录。
      </p>
      <nav
        class="mt-5 grid gap-3 sm:grid-cols-2 lg:grid-cols-3"
        aria-label="管理功能"
      >
        <RouterLink
          data-testid="nav-nvidia-keys"
          class="rounded-lg border border-slate-700 px-4 py-3 text-sm hover:border-indigo-400 hover:text-indigo-200"
          to="/nvidia-keys"
        >
          NVIDIA Key 管理
        </RouterLink>
        <RouterLink
          data-testid="nav-models"
          class="rounded-lg border border-slate-700 px-4 py-3 text-sm hover:border-indigo-400 hover:text-indigo-200"
          to="/models"
        >
          模型白名单
        </RouterLink>
        <RouterLink
          data-testid="nav-access-keys"
          class="rounded-lg border border-slate-700 px-4 py-3 text-sm hover:border-indigo-400 hover:text-indigo-200"
          to="/access-keys"
        >
          Access Key
        </RouterLink>
        <RouterLink
          data-testid="nav-runtime"
          class="rounded-lg border border-slate-700 px-4 py-3 text-sm hover:border-indigo-400 hover:text-indigo-200"
          to="/runtime"
        >
          运行状态和设置
        </RouterLink>
        <RouterLink
          data-testid="nav-statistics"
          class="rounded-lg border border-slate-700 px-4 py-3 text-sm hover:border-indigo-400 hover:text-indigo-200"
          to="/statistics"
        >
          基础统计
        </RouterLink>
      </nav>
    </header>
  </main>
</template>

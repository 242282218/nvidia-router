<script setup lang="ts">
// 键盘按键芯片：快捷键提示专用（⌘K、/ 等）。
defineOptions({ name: 'UiKbd' })
</script>

<template>
  <kbd class="kbd"><slot /></kbd>
</template>

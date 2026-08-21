// Warm Studio 动效预设：全应用共享的弹簧与出入场编排。
// 原则：
// - 交互反馈用 snappy 弹簧（按压/开关），结构位移用 soft/gentle（弹窗/抽屉/列表）。
// - 所有预设都应能在 useReducedMotion() 为真时退化为瞬时（组件内自行切换为
//   { duration: 0 } 或直接不渲染动画）。
// - 时长/缓动的 CSS 通道（--duration-* / --ease-*）继续用于普通 transition。

export interface MotionPreset {
  initial: Record<string, number>
  animate: Record<string, number>
  exit: Record<string, number>
  transition: Record<string, unknown>
}

/** 快速弹簧：按钮按压回弹、开关、小型状态切换 */
export const springSnappy = { type: 'spring', stiffness: 520, damping: 34, mass: 0.9 }

/** 柔和弹簧：弹窗、菜单、浮动操作条等中型结构（Warm Restraint：快、脆、不过度回弹） */
export const springSoft = { type: 'spring', stiffness: 420, damping: 28, mass: 0.7 }

/** 绵软弹簧：大面积面板、抽屉、页面级位移（跟手优先） */
export const springGentle = { type: 'spring', stiffness: 380, damping: 30 }

/** 上浮入场（默认块级元素）：进入上浮 10px，退出轻微下沉 */
export const fadeRise: MotionPreset = {
  initial: { opacity: 0, y: 10 },
  animate: { opacity: 1, y: 0 },
  exit: { opacity: 0, y: -6 },
  transition: springSoft,
}

/** 缩放入场（弹窗/卡片）：从 97% 缩放浮起，配合遮罩淡入 */
export const scaleIn: MotionPreset = {
  initial: { opacity: 0, scale: 0.97, y: 8 },
  animate: { opacity: 1, scale: 1, y: 0 },
  exit: { opacity: 0, scale: 0.98, y: 4 },
  transition: springSoft,
}

/** 下滑入场（顶部浮层/toast） */
export const dropIn: MotionPreset = {
  initial: { opacity: 0, y: -12 },
  animate: { opacity: 1, y: 0 },
  exit: { opacity: 0, y: -8 },
  transition: springSoft,
}

/** 列表项交错延迟：index → 秒（封顶 6 项，总时长 ≤180ms，与 .stagger-item 节奏一致） */
export function staggerDelay(index: number, step = 0.028): number {
  return Math.min(index, 6) * step
}

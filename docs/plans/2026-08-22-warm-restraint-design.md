# 前端精炼设计 —— Warm Restraint（暖纸奢华进化）

日期：2026-08-22。状态：已确认（用户逐节批准）。

## 目标与范围

在 Warm Studio v3 基础上做减法，把管理面板从「精致」推到「昂贵」：
方向为暖纸奢华进化（对标 Linear/Stripe 的克制），不转冷峻科技风。
范围：设计语言 token、壳层侧栏、三个仪表样板间、动效体系；后端零改动，
`contract.spec.ts` 零改动，不新增字体与依赖。

## 已确认决策

| 决策点 | 结论 |
|---|---|
| 方向 | 暖纸奢华进化（A），保留暖纸基调 |
| 痛点 | 排版/材质/数据呈现/动效 四项全动 |
| 样板间 | 监控统计 + 渠道状态 + 代理池 三仪表优先 |
| 交互 | 材质克制 + 数据仪式感 + 弹簧微交互；不动键盘效率层 |
| 红线 | 无硬红线，但对比度表与 testId 实际保留 |
| 侧栏底栏 | A 克制收纳：身份一行 + `…` 菜单收纳操作 |
| 搜索框 | A1 幽灵极简：透明底 + 发丝描边，hover/focus 显形 |
| 图标 | B1 更细更贵：strokeWidth 1.5→1.35、size 16→15、圆端点 |
| 总方案 | 方案 1 Warm Restraint 精炼克制（减法而非换风格） |

## 设计语言 v4（克制增量）

- 字阶：`display` 1.75rem/600/-0.02em 专供 KPI 主数字（tabular-nums）；
  `title` 加字距 +0.01em；`label` 加字距 +0.04em 作眉题。
- 留白：卡片 `p-6→p-7`，组间距 `gap-6→gap-7`。
- 材质：纸纹保持 3% 不加浓；全局描边 `#e8e4dd→#ece8e0` 更淡；
  卡片 hover 显 `border-strong` + 1px 暖琥珀渐变流光（200ms）；
  阴影改双层柔影 `0 1px 2px rgba(28,25,23,.04), 0 8px 24px rgba(28,25,23,.06)`。
- 图表轻量化：无网格仅 0.5px 基准线；线宽 1.25px；面积渐变 18%→8%；
  点半径 3px→2px hover 放大；KPI 全部 tabular-nums。

## 壳层与侧边栏

- 搜索框幽灵化：透明底+发丝描边，hover `bg-hover`，focus `border-strong`
  + ring；⌘K 胶囊无边框化；折叠态 9x9 图标钮。
- UiIcon：默认 strokeWidth 1.35、导航 15px、round 端点；激活不加粗。
- 底栏收纳：头像+管理员/会话有效 一行 + 右侧 `…` 按钮 → UiMenu
  （主题切换/修改密码/退出登录，36px 行高）；折叠态只剩圆形头像居中，
  菜单悬浮 rail 外侧不被裁剪；`p-4` + 与导航间发丝分割线。
- 其余（分组、行高、FLIP 指示器、品牌区、折叠开关）全部保留。

## 三仪表样板间

1. 监控统计：KPI 收敛 6 卡→3 主（请求数/成功率/成本，display 大数字）
   +1 次；趋势图 1.25px 线+8% 面积+生长动效；环宽 14→10px 中心 display；
   热力图格子 6px 圆角、色阶 4 档→3 档、hover 才显数值。
2. 渠道状态：卡片 p-7；徽章 1.25px 描边；时间格改空心描边+成功 40% 填充；
   健康环 3px 细线实色；刷新/频率控件收进卡片右上 `…` 菜单。
3. 代理池：大环下加 `x / y 健康` 辅助行；出口表行高 36→40px、去纵线只留
   发丝横线、hover 极淡高亮；空态插画改细线轮廓。

数据与接口零改动，复用现有 api/usePolling 字段。

## 动效体系

- 弹簧参数集中到 `shared/motion/index.ts`：弹窗/菜单 420/28/0.7；
  抽屉 380/30；列表 stagger index*28ms（上限 6 项）总时长 ≤180ms。
- 数据仪式：KPI count-up 600ms easeOutCubic，仅首载或变更 >5% 触发；
  趋势线 stroke-dasharray 生长 700ms；环 dashoffset 800ms 展开；
  热力图按行 stagger 20ms scale+fade。
- hover：描边加深 150ms + 双层阴影提升 200ms + 发丝高光淡入同步。
- reduced-motion 全部降级 opacity 150ms；count-up 直跳终值。

## 落地与验证

改动面（仅视图层）：theme.css / icons.ts+UiIcon / AppShell（新增 UiMenu.vue）/
UiStat+UiCard / ChartArea+ChartDonut+ChartHeatmap / motion+useCountUp /
StatisticsView / ModelHealthView / ProxyPoolView。不碰路由/API/store/后端/contract.spec。

分批：①地基 token/图标/动效常量 → ②壳层 → ③三仪表 → ④全量回归。

每批验证：web lint/typecheck/test/build 全绿；`python scripts/calc_contrast.py`
88/88；`go build ./...` embed 通过；`scripts/check-web-dist.sh` 无 stale；
320/768/1440+200% 无横向溢出；reduced-motion 无位移。

风险低：纯视觉增量可按文件回滚，最坏退回 Warm Studio v3 的 theme.css+AppShell.vue；
不引入新依赖，构建体积增量 <5kB。

# UI 重构评审报告（2026-08-16）

按设计美学知识库《评审流程》五维评分对本次 UI/交互系统化重构自检。评审对象：`web/` 全部 10 个管理页面 + 共享层，分支 `refactor/ui-consolidation-20260816`（10 个 commit，16c8d3f → 22f36e5）。

## 五维评分

| 维度 | 得分 | 依据 |
|------|------|------|
| 意图清晰度 | 4/5 | 每页单一 PageHeader（eyebrow 分组词 + h1 + 副标题 + 操作区），KPI 区有统一口径行；监控页 range 切换组与页头主信息无争抢。扣 1 分：代理池页头右侧同时出现池状态徽章与三按钮操作区，首屏焦点略分散 |
| 证据强度 | 5/5 | KPI 区带口径/窗口/来源说明；四张趋势图各有 metricLabel·rangeLabel·来源 caption + `<details>` 数据表兜底；成本面板标注"近 30 天·单价为空按 $0 计"。无任何虚构指标 |
| 视觉纪律 | 4/5 | 颜色全部走 token（grep 证实 .vue 内 0 处十六进制）；圆角两档（--radius-control/--radius-panel）全站引用；间距/动效档位 token 落地；遮罩统一 --color-overlay。扣 1 分：eyebrow 从彩色统一为 subtle 后，部分页面分组词与导航的对应关系只靠文字 |
| 状态完整度 | 5/5 | StatePanel 统一加载/空（含引导）/错误（role=alert+重试）三态；操作成功走 toast、表单结果走 inline、轮询失败走 stale 横幅——三通道各司其职；两步破坏性确认保留；错误态全部有恢复路径 |
| 可读性与可达性 | 5/5 | 39/39 文本与控件配对实算登记（`docs/前端对比度配对表.md`）；焦点环全局 :focus-visible；全部表格 caption + th scope；表单 label 关联补齐（单导入输入框）；对话框焦点陷阱 + aria-labelledby 全覆盖；键盘路径有 AppShell.spec 回归 |

**总分 23/25，达到交付线（≥18 且无单维 <3）。**

## 对比度实测

`python scripts/calc_contrast.py`，2026-08-16 实算，39/39 通过。关键行（完整表见 `docs/前端对比度配对表.md`）：

- 正文 `#edf1f2` on canvas/surface/elevated = 17.12 / 16.26 / 15.22:1
- 次级 `#a7b0b8` on 同三底 = 8.85 / 8.41 / 7.87:1
- 强调文字 `#8bd31f` on 同三底 = 10.60 / 10.07 / 9.43:1
- 状态色文字（success/warning/danger/info）on 三底 = 6.26~11.66:1
- 实底徽章前景对 = 7.19~11.73:1；焦点环 on 三底 = 7.46~8.39:1
- 图表数据色（单系列语义色 danger/warning/success/info）= 6.26~11.17:1，全部 ≥3:1

## 验证记录

- 单元测试：vitest **202/202 通过**（30 文件；新增 shared 屄件 22 例 + providers 5 例）
- E2E：Playwright + Go harness **8/8 通过**（全部 data-testid/文本选择器契约保持）
- 构建：`vue-tsc --noEmit && vite build` 通过（2.91s）；eslint 0 error 0 warning
- 响应式实测（`scripts/e2e/responsive-shots.mjs`，Playwright 实测）：4 个代表页 × 320/768/1440px + 200% 缩放抽查，**横向溢出全部 0px**；截图存 `web/test-results/responsive/`（13 张）

## 主要修复清单（对照重构前调研 25 项）

1. 代理池页 metric-card 未定义致四个指标块无样式 → shortcut 落实 + 排版（label + 等宽数值）
2. 双 spinner 实现（8 处 SVG + 2 处 CSS border）→ 共享 LoadingSpinner，全站清零
3. 三轨错误反馈 → StatePanel（inline+重试）/横幅/字段级三通道各归其位
4. formatDate 重复 6+ 处 → `shared/format.ts` 唯一实现（含秒/本地时钟/延迟变体）
5. 三种表格样式 → data-table 契约统一（caption + th scope + 单元格无值 —）
6. AuditView disabled:opacity-40 违规 → 移除（shortcut 契约禁用 opacity 禁用态）
7. 单导入输入框无 label → sr-only label 补齐
8. LoginView h1/h2 双标题 → 卡片内重复标题移除
9. BatchImportDialog 缺 aria-labelledby + 三重关闭 → 补齐 + 收敛至两处
10. 三处手写 setInterval 轮询后台空转 → usePolling（页面隐藏暂停）
11. AppShell 顶栏 route.path 调试信息 → 移除；遮罩两套参数 → 统一 token
12. 44px/40px 触控高度混用 → 按钮统一 min-h-11（shortcut 默认）
13. KeyTable/KeyCards、statusLabel 双份重复 → state.ts 单源 + StatusBadge
14. ModelsView 单价编辑裸文字按钮 → 标准 btn 变体 + 保存中文案
15. ProvidersView 无测试 → 补 5 例 spec

## P0 规则声明

已满足：硬约束 6 条全部通过（无框架默认色——NVIDIA 绿品牌色 + 降饱和 info；无虚构数字；无买话文案；无 emoji 图标；异步六态覆盖；无负字距）。

**未做到/未验证项（如实登记）：**

1. **KPI 口径行做区域级而非每卡级**——指标面板正例是每卡带口径四行；本页 10 张卡重复同一窗口属噪音，改为区域单行 + Token 卡拆输入/输出。偏离理由：信息不重复原则优先，口径信息完整可追溯。
2. **图表 hover tooltip 未实现**——趋势图为静态 SVG，数值第二出口靠底部 `<details>` 数据表（P0#10 允许：tooltip 非唯一出口即合规）。增强项留待后续。
3. **真实浏览器视觉走查未完成**——图像分析服务本次限流，视觉证据以 13 张截图 + 0px 溢出实测代替；建议人工过目 `web/test-results/responsive/`。
4. **kebab-case attr 在测试运行时不解析为 prop 的根因未定位**——已实证并绕过（camelCase 传递 + eslint ignore 白名单），注释登记于 `web/eslint.config.js`。

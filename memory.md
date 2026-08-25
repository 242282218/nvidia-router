# 记忆 — 可复用约束、部署方法与排障结论

> 本文件只保留长期有效信息。不得记录密钥、完整 URL 凭据、Cookie、Access Key、日志原文、临时数据或普通测试流水账。

## 1. 项目边界与安全

- 星空代理真实联调、部署、重启和线上检查只在国内目标 hangzhou2-2 执行；国外机器不用于星空代理。
- 处理服务器前依次读取：项目 AGENTS.md、服务器管理/AGENTS.md、目标目录 AGENTS.md 与 memory.md、部署脚本、Compose 和部署说明。
- 单体路由器内置 XApi 采集、验证、池管理和 CONNECT；池未就绪时必须失败，不得静默直连。
- XApi 完整地址、provider 凭据、主密钥、管理员密码、NVIDIA Key 和 SSH 私钥只通过运行时 Secret 注入；命令输出、日志、Git、文档和记忆中只允许出现脱敏值。
- 当前公网入口为 HTTP；管理员密码、Cookie、Access Key、请求和响应存在明文传输风险。生产 HTTPS 需要受信反向代理、Secure Cookie、External Origin 和 Trusted Proxy CIDR。

## 2. 镜像与发布版本

- 版本格式：YYYYMMDD-变更主题-git短SHA；每次发布必须唯一，禁止生产使用 latest、local、dev 或其他漂移标签。
- 同一版本号必须同时对应 Git HEAD、Release 目录、镜像和回滚记录：
  - /opt/nvidia-router-releases/<version>
  - nvidia-router:deploy-<version>
- 标准发布命令：python scripts/deploy/deploy_remote.py <version>。脚本使用 git archive HEAD，继承现网 .env 与 deploy override，使用 goproxy.cn 构建，切换前由旧镜像备份数据库。
- 同一 Release 已存在备份时不得覆盖；源码或配置变化必须生成新版本号。部署后核对容器实际镜像、工作目录、Git SHA、备份路径和回滚版本。
- 版本、备份、回滚点和未完成验证项要更新到本节“当前线上状态”；详细历史放在目标服务器记忆或专项文档。

## 3. 架构与关键配置

- 监听：应用容器 0.0.0.0:3756；默认 Compose 绑定回环，公网部署 override 绑定 0.0.0.0:3756。
- XApi：当前国内套餐 qty=2；qty>2 曾返回 506，NVIDIA_ROUTER_XK_EXPECTED_QTY 必须先与实际套餐确认，不能凭旧记录调高。
- 常用默认：采集间隔 5s、代理 TTL 120s、验证期望状态 404、验证并发 2；慢推理模型客户端首字节超时建议至少 120s，流式 idle 超时建议 180s。
- 数据卷：生产使用外部 Docker volume nvr-data；不得执行 docker compose down -v。
- SQLite 迁移：新增或升级索引必须核对旧索引名；CREATE INDEX IF NOT EXISTS 同名时不会重建索引。

## 4. 标准部署流程

### 发布前

~~~powershell
Set-Location 'D:\PROJECT_ZZZZZZZZZ\服务器管理\hangzhou2-2'
ssh -F .\ssh_config_local hangzhou2-2
~~~

- 先检查远端 app、端口、数据库卷、健康接口和近期错误，再开始构建。
- 本地确认工作树和目标提交：git status --short、git diff --check、git rev-parse --short HEAD。
- 有新增迁移时，必须确认旧镜像备份成功后再切换；不要把 .env、key/、data/、依赖或本地二进制放进发布包。

### 切换

~~~powershell
python scripts/deploy/deploy_remote.py 20260823-redeploy-cfcaecf
~~~

- 脚本流程：读取现网 release/image → 创建唯一 Release → git archive HEAD 上传 → 继承 .env/deploy override → 构建版本镜像 → 停 app → 旧镜像备份 nvr-data → 启动新 app → live/ready 校验。
- Compose 生产覆盖文件通过 NVIDIA_ROUTER_IMAGE 注入版本镜像；不要手工改回 nvidia-router:local，不要使用 --remove-orphans 删除预期存在的容器。

### 回滚

- 使用带版本号的旧 Release、旧镜像和同一组基础 Compose + deploy override；不得依赖默认镜像标签。
- 回滚前确认数据库迁移兼容性；数据库恢复只能使用明确的备份文件，且备份权限保持 600。
- 管理员密码重置必须先停 app 释放 SQLite 锁，密码仅通过 stdin 注入；不得写入 argv、文件、日志或记忆。

## 5. 最小充分验证

### 本地

~~~bash
go vet ./...
go test ./...
pnpm --dir web run typecheck
pnpm --dir web run test
pnpm --dir web run build
git diff --check
~~~

前端改动必须同步 internal/web/dist（go:embed），并运行 scripts/check-web-dist.sh；Go race 在无 CGO/GCC 的本机无法验证时，交给 CI。

### 远端免认证

- 容器：running/healthy、重启次数为 0、OOM 为 false。
- 接口：/health/live、/health/ready、代理池和 OpenCodeFree 健康端点返回 200。
- 端口：3756 以及本次涉及的 18080、18081、6020 监听正常。
- 根页及其 JS/CSS 资源返回 200；匿名 /v1/models、/metrics 应返回 401。
- 读取 schema_migrations 最大版本、enabled 模型数量和部署后错误签名；不输出数据库内容或日志原文。

### 真实联调

- 只在 hangzhou2-2，通过运行时 Secret 执行 scripts/test/live-nvidia.sh、live-xk-proxy.sh 或专项探针。
- 重启后先等待代理池预热，再判断 NVIDIA 渠道；优先看鉴权 metrics 中的池健康 gauge。
- 逐 case PASS 才能宣称通过；SKIP、BLOCKED、缺凭据或仅健康检查都不能宣称完整 live/E2E。

## 6. 高频排障结论

- 池健康数高但 latency_samples 为 0：优先检查 validator 和请求 transport 是否启用 ForceAttemptHTTP2；Grace 上限必须锚定 ValidatedAt，不能锚定当前时间，也不能续命从未验证的出口。
- validation_all_failed：先看池 gauge 和实际请求，不要只依赖 INFO 日志；XApi TXT 随机性需要在 fetch 内有界重试，最终失败必须触发退避。
- 代理快速 502：ReasonTransportFailed 必须映射为可重试的全局上游故障，让请求换 Key；共享租约并发上限可能造成瞬时无健康出口。
- OpenCodeFree 638/502：内网 gateway 只走直连，不要经外部 XApi；非 2xx、空响应和协议错误必须映射为明确的上游错误，不能回退成泛化 500。
- 大量 499：通常是客户端 60 秒超时与 NVIDIA 慢首字节冲突；监控中将 canceled 单列，不要直接归因于路由器故障。
- 管理 API 401/403：先确认运行时密码是否为当前值；变更请求需要匹配 Host 的 Origin。管理员登录页面验证应等待 URL 离开 /admin/login。
- 前后端契约漂移：后端迁移、DTO 或结构体加字段时，同步前端类型、contract spec 和 embed 产物；模型能力或 context_length 由运营数据维护时不要在候选发现中编造默认值。

## 7. Windows 与发布工具坑

- Windows 无 sshpass/plink；使用目标目录的 SSH 配置或部署脚本内 Paramiko 配置。
- PowerShell 管道传 gzip/归档可能破坏二进制；优先用 git archive + SFTP。
- PowerShell 读写中文文件可能改变编码；批量替换使用 apply_patch 或明确 UTF-8 的工具。
- 本地 3756 可能被旧 nvidia-router.exe 占用；运行 E2E/视觉探针前确认实际端口和嵌入资源版本。
- 一次性诊断脚本放临时目录，含 Secret 时使用 umask 077/权限 600，验证后清理。

## 8. 当前线上状态（最后核验：2026-08-23）

- 源码：main@cfcaecf。
- Release：/opt/nvidia-router-releases/20260823-redeploy-cfcaecf。
- 镜像：nvidia-router:deploy-20260823-redeploy-cfcaecf。
- 回滚点：20260823-ui-polish-worktree / nvidia-router:deploy-20260823-ui-polish-worktree。
- 切换前数据库备份：/opt/nvidia-router-releases/20260823-redeploy-cfcaecf/backups/predeploy-20260823-redeploy-cfcaecf/router.db，权限 600。
- 已核验：app healthy、重启 0、OOM false；schema 42；enabled 模型 10 个；live/ready、关键健康端点、根页和新静态资源正常；匿名业务与 metrics 鉴权正常；部署后错误签名为 0。
- 最近一次确认性重部署未执行管理员会话、真实模型、代理轮换或 CONNECT 矩阵；不要把上述免认证结果当作完整 live/E2E。

## 9. 价格功能移除（2026-08-23）

- 价格字段、模型单价编辑、成本统计接口和成本面板已从运行时代码移除；迁移 043 负责从现有 `models` 表删除遗留价格列。历史迁移 019/027/029/031 保持不变，以免破坏已应用迁移的 checksum 和升级链。
- 前端构建配置直接把 Vite 输出写入 `internal/web/dist`；Windows 下 pnpm 非交互安装可能被 `ERR_PNPM_IGNORED_BUILDS` 拦截，可直接调用 `web/node_modules/.bin/vue-tsc.cmd`、`vitest.cmd` 和 `vite.cmd` 完成本地校验。`scripts/check-web-dist.sh` 需要可用的 POSIX bash。

## 10. 渠道状态页卡片重设计（2026-08-23）

- 渠道状态卡片应将成功率、探测次数、连续失败和最近延迟作为首屏固定信息；时间段只保留紧凑时间线和单段标题提示，不再用可展开的长详情列表，避免卡片高度随交互跳变。
- 这类前端改动的最小验证组合：`web/node_modules/.bin/eslint.cmd`、`vitest.cmd run`、`vue-tsc.cmd --noEmit`、`vite.cmd build`，再用 Playwright 在桌面三列和 390px 单列检查无展开入口、无横向溢出；构建后同步检查 `internal/web/dist` 的静态资源引用闭包。

## 11. 本地统一启动（2026-08-23）

- 本地唯一启动入口为 `start.bat`，它只包装 `scripts/start-local.ps1`；脚本从当前源码启动 `go run ./cmd/nvidia-router serve`，再启动 `web/node_modules/.bin/vite.cmd --host 127.0.0.1 --strictPort`。
- 前端开发地址固定为 `http://127.0.0.1:5173`，Vite 的 `/admin/api` 请求代理到 `3756`；`3756` 在本地开发中只作为 API 服务，不作为前端页面入口。
- `internal/web/dist` 是 Docker/生产 Go 内嵌前端产物，不能作为本地实时开发入口删除或替代 Vite。
- 启动器在 `tmp/local-start-state.json` 记录自己拉起的进程及启动时间；无登记的端口占用会安全失败，不得按通用 `node`/`go` 进程名误杀其他服务。启动失败时要清理本次已拉起的进程。
- 本地 `.env` 的主密钥必须与现有 `data` 匹配；不匹配会触发 AES-GCM authentication failure。密钥只通过本机运行时环境注入，不能写入启动脚本、日志或记忆；不得删除或重建 `data`。
- 启动自检：`powershell.exe -NoProfile -ExecutionPolicy Bypass -File scripts/test/start-local_test.ps1`；它检查 `3756/health/live=200`、`5173/=200`、`5173/@vite/client=200` 和 `/admin/api/models=401` 代理边界，前端改动只在 `5173` 页面验证 HMR。

## 12. Flat Outline 遮罩与响应式浮层（2026-08-23）

- 前端浮层验收应覆盖 1440px 与 390px：基础页、移动侧栏、命令面板、快捷键帮助、Modal、菜单和 tooltip 均检查 `scrollWidth - clientWidth`、可见浮层矩形是否在视口内、活动焦点中心是否仍可命中；完整页面检查用 Playwright，认证只从本地运行时配置读取，不回显凭据。
- UnoCSS 下移动抽屉的显示/隐藏位移类必须互斥；基础类同时保留 `-translate-x-full`、再动态追加 `translate-x-0` 可能因生成顺序继续应用隐藏变换。使用条件类只输出一个移动位移状态，并保留 `lg:translate-x-0` 桌面覆盖。
- 单列 CSS Grid 的卡片子项默认 `min-width:auto`，内部 flex 标题与固定操作按钮可能把轨道撑出视口；卡片 grid item 与可收缩的标题内容容器都应按需加 `min-w-0`，不能只依赖 `body { overflow-x: hidden }`。
- Flat Outline 视觉回归同时做源码扫描和浏览器计算样式检查：生产源码不得出现阴影、文字阴影、drop-shadow、backdrop-filter、backdrop-blur、mask-image、animate-ping 或 shadow 工具类；功能性半透明 scrim 保留，面板层级使用底色与描边。
- Windows 本机没有可用 POSIX bash 时，`scripts/check-web-dist.sh` 用等价 Python 闭包检查：从 `internal/web/dist/index.html` 递归跟踪静态引用与指纹文件名，确认无缺失资源和孤立旧 hash；随后用 `web/node_modules/.bin/vite.cmd build` 重建嵌入产物。

## 13. Flat Outline 交互与移动布局补充（2026-08-23）

- 移动抽屉打开时，后渲染的 `aside` 若与顶部 Header 同为 `z-40`，会拦截菜单关闭按钮；打开态 Header 应提升到 `z-50`，并用真实点击回归验证关闭路径。
- 移动端 Playwright 几何检查要等待抽屉 300ms 位移动画完成；登录跳转要等待明确的 `/admin/`，不能用会匹配 `/admin/login` 的宽泛 glob。
- 代理池配置的全宽 Grid 子项若包含长说明和输入框，必须加 `min-w-0`；数据表自身可以保留在 `overflow-auto` 容器内，但不能依赖 `body { overflow-x: hidden }` 掩盖父级 Grid 外溢。
- 本地统一启动器清理应以 `tmp/local-start-state.json` 的进程路径、启动时间和父 PID 做精确核验；子进程退出可能连带启动器消失，确认 3756/5173 已无监听后再清理状态和本轮日志。

## 14. 全站视觉 QA 环境与基线（2026-08-24）

### 隔离 QA 环境（不碰本地 data 与 3756/5173）

- `node scripts/test/web_qa_env.mjs [--port 5175] [--no-seed]`：编译并拉起 `tests/e2e/harness`（临时目录 SQLite + mock NVIDIA 上游），播种数据，再起一个独立 Vite，把 `/admin/api` 代理到 harness；连接信息写 `tmp/web-qa-env.json`。用它而不是直接对本地实例做 QA：本地库的管理员口令存在库里且可能已改，隔离库既能稳定播种又不会写坏数据。
- `node scripts/test/web_visual_qa.mjs [--out dir] [--only substr]`：13 个页面 × 4 视口（390/768/**1024**/1440）× 双主题 × 6 种浮层态截图 + 几何审计。1024 是 lg 断点第一格，侧栏刚占 240px 而正文比 768 更窄，是全站最容易溢出的宽度，必须单独测。
- `Vite` 的代理目标由 `VITE_PROXY_ORIGIN` 决定，可指向任意后端；spawn Vite 用 `node node_modules/vite/bin/vite.js` 而非 `.bin/vite.cmd`，避免 shell 拼参与进程树回收不确定。
- 管理端登录限流是**每 IP+用户名 1 分钟 5 次**，多视口各自登录必然被拒；探针必须登录一次后用 `context.addCookies` 复用会话。

### 播种契约（易踩）

- `POST /admin/api/auth/change-password` 只收 `current_password`/`new_password`（多传字段会 400），且会换发 cookie，jar 必须跟着更新。所有写请求都要带与 baseURL 完全一致的 `Origin`。
- `POST /admin/api/nvidia-keys/batch` 的 `keys` 是**换行分隔字符串**，不是数组。
- `POST /admin/api/models` 不校验候选是否来自上游发现，可离线批量播种；但 `selectionDTO` 不含 `context_length`/`stream_*`（只能后续 PATCH），且 `kind=asr/tts` 直接 `enabled:true` 会被能力校验拒绝。
- `PATCH /admin/api/proxy-pool` 的 `upstream_url` 必须带 provider 查询凭据，QA 播种应留空而不是伪造凭据串。
- 请求日志只有请求热路径一个写入口，且 `BufferRecorder` 默认 **30s** 才 flush；打完流量要等够时间，否则监控/统计页是空的。

### 几何审计的假阳性规则（缺了会被噪声埋掉真缺陷）

- 判定必须**祖先感知**：滚动容器内的宽内容、收起抽屉（`[inert]` 或整体位于画布外）的后代、`.sr-only` 子树都要排除。
- `overflow:visible` 的"撑破"不丢内容，属于越界范畴；只报真正裁切的容器，且带 `text-overflow:ellipsis` + `nowrap` 的单行截断是设计意图。
- 命中测试要跳过铺满视口的 scrim，以及被打开的 `[role=menu/dialog/tooltip/listbox]` 浮层正常覆盖的底层控件。
- 触控尺寸按最近的 `<label>` 命中区算，而不是 16px 的 `input` 本体。

### 本轮定位到的系统性根因（都已修，勿回退）

- **项目没有任何 CSS reset**，UA 默认值曾在每页泄漏：`<a>` 全站带下划线；169 个 `<p>` 带 13–14px 非网格外边距（`mt-*` 只覆盖 top，bottom 全残留）；`<ul>/<ol>` 带 disc 与 40px 内缩，让状态行出现"圆点 + 状态点"双点；`<dd>` 有 40px 缩进；表单控件不继承字体回落 **Arial**。reset 现集中在 `web/src/styles/theme.css`，改动前先确认不是在重新引入这些。
- **UnoCSS 的 `font-[...]` 编译成 `font-family`**，而 `--text-display/title/heading/label` 是 `font` **简写**值，赋给 font-family 是无效声明会被整条丢弃——四层字体阶梯从未生效，标题一路回落到 UA（h1 30px/700、h2 22.5px/700、眉题 15px）。现改为 `uno.config.ts` 的 rule 显式输出 `font` 简写；`type-*` 不要再与 `text-base` 等字号工具类混用（简写会重置字号）。
- 表格默认 `border-spacing: 2px`，9 列表格凭空多 20px 足以逼出横向滚动条；`data-table` 已加 `border-collapse`，同时让 1px 行分隔连成整线。
- `table-layout: auto` 下"不能换行"的列先吃满 max-content，可换行的模型列只分到剩渣（实测 115px，长模型名折 8 行），声明宽度被整体忽略。密集表用 `table-fixed` + 显式列宽 + `min-w`，宽度分配才可控；给单元格加 `whitespace-nowrap` 会让该列变刚性并饿死邻列，是有代价的。
- `ring-*` 工具类编译成 box-shadow，与 Flat Outline 冲突，且会和 `theme.css` 的 `:focus-visible` outline 叠成多层焦点指示。焦点统一只用 outline。
- 颜色只允许来自 theme.css 语义 token：`dark:` 变体走 `.dark` 选择器，与本项目的 `[data-theme='dark']` 永不相交，写了就是死代码（组件会停在亮色配色上）。
- 以上三条已固化为回归守卫：`web/src/styles/flat-visual.spec.ts` 断言无 `ring-\d`、无 `dark:` 变体、无原始调色板色阶。

### 最小验证组合

~~~bash
web/node_modules/.bin/eslint.cmd .
web/node_modules/.bin/vue-tsc.cmd --noEmit
web/node_modules/.bin/vitest.cmd run
web/node_modules/.bin/vite.cmd build
python scripts/test/check_web_dist_closure.py   # dist 静态资源闭包（无 POSIX bash 时替代 check-web-dist.sh）
~~~

## 15. 2026-08-24 main 提交与确认性重新部署（9f45568）

- 发布时工作树 `HEAD`、本地 `main` 和 GitHub `origin/main` 均为 `9f45568`；执行 `git push origin HEAD:refs/heads/main` 返回 `Everything up-to-date`。发布前工作树保持干净。
- 本地门禁已通过：`go test ./...`、`go vet ./...`、前端 lint、typecheck、284 个前端单测、前端生产构建、`git diff --check`；Windows 使用 `python -X utf8 scripts/test/check_web_dist_closure.py`，结果为引用 40、磁盘 40、缺失 0、孤立 0。
- 使用标准 `python scripts/deploy/deploy_remote.py 20260824-redeploy-9f45568` 发布到 `/opt/nvidia-router-releases/20260824-redeploy-9f45568`，镜像为 `nvidia-router:deploy-20260824-redeploy-9f45568`；继承线上 `.env` 和 deploy override，复用外部 `nvr-data`，切换前由旧镜像生成数据库备份。
- 切换前备份为 `/opt/nvidia-router-releases/20260824-redeploy-9f45568/backups/predeploy-20260824-redeploy-9f45568/router.db`，大小 8,806,400 字节，权限 `600`，属主 `10001:10001`，SHA-256 为 `4b40da6a7197ebd86f16b5bc07a99190086335401a586be2852291e61ee1d885`；回滚点为 `20260823-remove-pricing-59abdc9` / `nvidia-router:deploy-20260823-remove-pricing-59abdc9`。
- 部署后 app 使用新镜像，`running/healthy`、重启 0、OOM false；`/health/live`、`/health/ready`、代理池 `18080/healthz`、OpenCodeFree `6020/healthz` 均 200；公网 3756 `/health/live`、根页和当前 JS/CSS 资源均 200；匿名 `/v1/models` 与 `/metrics` 均 401。
- 管理烟测通过：管理员登录 200，鉴权 `/metrics`、模型、设置和 Access Key 列表均 200，模型总数 11、启用 10，注销 204。未执行真实模型请求、代理轮换或 CONNECT 矩阵；部署后代理验证日志仍出现有限的 `validation_all_failed` 预热告警，因此不能将本轮结果宣称为完整 live/E2E。公网 HTTP 明文风险保持不变。

## 16. 2026-08-24 vibe 场景多维复测

- 使用 `scripts/test/vibe_eval_remote.py` 和一次性补充探针，在 `hangzhou2-2` 的当前 release 上完成模型思考强度、thinking 开关、工具、流式、长输入、context needle、JSON、长输出和重复稳定性测试；未修改源码、运行配置或模型白名单。
- 模型目录共 11 个，其中 10 个启用 Chat 模型；10 个基础请求中 7 个有效 HTTP 200，3 个 NVIDIA 模型为 `502 upstream_proxy_unavailable`：`deepseek-ai/deepseek-v4-flash-0731`、`meta/llama-3.2-90b-vision-instruct`、`minimaxai/minimax-m3`。
- OpenCodeFree 深测覆盖 `opencode-free/nemotron-3-ultra-free` 与 `opencodefree/x-preview-f-free`，思考、工具、长输入主路径大部分为 200；Nemotron 完成流式样本无 malformed event，x-preview 出现一次流式 503。
- 综合矩阵耗时约 127 秒，选中 `nvidia/nemotron-3-ultra-550b-a55b`、`stepfun-ai/step-3.7-flash`、`opencodefree/x-preview-f-free`、`opencode-free/hy3-free`；x-preview 完成双工具调用和 follow-up，另外三个选中模型的工具用例观察到 501 `not_implemented`。工具能力必须按模型判断。
- 临时测试 Key 已删除，清理后管理注销 204、metrics 200、健康代理 27；最终容器为 `running/healthy`、重启 0、OOM false，live/ready、18080/healthz、6020/healthz 均 200。评测报告见 `docs/plans/2026-08-24-vibe场景多维复测报告.md`。
- 限制：5 次重复不是长压测；context needle/长输入不等于已证明 32K/128K 上限；501/502/503 分别按能力未实现、代理出口不可用、上游瞬态失败区分，不能合并为单一故障。

## 17. 2026-08-24 vibe 优化提交与重新部署

- GitHub `main` 已推送到 `b9704f3`（`feat: tighten vibe model capability validation`）；发布前 worktree 干净，`go test ./...` 通过。
- 标准发布版本为 `20260824-vibe-optimization-b9704f3`，release 为 `/opt/nvidia-router-releases/20260824-vibe-optimization-b9704f3`，镜像为 `nvidia-router:deploy-20260824-vibe-optimization-b9704f3`。
- 切换前数据库备份位于 `backups/predeploy-20260824-vibe-optimization-b9704f3/router.db`，大小 9,023,488 字节，权限 `600`，SHA-256 为 `47336e880e4a290b4c2d9d85b1eb8d0b27aa68619bf23f0429eaeface52769c6`；回滚点为 `20260824-redeploy-9f45568` / `nvidia-router:deploy-20260824-redeploy-9f45568`。
- 发布后容器 `running/healthy`、重启 0、OOM false；3756 live/ready、18080/6020 healthz、根页均 200；匿名 `/v1/models` 与 `/metrics` 均 401；3756/18080/18081/6020 端口监听正常；release 含 migration 044。
- 本轮没有重复执行管理员会话、真实模型、代理轮换或 CONNECT 矩阵；不据此宣称完整 live/E2E，公网 HTTP 明文风险保持不变。

## 18. 2026-08-24 前端整体美学一致性收敛

- 视觉一致性守卫集中在 `web/src/styles/flat-visual.spec.ts` 与 `shortcuts.spec.ts`：组件源码不得直接写状态 hex、引用未定义 surface token、使用未命名的宏观圆角或把运行时值插入 UnoCSS 任意颜色 class；数据热力格、时间线刻度和状态点等数据形状可保留局部圆角。
- `ModelHealthCard` 的动态 `border-[color-mix(...${token}...)]` 与 `bg-[...${token}]` 会让 Vite/esbuild 产生 CSS 语法警告；状态边框改为静态 token class 映射，时间线颜色改为原生 `:style` 的 `backgroundColor`，不要回退到运行时拼 UnoCSS class。
- 全站视觉 QA 必须先启动隔离 `node scripts/test/web_qa_env.mjs --port 5175`，等待 `tmp/web-qa-env.json` 出现后再运行 `node scripts/test/web_visual_qa.mjs http://127.0.0.1:5175 --out <dir>`；环境未就绪时探针会误连本地实例并以错误登录失败，不能据此判断页面代码。

## 19. 2026-08-24 前端一致性发布与重新部署

- 运行时代码发布基线为 GitHub `main` 的 `08eb1c9`；最终发布版本为 `20260824-ui-consistency-08eb1c9`，release 为 `/opt/nvidia-router-releases/20260824-ui-consistency-08eb1c9`，镜像为 `nvidia-router:deploy-20260824-ui-consistency-08eb1c9`。随后仅追加本记录的文档提交已推送到 `main`，不改变运行时代码。
- 切换前数据库备份位于 `backups/predeploy-20260824-ui-consistency-08eb1c9/router.db`，大小 9,445,376 字节，权限 `600`，属主 `10001:10001`，SHA-256 为 `95816c45052bd783995e09be7ab9c827cfdd3cac226f24807c456be8e3695b4d`；回滚点为 `20260824-vibe-optimization-b9704f3` / `nvidia-router:deploy-20260824-vibe-optimization-b9704f3`。
- 发布后容器实际为目标镜像，`running/healthy`、重启 0、OOM false；release 工作目录与版本一致，迁移 `044_model_tools_status.sql` 存在；3756/18080/18081/6020 端口监听正常，3756 live/ready、18080/6020 healthz 均 200，根页和 index 引用的 2 个静态资源均 200。
- 匿名 `/v1/models` 与 `/metrics` 均 401；管理员登录 200，会话、模型列表、运行时摘要均 200，注销 204，注销后的会话为 401。未执行真实模型请求、代理轮换或 CONNECT 矩阵；公网 HTTP 明文风险保持不变。
- 远端 loopback 静态探针应使用禁用代理的 opener，并将正则提取的 bytes 路径先 decode 成字符串；否则 Python 探针会把类型错误吞成资源失败，而 `curl` 与实际服务均正常。

## 20. 2026-08-24 Teleport 测试隔离

- 共享浮层组件真实 Teleport 到 `body` 后，业务视图单测中不要继续用 `wrapper.get/find` 查询菜单、Modal、确认按钮或 tooltip；用 `document.body.querySelector` 查询，并用原生 `click`/`change` 事件驱动交互，保留对真实 DOM 拓扑的覆盖。
- 每个会打开浮层的测试套件在 `afterEach` 清理 `document.body`，避免未销毁的 Teleport 节点让后续用例命中旧菜单或旧弹窗；使用全局 `config.global.stubs` 的旧测试必须在 `afterEach` 恢复为空，不能污染其他套件。
- `v-model.lazy` 的 Teleport 输入控件测试要设置 `HTMLInputElement.value` 后派发冒泡 `change`，仅派发 `input` 不会触发保存逻辑。

## 21. 2026-08-24 Teleport 菜单无障碍与层级

- `UiMenu` Teleport 到 `body` 后，打开时必须把焦点移入首个可操作项；菜单内使用 `ArrowUp/ArrowDown/Home/End` 移动焦点，关闭时再归还触发按钮。仅依赖 DOM 顺序会让键盘焦点跳过 body 末尾的 Teleport 节点。
- Overlay token 的数值顺序必须与语义一致：popover 低于 modal，modal 低于 toast，tooltip 位于最上层；共享 shortcut 不得用 `z-50` 这类硬编码覆盖 token。
- 当前机器默认 Vitest 文件并行时，路由/命令面板/AppShell 的 5 秒用例可能被 worker 竞争拖超时；遇到无失败堆栈但有超时，使用 `vitest run --maxWorkers=1 --no-file-parallelism` 做确定性全量复核，再结合定向并行测试判断是否为环境抖动。

## 22. 2026-08-25 UI 浮层视觉重构提交与重新部署（772fce2）

- 部署前 GitHub `main` 与源码 `HEAD` 均为 `772fce2`（`feat: refine frontend overlays and visual tokens`）；本次只包含前端浮层、焦点无障碍、层级 token、回归测试和嵌入式资源更新，无数据库迁移。
- 标准版本为 `20260825-ui-overlays-772fce2`，release 为 `/opt/nvidia-router-releases/20260825-ui-overlays-772fce2`，镜像为 `nvidia-router:deploy-20260825-ui-overlays-772fce2`。
- 回滚点为 `/opt/nvidia-router-releases/20260824-ui-consistency-08eb1c9` / `nvidia-router:deploy-20260824-ui-consistency-08eb1c9`；切换前备份为 `/opt/nvidia-router-releases/20260825-ui-overlays-772fce2/backups/predeploy-20260825-ui-overlays-772fce2/router.db`，大小 9,494,528 字节，权限 `600`，属主 `10001:10001`，SHA-256 为 `9d363345872d8c101863832178d0068638db52a83d713a2b31b68d4fd022f495`。
- 发布后 app 使用目标镜像，`running/healthy`、重启 0、OOM false；3756 live/ready、代理池 `18080/healthz`、OpenCodeFree `6020/healthz`、根页和公网 live 均 200；新静态资源 `/assets/index-CMOvBKXD.js`、`/assets/index-D5M_XNC2.css` 均 200；匿名 `/v1/models` 与 `/metrics` 均 401；3756/18080/18081/6020 监听正常；部署后错误签名计数为 0。
- 管理员烟测通过：登录 200，会话访问 models/settings/runtime summary/access-keys 均 200，注销 204。未执行真实模型请求、代理轮换或 CONNECT 矩阵；公网 HTTP 明文风险保持不变。

## 18. 2026-08-25 模型能力评测与修复

- 新增可复用探针 `scripts/test/capability_eval_remote.py`（经 `remote_exec.py --stdin-env` 注入密码，在 hangzhou2-2 机内跑），判分全部程序化：沙箱执行生成代码、约束正则、needle 回取；本地 Windows 无 spawn 沙箱时走 `_sandbox_inline` 回退。
- 管理员密码：环境变量 `NVIDIA_ROUTER_ADMIN_PASSWORD` 已失效（401）；当前有效值在项目根 `.env` 的 `NVIDIA_ROUTER_INITIAL_ADMIN_PASSWORD`。
- 工具 501 根因是路由器门控（`tools_status != supported` 即拒，internal/modelcatalog/capabilities.go:179）。生产 detailed 探测入口：`POST /admin/api/model-test-jobs {"model_ids":[...],"mode":"concurrent"}`，会自动写回 reasoning/tools 状态。实测 llama tools=unsupported（真实不支持）；nemotron-free 与 x-preview 反复探测均 unknown——`service_probe.go` 的 `marshalProbeToolsBody` 用 `tool_choice=required`，这两个网关模型疑似只响应 auto+强提示，属探针局限，不要据此断言模型不支持工具。
- 元数据修复：llama(id=35) PATCH `reasoning_zero_allowed=true` 后 `reasoning_effort=none` 从 501 变 200（levels=["none"] 且 zero_allowed=false 时 nearestLevel 必失败）。
- 探针假阴性已修：reasoning 模型在小 max_tokens 下会吃光预算产出空回复——stability 32→512、json_mode 256→1024、tools_parallel 512→1024；多函数代码块需执行式函数选择（同名 arity 的 helper 会遮蔽目标函数）。
- 第二轮复测要点：kimi-k3 恢复可用且 needle/IF/推理/补测代码全对（偶发 502）；hy3 稳定性 3/3 exact OK；nemotron json_mode 仍输出超长非精确 JSON（真实弱点）。

## 19. 2026-08-25 设计令牌提交与重新部署（ccc725d）

- GitHub `main` 已推送至 `ccc725d`（`feat: refine design tokens and add capability eval probe`）：前端五层字体刻度（新增 caption/metric/mono）、间距半步与 data 圆角 token、panel-inset 描边、模型表截断提示、侧栏排版修正；含能力评测探针、2026-08-24 复测报告与设计文档；发布前门禁全过（go vet/test、typecheck、291 前端单测、dist 闭包 41/41）。
- 标准版本 `20260825-design-tokens-ccc725d`，release `/opt/nvidia-router-releases/20260825-design-tokens-ccc725d`，镜像 `nvidia-router:deploy-20260825-design-tokens-ccc725d`；切换前备份 `backups/predeploy-20260825-design-tokens-ccc725d/router.db`（10,059,776 字节，600）；回滚点 `20260825-ui-overlays-772fce2`。
- 首次构建失败：镜像加速器瞬时无法解析 `golang:1.24.0-bookworm` 元数据；远端直接 pull 精确 tag 后重试即成功。教训：加速器抖动先补拉精确 tag 重试，不改 Dockerfile、不换版本号。
- 发布后验证：app `running/healthy`、重启 0、OOM false；live/ready、18080/6020 healthz、根页与新资源 `index-CweVNl6N.js`/`index-CIPy-lLW.css`、公网 live 均 200；匿名 `/v1/models` 与 `/metrics` 401；schema 44、enabled 模型 10；近 5 分钟错误签名 0。管理烟测：登录 200、受保护 API 全 200、注销 204。

## 20. 2026-08-25 P0-P3 全量优化提交（5da18ad）

- 提交 `5da18ad`（`fix: resolve tool-gate deadlock, reasoning starvation, and proxy exit stickiness`），基于 2026-08-25 能力评测与四份测试报告的根因修复：
  - **工具门控死锁（P0）**：`capabilities.go` validateRequirements 放行 `tools_status=inferred`（运营/能力注册表显式声明；unsupported 与 unknown 仍拒绝）；探针改两段式（required → auto+强提示，tools 探针 max_tokens 16→256），unsupported 需两种形态都明确阴性。kimi-k3/minimax-m3/nemotron-free/x-preview 恢复 Agent 可用。
  - **小预算思考饥饿（P0）**：`AutoReasoningSpec(profile, outputLimit)` 注入阶梯——≤128 token 注入 none（无 none 可表达则跳过注入，绝不落入正档位）、129–511 最便宜正档位、≥512 维持 medium 上限；`capThinkingBudget` 增加绝对保留 `limit−32`（limit≤32 不封顶）。两个协议包均接线，Responses 的 max_output_tokens 已在 Parse 时改名所以直接读 chat["max_tokens"]。
  - **proxy_rejected 出口粘滞（P1）**：CONNECT 被拒的出口现在 `Retire(RetireReasonProxyRejected)` 计入池失败计数并丢弃缓存 transport，消除同会话反复拨同一坏出口导致的成簇快速 502；router 层短路语义保持不变。
  - **启动期 profile 一致性检查（P1）**：`CountUnexpressibleReasoningProfiles` 启动时告警 llama 形态（levels=[none]+zero_allowed=false）模型。
  - **OCF 流式瞬态重试（P2）**：首字节未写出前允许一次 500ms 重试（firstWriteTracker 跟踪）；owned_by 按 provider 区分；capability 501 写入专用 error_code。
  - **常态化探测（P3）**：迁移 045 加 `capability_probe_enabled`（默认关）+ `capability_probe_interval_hours`（默认 24），CapabilityProbeRunner 串行跑 detailed probe 自动回写。
- 测试要点：探针 fake 用 `chatResponses []string` 按调用重放；runner 测试必须给 discoverer 设 `chatResponse` 兜底否则第二个 chat 模型的 base+reasoning 探针会因空 choices 失败导致调用数不符；runner 已改为纯串行（无并发），测试确定性依赖 List 的 public_id 排序。

## 21. 2026-08-25 P0-P3 确认性重新部署（227061b）

- GitHub `main` 与部署源码基线均为 `227061b`；发布前工作树干净，`go test ./...`、`go vet ./...`、`git diff --check` 通过。
- 使用标准 `python scripts/deploy/deploy_remote.py 20260825-redeploy-227061b` 发布到 `/opt/nvidia-router-releases/20260825-redeploy-227061b`，镜像为 `nvidia-router:deploy-20260825-redeploy-227061b`；运行时 `.env` 与 deploy override 从现网 Release 继承。
- 首次构建因远端 Docker 镜像加速器对 `node:22.12.0-bookworm-slim` 返回 `not found` 失败，期间未停止 app、未生成备份、未切换现网；通过远端精确拉取该 tag 后重试成功。不要因此修改 Dockerfile、换基础版本或复用失败版本。
- 回滚点为 `/opt/nvidia-router-releases/20260825-design-tokens-ccc725d` / `nvidia-router:deploy-20260825-design-tokens-ccc725d`。切换前备份为 `/opt/nvidia-router-releases/20260825-redeploy-227061b/backups/predeploy-20260825-redeploy-227061b/router.db`，大小 10,391,552 字节，权限 `600`，UID/GID `10001:10001`，SHA-256 为 `87a7872b1bde68253100dec58ceab328a0ba7cf385882471c2ce12b82c56e878`。
- 发布后 app 使用目标镜像，`running/healthy`、重启 0、OOM false；schema 45、enabled 模型 11 个；3756 live/ready、18080/6020 healthz、根页均 200；匿名 `/v1/models` 与 `/metrics` 均 401；3756/18080/18081/6020 监听正常；临时归档已清理。
- 管理烟测通过：登录 200、鉴权 metrics 200、reasoning 接受请求 2/2 返回 200、预算协调请求 200、注销和临时 Key 清理由脚本 finally 执行。未执行完整模型矩阵、代理轮换或 CONNECT 矩阵；公网 HTTP 明文风险保持不变。

## 22. 2026-08-25 渠道状态美学重构与重新部署（b8e83c2）

- GitHub `main` 与部署源码基线均为 `b8e83c2`（`feat: redesign channel status with Claude and Codex aesthetics`）：
  - 前端融合 Claude 官方温润排版（暖白卡片、微光呼吸状态胶囊、清晰标题层级）与 Codex 官方精密遥测（4 联 KPI 态势概览看板、交互式状态过滤胶囊条、即时搜索框、4 列对齐核心指标网格与 Uptime Bar 悬停时间线）。
  - 补齐前端 `capability_probe_enabled` 运行时设置契约与测试用例，全量 44 套件 292 个前端单测全部 PASS，88/88 对比度配对合规，vue-tsc/eslint 0 错误。
- 使用标准脚本 `python scripts/deploy/deploy_remote.py 20260825-channel-status-b8e83c2` 发布到 `/opt/nvidia-router-releases/20260825-channel-status-b8e83c2`，镜像为 `nvidia-router:deploy-20260825-channel-status-b8e83c2`。
- 回滚点为 `/opt/nvidia-router-releases/20260825-redeploy-227061b` / `nvidia-router:deploy-20260825-redeploy-227061b`。切换前备份为 `/opt/nvidia-router-releases/20260825-channel-status-b8e83c2/backups/predeploy-20260825-channel-status-b8e83c2/router.db`，大小 10,518,528 字节，权限 `600`，属主 `10001:10001`。

## 23. 2026-08-25 渠道状态精修重构与重新部署（3f3ceae）

- GitHub `main` 与部署源码基线均为 `3f3ceae`（`feat: optimize channel status card with Claude and Codex telemetry aesthetics`）：
  - 优化卡面结构：双同心呼吸光晕指示灯、SLA 与延迟速度评级微标签、成功/异常分项拆解、60-slot Uptime Bar 跟随 Tooltip 悬浮提示与自适应对齐。
  - 新增 `/admin/model-health` 至 `/admin/channel-status` 的平滑路由别名重定向。
  - 前端 44 套件 292 个测试 100% PASS，vue-tsc/eslint 0 错误，静态闭包 41/41 完整。
- 使用标准脚本 `python scripts/deploy/deploy_remote.py 20260825-channel-status-3f3ceae` 发布到 `/opt/nvidia-router-releases/20260825-channel-status-3f3ceae`，镜像为 `nvidia-router:deploy-20260825-channel-status-3f3ceae`。
- 回滚点为 `/opt/nvidia-router-releases/20260825-channel-status-b8e83c2` / `nvidia-router:deploy-20260825-channel-status-b8e83c2`。切换前备份为 `/opt/nvidia-router-releases/20260825-channel-status-3f3ceae/backups/predeploy-20260825-channel-status-3f3ceae/router.db`，大小 10,559,488 字节，权限 `600`，属主 `10001:10001`。
- 远端按指令通过离线 CLI 完成管理员密码重置（密码未写入文件或日志）。
- 部署后验证：
  - 容器 `nvidia-router-app-1` 状态 `Up (healthy)`、重启 0；
  - 端点 `http://127.0.0.1:3756/health/live` 与 `http://127.0.0.1:3756/health/ready` 返回 200；
  - 静态资源 `/admin/` 正常返回 200；
  - 管理员认证：使用配置管理员密码 `POST /admin/api/auth/login` 返回 200 `authenticated: true`，获取 Session Cookie 请求 `/admin/api/model-health/summary` 正常返回 11 个白名单模型遥测数据。



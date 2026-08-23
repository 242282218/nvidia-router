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

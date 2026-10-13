# Deploy 配置

[English](../deployment.md)

Issue #125 首版支持每次部署启动一个临时 HTTP 服务。服务端会在创建
Sandbox 前，从用户选择的完整 Commit 读取仓库中的 `.qiniu/deploy.yaml`。

首版配置格式保持小而严格：

```yaml
version: 1
workingDirectory: .
installCommand: npm ci
startCommand: npm run start -- --host 0.0.0.0 --port 3000
port: 3000
healthcheck:
  path: /health
  timeoutSeconds: 60
```

`workingDirectory` 相对于检出后的仓库目录，不能越界。`installCommand` 和
`startCommand` 会在目标运行环境中执行受信任的仓库代码，不能在命令中写入
凭证。`healthcheck.path` 必须是相对于服务根地址的路径。解析器会拒绝未知字段、
多个 YAML 文档、不支持的版本、不安全路径、无效端口以及超过 64 KiB 的文件。

上述 camelCase 字段沿用现有仓库约定。也接受 `working_directory`、
`install_command`、`start_command` 和 `healthcheck.timeout_seconds` 下划线别名；
同一字段同时提供两种拼写会被拒绝。

解析器会将仓库配置映射为 deployd 的结构化 `Spec`。deployd 不拥有这套 YAML
格式，只负责校验执行参数，并将参数绑定到已解析的完整 Commit。Deploy 控制器
必须在创建 Sandbox 前持久化计划，不能把命令写入生命周期事件，也不能把
Sandbox 凭证放进计划。

启动应用时，runnerd 将当前 Sandbox 应用地址的精确域名注入
`__VITE_ADDITIONAL_SERVER_ALLOWED_HOSTS`。Vite preview 因此可以接受公网健康检查，
仓库无需预先写入动态域名或使用通配符 Host 白名单。该环境变量只在启动时传入，
不属于持久化的执行 Spec，也不会传给安装命令。

启动确认使用本地截止时间，不将它通过 envd `Connect-Timeout-Ms` 变成应用进程的存活期限。
确认 PID 后，取消观察流不会停止服务；应用继续运行，直到手动停止或 Sandbox 到期。
克隆和安装命令仍保留执行超时。

创建部署时从所选账号或组织的可运行公共／自有 Sandbox 目录中选择模板。
`runnerd.yaml` 可以为未传 `template_id` 的 API 客户端配置可选默认值：

```yaml
deploy:
  template_id: your-deploy-sandbox-template
  work_root: /var/lib/runnerd/deployments
  max_concurrent_per_scope: 3
  retention_hours: 168
  allow_admin_fallback: false
```

`template_id` 与 Runner Spec 完全分离。`work_root` 是控制器拥有的绝对路径，
位于 Sandbox 内，只用于每次 attempt 的临时工作目录。worker 会创建带独立元数据的
Sandbox，检出固定 Commit，持久化 deployd checkpoint，并在服务就绪后保持该
Sandbox 运行。系统记录 Sandbox ID、attempt、runtime ID 和进程 tag，重启后只会
重新连接同一个 attempt。命令输出在交给可选事件接收器前会限制大小并脱敏。每个部署
最多保留 2,000 条事件和 8 MiB 消息内容，时间线有界且优先保留最新证据。worker 不依赖默认模板配置。准入使用有效作用域凭据验证所选物理模板 ID，缺少模板、
模板不可用或凭据未配置时会在入队前拒绝。已有缺少模板的 queued 记录会明确失败，
应选择模板重新创建。创建／重试在事务中保存排队事件，并立即唤醒 worker；定期协调仍作为兜底。

区域来自该 scope 的有效 Sandbox endpoint。准入时保存区域标识、endpoint、加密凭据
和独立 Deploy 模板绑定，后续全局配置变更不会移动已准入部署的区域或模板。重试保留
不可变源码与模板绑定，并重新解析当前 scope 的凭据。Deploy 默认不使用平台 CI 兜底；
仅 `allow_admin_fallback: true` 时允许，且仍遵守已有的启用状态与 audience 限制。
Deploy 不使用 Fork sponsorship 或 Cache S3 凭据。

单个 Deploy worker 会定期连接 running attempt，并检查保存的 runtime/PID/tag。
健康服务在控制器重启后继续运行；临时 Provider 故障和身份不匹配会保留资源，等待下次检查。
Provider 查询后会再次检查到期时间，避免查询跨过截止时间时将正常到期误记为进程失败。
中断的 `creating` attempt 只按精确操作元数据查找并补偿，不重放用户命令或创建替代资源。
创建结果未知时保持非终态，直到找到并清理资源，或到期后的成功查询确认资源已不存在。
停止尚未确定的创建返回 `202`，等待补偿完成。确认 Sandbox 不存在才视为释放；重连使用
保存的剩余时长，初次创建时保存 Provider 返回的实际到期时间。此处只支持单进程恢复，
多实例 Deploy worker 尚未支持或验证。

每个账户或组织 scope 最多允许 `max_concurrent_per_scope` 个排队／运行中的部署
attempt。准入在状态事务中串行化，超过限制的并发请求返回
`429 deployment_quota_exceeded`，不会额外创建 Sandbox。Sandbox 已释放的终态记录
会按 `retention_hours` 保留，之后删除部署行和有界事件；审计事件继续作为长期控制面证据保留。

首版将 Provider 生成的 HTTPS 地址定义为**公开且不带应用层鉴权**的测试地址。部署就绪后
响应包含 `endpoint_access: "public"`。URL 本身不是授权机制；需要保护数据的应用必须自行
实现鉴权，不能把凭证写入仓库命令或输出。带鉴权的反向代理或签名访问契约另行设计。

该配置描述源码部署，暂不包含应用 Secrets、持久化数据库、自定义域名、多服务
编排或 CI/PR 自动触发。

## GitHub App 权限

私有仓库 Deploy 要求 App 具备仓库 **Contents: Read-only** 权限，以读取
`.qiniu/deploy.yaml` 和固定 Commit。现有用户 OAuth 流程还必须能够列出该仓库及其
Installation 访问范围。组织 Deploy 由 owner 管理；App 需要 **Members: Read-only**，
登录用户必须拥有 active GitHub organization owner membership（`role: admin`）。App
权限变更后，所有已有 Installation 都必须重新批准新增权限，组织才会显示为可管理。
Deploy 不会继承 CI Secrets、Cache S3 凭证或 Fork sponsorship 权限。

## 首个控制面切片

当前已提供用于持久化部署计划的登录 API。`installation_id` 是现有 Repositories
和 Preferences API 使用的本地关联 installation 记录 ID；外部 GitHub
Installation ID 由服务端解析：

- `GET /user/deployment-templates?installation_id=<local-id>` 使用与创建相同的 owner
  作用域检查，返回有效 Deploy 凭据／区域中的可运行自有与公共模板。只有明确启用
  Deploy fallback 时才允许平台兜底。
- `GET /user/deployment-refs?installation_id=<local-id>&repository_full_name=<owner/repo>&source=branch|tag&page=1`
  每页最多返回 100 个引用名称及 `has_more`。读取前检查与创建相同的 owner 作用域和
  OAuth/App 精确仓库交集；OAuth 缺失或被拒绝时失败关闭。
- `POST /user/deployments` 接收所选物理 `template_id`、已关联的本地 `installation_id`、精确的
  `repository_full_name`、`ref`、可选的 `source`（`branch` 或 `tag`）、可选的 `config_path` 和可选的
  `ttl_seconds`（五分钟到 24 小时，默认一小时）。可以使用 `Idempotency-Key` 请求头安全重放同一请求。
  传入 `source` 时，服务端先将名称限定为 `refs/heads/<name>` 或 `refs/tags/<name>`，
  避免同名分支与 Tag 混淆。未传 `source` 的 API 客户端仍可使用 branch/tag/commit ref。
  已知来源的响应返回来源和用于展示的原名称。
- `GET /user/deployments` 按当前登录账号直接从本地数据库分页读取部署历史，保留账号隔离，
  不调用 GitHub 或 Sandbox。仓库权限改变或保存的 GitHub Token 过期后，历史仍可列出。
- `GET /user/deployments/{id}` 检查记录归属、当前可管理 owner 作用域及 GitHub OAuth/App
  精确仓库交集。事件与生命周期操作保留这些授权检查，列表可见不会额外授予这些权限。
- `GET /user/deployments/{id}/events` 返回有界的生命周期／输出事件，并支持
  互斥的 exclusive `before_id`／`after_id` 游标。初次返回最新事件并按时间正序展示；
  `after_id` 返回后续正序页，使活动轮询能够连续补齐日志。
- `POST /user/deployments/{id}/stop`（或 `DELETE /user/deployments/{id}`）停止带
  tag 的进程并删除部署 Sandbox。清理失败会保留为 `failed`，worker 会继续重试。
- `POST /user/deployments/{id}/retry` 只有在上一轮 Sandbox 证据已清理后才能重新入队；
  原有完整 Commit 和计划保持不变。

创建流程会将 ref 固定为完整 Commit SHA，从该 Commit 读取配置，生成 deployd
不可变计划，加密保存规范化后的执行 Spec，并在同一事务中写入部署记录和审计事件。
这些 API 不返回命令内容。启用 worker 后，部署会依次进入 `planning`、`creating` 和
`running`；到期时重新连接保存的 Sandbox/PID attempt，停止带 tag 的进程并删除
Sandbox，然后记录 `expired`。创建或清理失败时只记录 `failed`，不会假设远端资源
已经释放。

`/deployments` 只展示部署列表和“创建部署”链接，点击进入 `/deployments/new`。
列表页不加载 GitHub App 或表单目录；`/deployments/new` 只显示创建表单，不读取部署列表或日志。

创建表单在明确选择仓库后提供“版本来源”（分支／Tag），再从真实引用列表中
“选择分支”或“选择 Tag”。切换仓库、作用域或版本来源后，旧发现请求不会覆盖当前列表。
空列表禁止提交；加载失败可重试，更多版本分页加载并保留当前选择。
App 无法读取版本时，表单提示 Installation owner 批准 Contents 读取权限后重试；
仓库可见不代表 App 已获准读取内容。

创建成功后进入独立的 `/deployments/{id}` 详情地址，按 ID 直接读取资源，
不依赖列表第一页。刷新和浏览器前进／后退保留选择。详情逐行展示阶段、stdout／stderr，
轮询活动 attempt 并合并已有历史，支持更早日志和手动刷新；读取失败时保留已加载日志并显示重试提示。

## 实现迭代方案

2026-10-13 的公开执行验收使用公共 `base` 模板，以及 `qiniu/deployment` 的完整 Commit
`fe64a78977093d7c7fd7fb9a159975c5f73f8ff6`，仓库配置保持不变。真实 worker 返回 HTTP 200，
控制器对象重连保留原 attempt，手动停止进入 completed，三分钟 TTL 后进入 expired。
两次清理均由 Provider 404 确认资源释放。

同日的已登录本地浏览器验收使用 `qiniu/deployment` 的 `example/vite` 分支与 `base`：
创建返回 201，跳转到详情，展示安装／启动／就绪日志，刷新后保持同一详情和日志。
公网 HTML 返回 HTTP 200，浏览器无 console/page 错误。停止 API 返回 200，记录进入
`completed` 并清空 Sandbox 引用。中文表单的分支／Tag 字段和空 Tag 禁止提交也已验证。
私有仓库权限、已登录的 retry/delete 流程、完整进程重启及 Provider 故障注入仍需单独验收。

1. **预检与计划（已完成）**：严格解析仓库配置，固定完整 Commit，校验 OAuth/App
   精确仓库交集，加密保存 Spec 快照，并以审计事务持久化部署状态。
2. **Sandbox 执行与恢复（本地完成）**：解析作用域 Sandbox 服务和独立 Deploy 模板，
   创建独立 Sandbox，检出固定 Commit，运行 `deployd.Apply`，持久化 checkpoint，
   使用 Sandbox/PID/attempt fencing，并在重试前按精确的 Provider 元数据补偿结果未知的创建操作。
3. **生命周期 API（本地完成）**：后端已经提供停止／删除／重试、TTL 到期、有界部署事件
   和日志，以及带状态的清理证据；真实 Provider 故障注入验收仍待完成。
4. **用户界面（首片已完成）**：`/deployments` 展示本地历史，`/deployments/new` 处理授权
   仓库、分支／Tag、模板选择与不可变部署创建，`/deployments/{id}` 展示状态、地址、TTL、
   生命周期／输出事件及停止／删除／重试操作。
   本地验证和已登录的公开仓库创建／详情／刷新／停止流程已通过；剩余真实验收见上文。
5. **验收**：完成聚焦测试、本地启动／构建检查，再用真实 GitHub App 和 Sandbox
   验证部署、停止、到期、重启恢复及越权访问证据。

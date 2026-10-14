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
和独立 Deploy 模板绑定，后续全局配置变更不会移动已准入部署的区域或模板。重试会重新解析
当前 scope 的凭据，并按重新部署方式刷新分支源码或复用不可变源码与模板绑定。Deploy 默认不使用平台 CI 兜底；
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
Installation 访问范围。Deploy 与 Jobs 一样按 OAuth/App 的 Installation＋仓库精确交集
授权，不按创建者或组织角色隔离。仓库成员可以查看、创建、重新部署、停止和删除部署。
Sandbox Preferences 与私有模板目录仍由 owner 管理；管理组织设置仍需
**Members: Read-only** 和 active owner membership（`role: admin`）。
Deploy 不会继承 CI Secrets、Cache S3 凭证或 Fork sponsorship 权限。

## 首个控制面切片

当前已提供用于持久化部署计划的登录 API。`installation_id` 是现有 Repositories
和 Preferences API 使用的本地关联 installation 记录 ID；外部 GitHub
Installation ID 由服务端解析：

- `GET /user/deployment-templates?installation_id=<local-id>&repository_full_name=<owner/repo>`
  向作用域 owner 返回可运行的自有／公共模板。其他用户必须通过精确仓库授权，
  只获得公共模板 ID 与名称，不返回私有目录或作用域元数据。非 owner 创建时只能
  选择已验证的公共模板；仓库成员可以重新部署已有的保存版本。Deploy fallback 仍需明确启用。
- `GET /user/deployment-refs?installation_id=<local-id>&repository_full_name=<owner/repo>&source=branch|tag&page=1`
  每页最多返回 100 个引用名称及 `has_more`。读取前检查与创建相同的
  OAuth/App 精确仓库交集；OAuth 缺失或被拒绝时失败关闭。
- `POST /user/deployments` 接收所选物理 `template_id`、可选的模板目录快照 `template_name`、
  `template_cpu`、`template_memory_mb` 和 `template_disk_mb`、已关联的本地 `installation_id`、精确的
  `repository_full_name`、`ref`、可选的 `source`（`branch` 或 `tag`）、可选的 `config_path` 和可选的
  `ttl_seconds`（五分钟到 24 小时，默认一小时）。可以使用 `Idempotency-Key` 请求头安全重放同一请求。
  创建时保存原始规范化准入字段的摘要。Provider 更新到期时间、停止或重新部署后，重放仍
  返回已有资源，不启动新 attempt；执行字段不同则返回 409。没有摘要的历史记录无法安全
  重建原请求，因此拒绝幂等键重放；新工作使用新键。
  部署 ID 使用 26 位小写 Base32 编码，保留 128 位标识空间，可直接用于 URL。
  有幂等键时根据账号和请求键生成固定 ID；无幂等键时生成新的随机 ID。
  客户端应将 ID 作为不透明字符串使用；上线前不提供旧 ID 的重放回退逻辑。
  传入 `source` 时，服务端先将名称限定为 `refs/heads/<name>` 或 `refs/tags/<name>`，
  避免同名分支与 Tag 混淆。未传 `source` 的 API 客户端仍可使用 branch/tag/commit ref。
  已知来源的响应返回来源和用于展示的原名称。
- `GET /user/deployments` 在本地数据库中按已授权的 Installation＋仓库过滤，再计算
  总数和分页；创建者身份不限制可见性。
- `GET /user/deployments/{id}`、已保存事件、版本与生命周期操作复用 Jobs 的仓库授权缓存：
  20 秒后刷新，30 秒硬过期，并合并并发刷新。缓存未命中时可能访问 GitHub；部署内容
  本身不需要 Sandbox I/O。OAuth 缺失或被拒绝时失败关闭，原创建者也遵守该规则。
  创建／重试为每个 revision 保存执行账号；worker 在检出来源前重新校验该操作者。
  历史执行账号为零时使用原创建者；创建者身份和幂等键归属保持不变。
- `GET /user/deployments/{id}/events` 返回有界的生命周期／输出事件，并支持
  互斥的 exclusive `before_id`／`after_id` 游标。初次返回最新事件并按时间正序展示；
  `after_id` 返回后续正序页，使活动轮询能够连续补齐日志。
- `POST /user/deployments/{id}/stop`（或 `DELETE /user/deployments/{id}`）停止带
  tag 的进程并删除部署 Sandbox。清理失败会保留为 `failed`，worker 会继续重试。
- `POST /user/deployments/{id}/retry` 只有在上一轮 Sandbox 证据已清理后才能重新入队。
  JSON 请求体可使用 `{"mode":"latest"}` 重新解析分支并保存新的不可变计划，
  或使用 `{"mode":"revision","revision_number":N}` 复用保留的部署版本；省略请求体时复用当前版本。

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

## 部署版本与日志

失败后的补偿清理成功时，部署及版本仍保留原始失败阶段与原因；清理过程中断后恢复也遵循此规则。

Deployment 保持稳定的资源 ID。首次创建以事务保存版本 #1；
`POST /user/deployments/{id}/retry` 创建下一个版本。使用分支最新代码时会重新解析
已移动的分支，复用版本时复制所选固定 Commit 和加密计划。新版本、排队日志及审计在同一事务中提交。
只有当前版本可推进，旧版的来源、状态、地址、已确认 Sandbox 标识与日志独立保留。
执行 attempt 继续用于 Provider fencing，与用户可见的版本编号分开。

本地详情返回 `revision_number`、最新 100 条 `revisions`、`revisions_has_more` 和
最近确认成功的版本编号。更早版本通过
`GET /user/deployments/{id}/revisions?before_number=N` 获取；版本日志通过
`GET /user/deployments/{id}/revisions/{number}/events` 获取，支持相同的排他
`before_id`／`after_id` 游标。原 `/events` 接口读取当前版本。
这些读取通过 Jobs 缓存校验精确仓库权限，使用 `Cache-Control: no-store`，不请求 Provider，
也不返回加密计划或服务凭据。

详情上方展示已保存的目标配置，下方左侧为版本历史、右侧为所选版本的部署日志。
日志逐行展示行号、毫秒时间戳和原始正文，stderr 与失败分别强调。
切换版本后重置游标，拒绝前一版本的迟到响应。
`/deployments/{id}?revision=N` 在刷新及浏览器历史导航后保留选择；
即使所选旧版不在最新一页，也能直接读取。展示页面无需实时探测 Sandbox。

升级以新增字段和索引方式兼容旧数据库，为缺少版本的部署补为 #1，保留原来源、
凭据、时间戳、attempt 证据及日志。旧记录的更早执行日志一起保留在导入的 #1 中，
不重建已经丢失的历史状态。日志数量与字节预算按版本分别计算。
终态保留期结束后，在同一事务中删除 Deployment、所有版本及日志，审计仍保留。

## 实现迭代方案

2026-10-13 的公开执行验收使用公共 `base` 模板，以及 `qiniu/deployment` 的完整 Commit
`fe64a78977093d7c7fd7fb9a159975c5f73f8ff6`，仓库配置保持不变。真实 worker 返回 HTTP 200，
控制器对象重连保留原 attempt，手动停止进入 completed，三分钟 TTL 后进入 expired。
两次清理均由 Provider 404 确认资源释放。

同日的已登录本地浏览器验收使用 `qiniu/deployment` 的 `example/vite` 分支与 `base`：
创建返回 201，跳转到详情，展示安装／启动／就绪日志，刷新后保持同一详情和日志。
公网 HTML 返回 HTTP 200，浏览器无 console/page 错误。停止 API 返回 200，记录进入
`completed` 并清空 Sandbox 引用。中文表单的分支／Tag 字段和空 Tag 禁止提交也已验证。
当时私有仓库权限和已登录的 retry/delete 流程尚未验收；后续结果记录在下文。

2026-10-14，用户在 Web 终端实现后确认真实 Sandbox 验收已通过。此处记录用户完成的
验收，未附加命令记录或逐项场景结果；上述 Issue #125 的独立验收项仍保持待完成。

2026-10-14 的另一轮验收使用普通 Google Chrome、相同的公开仓库 Commit 与 `base`。
创建和刷新保留短部署 ID 与日志。完整重启 runnerd 后，PID 从 99226 变为 4048，部署 ID、
版本 #1、attempt #1、Sandbox ID、runtime ID、进程 ID／tag、endpoint、到期时间及 Commit
均保持不变；重启前后公网 HTML 均返回 HTTP 200。随后通过已登录浏览器停止，记录进入
`completed` 并清空 Sandbox 引用。本轮未单独查询停止后的 Provider 404，也未同时运行 CI Job。

2026-10-14 的真实 Provider 故障注入通过主动启用的 `create-timeout` worker 子测试完成：
丢弃一次真实 Sandbox 创建成功的响应，并让首次元数据查询超时。记录保持 `creating`、
attempt #1，未记录尚未确认的 Sandbox ID。新的 worker 按精确操作恢复资源并补偿清理，
保留 `failed`／`recovery`，清理后通过 Provider 404 确认释放；传输层仅接到一次创建请求。
传输故障由测试注入，创建、元数据查询与释放使用真实 Provider。这是 worker 层证据，
不代表重复的已登录 API 提交或真实 Provider 服务中断已验收。

2026-10-14 在 `09c3a81` 上通过普通 Chrome 继续验收：使用版本 #1 的原计划创建 #2，
再选择最新分支代码创建 #3，两次公网访问均返回 HTTP 200。刷新后保留 #1 与旧日志，
历史版本终端不可连接。两个新版本均已停止，最终记录为 completed、Sandbox 引用为空，
三个版本均保留。分支期间没有前进，因此不能证明移动分支行为；这两次停止未单独验证
Provider 404。本次记录早于仓库共享授权调整，不代表跨账号真实验收；私有仓库和 fork PR
真实执行按当前安排暂缓。

2026-10-14 的已认证 API 验收使用运行中的本地 runnerd、相同 Commit 的
`qiniu/deployment`／`example/vite` 与公共 `base`。同一幂等键并发提交两次只创建一个
部署和 attempt；作用域上限为 3，超额请求返回 429 且不落库，释放后准入恢复。
公网 HTML 返回 200；DELETE 后进入 completed、清空 Sandbox 引用，并通过 Provider
查询确认资源不存在。清理后重放返回同一终态记录，20 条事件仍保留；本轮准入的四个
测试资源均已停止。测试为已有本地账号签发仅保存在内存中的短期 session，覆盖真实授权
与 Provider 执行，不代表 OAuth 登录或第二个用户已验收。本轮发现 Provider 到期时间
精度导致幂等重放冲突，现已改为比较不可变的创建请求摘要，不依赖可变的到期时间或版本字段。

独立的真实 worker `start-failure` 验收在固定公开计划中注入 `install: true` 与
`start: exit 23`：仅创建一个 Sandbox，部署和版本均保留 `failed`／`probing`，
清理后由 Provider 查询确认资源不存在。这属于合成计划的真实执行证据，不代表仓库配置准入验收。

运行中的本地 worker 也通过了保留期加速验收：配置为 168 小时，仅将上述已确认
Sandbox 释放的 API 测试记录的 `updated_at` 回拨 169 小时。正常清理循环删除了该部署、
一个版本及 20 条事件，两个审计事件保持不变。这证明实际进程在时间戳注入下的清理行为，
不代表已自然等待七天。

1. **预检与计划（已完成）**：严格解析仓库配置，固定完整 Commit，校验 OAuth/App
   精确仓库交集，加密保存 Spec 快照，并以审计事务持久化部署状态。
2. **Sandbox 执行与恢复（本地完成）**：解析作用域 Sandbox 服务和独立 Deploy 模板，
   创建独立 Sandbox，检出固定 Commit，运行 `deployd.Apply`，持久化 checkpoint，
   使用 Sandbox/PID/attempt fencing，并在重试前按精确的 Provider 元数据补偿结果未知的创建操作。
3. **生命周期 API（本地完成）**：后端已经提供停止／删除／重试、TTL 到期、有界部署事件
   和日志，以及带状态的清理证据；真实创建响应丢失后的恢复与清理已验证。
4. **用户界面（首片已完成）**：`/deployments` 展示本地历史，`/deployments/new` 处理授权
   仓库、分支／Tag、模板选择与不可变部署创建，`/deployments/{id}` 展示状态、地址、TTL、模板 CPU／内存／磁盘资源、
   生命周期／输出事件及停止／删除／重试操作。
   本地验证和已登录的公开仓库创建／详情／刷新／停止流程已通过；剩余真实验收见上文。
5. **验收**：完成聚焦测试、本地启动／构建检查，再用真实 GitHub App 和 Sandbox
   验证部署、停止、到期、重启恢复及越权访问证据。

## 部署 Web 终端

详情页提供“部署日志”和“终端”标签。只有当前运行中、未到期且具有 Sandbox ID
的版本可手动连接终端；切换标签或版本、离开详情页、停止部署都会关闭交互 shell。
终端可检查进程和应用日志文件，不会自动重放部署日志或附加到服务进程的 stdout；
shell 中的修改仅作用于当前 Sandbox。

终端路由前缀为 `/user/deployments/{id}/revisions/{number}/terminal`：
`POST` 创建 PTY，`GET /{sessionID}/events` 订阅输出，
`POST /{sessionID}/input` 发送输入，`POST /{sessionID}/resize` 调整大小，
`DELETE /{sessionID}` 关闭终端。访问会检查当前用户的仓库权限及 shell 归属；关闭已有终端
只需该终端的创建账号，以便在权限或生命周期变化后仍能清理。
会话绑定打开终端的账号、部署、revision、attempt 和 Sandbox 标识，历史版本不能连接当前 Sandbox。
详情与已保存日志继续从本地读取；终端使用部署保存的 Provider endpoint 和加密凭据，
不向浏览器暴露凭据。连接使用部署剩余有效期，不使用固定 Sandbox 超时。
本地生命周期每 5 秒检查一次，已打开的会话每 30 秒重新检查远程权限；
输出连接断开后沿用 30 秒空闲清理。

## GitHub 上下文路由

Jobs 和部署使用仓库资源集合路由：

- `/github/{owner}/{repo}/jobs` 与 `/github/{owner}/{repo}/deployments`
- `/github/{owner}/{repo}/jobs?branch=example/vite`
- `/github/{owner}/{repo}/deployments?branch=example/vite`
- `/github/{owner}/{repo}/deployments?tag=release/v1`
- `/github/{owner}/{repo}/pull_requests/{number}/jobs` 或 `/deployments`
- `/github/{owner}/{repo}/runs/{number}/jobs` 用于缺少来源上下文的 Jobs

Branch 和 Tag 名称放在查询参数中。浏览器链接保留可读的 `/`，同时继续编码
`&`、`#`、`+`、`%` 等字符。先编码再仅恢复 `%2F`，ref 中字面的 `%2F` 仍保留为 `%252F`。
`branch` 与 `tag` 互斥，空值和重复的来源条件无效。Jobs 尚无可靠的 Tag 证据，Tag 筛选仅用于部署。
`commit={sha}` 可按不可变提交筛选列表，分支 Job 分组链接始终携带它。
`job={id}`、`deployment={id}` 选择资源，`revision=N` 选择部署的不可变版本。
部署详情 URL 不携带 `commit`，实际 SHA 由已保存的部署和版本确定。
详情展示短 SHA，链接到来源仓库的 GitHub Commit，并提供完整 SHA 的复制按钮。
返回历史列表时展示整个版本来源，不自动添加 Commit 筛选；显式筛选 Commit 的列表 URL 仍然有效。

仓库 Jobs 仅显示已有授权历史窗口（最多加载 500 条 Runner 请求）中的匹配分组，
空结果不会退回其他仓库；本次路由调整不新增无限历史归档。仓库部署列表在数据库分页前
筛选已保存版本。未选择部署时展示当前上下文的部署历史。
`/deployments` 保留为账号全局列表，`/deployments/new` 保留为创建表单。
仓库级创建链接预填仓库，再由用户选择版本来源。旧 `/github/pulls/...`、
`/github/branches/...`、`/jobs/...`，以及上一版 `/pr/`、`/branch/{encoded-name}/`、
`/tag/{encoded-name}/`、`/run/` 路径继续兼容，转换到新浏览器地址时保留 Job、部署和版本选择。
既有 API 路径不变。

旧 `/deployments/{id}` 链接读取本地记录后替换为保存的来源地址，历史版本使用自己的
ref，Commit SHA 从保存的版本读取。URL 中的仓库与来源不授予权限：详情仍按授权仓库和部署 ID 校验，再修正展示地址。
`GET /user/deployments` 的 `repository_full_name`、`ref`、`commit_sha` 和
`pull_request_number` 筛选在分页前查询已保存版本，`selected_revision` 标识匹配的历史版本。
这些读取不请求 GitHub 或 Sandbox。

Jobs 分组及独立详情提供“部署历史”和“部署此提交”，使用当前选中 Job 自己的仓库和
PR／分支。历史列表包含该来源的所有提交；创建时固定所选 Job 的 SHA，包括历史 Job。创建链接使用
`/deployments/new?repository=...&source=...&ref=...&commit_sha=...`，参数仅用于预填。
表单等待已授权、可管理账号的目录加载，保留显式指定但已关闭的 PR，并显示固定 SHA。
修改仓库、版本来源或 ref 会清除旧 SHA；“改用版本来源的最新提交”可主动解除固定。
提交时由 GitHub 校验最终来源。缺少完整 SHA 或来源信息的 Job 不展示这些入口。
PR 合成 merge SHA 无法通过 head 提交归属校验，会明确拒绝。

## PR 部署

创建时传入 `source: "pull_request"`，`ref` 为正整数 PR 编号。表单列出开放的 PR；
API 可显式指定已有的关闭 PR。执行固定的 PR head 提交，不部署 GitHub 合成的 merge 结果。
Sandbox 作用域与配额归属于已授权的目标仓库；GitHub 返回的 base 仓库身份必须一致，
来源仓库已删除或不可用时拒绝创建。服务端解析 `refs/pull/{number}/head`，确认与观察到的
head SHA 相同，然后通过目标仓库读取配置、检出该不可变 SHA。fork 提供的 URL 不决定
凭据或检出地址。GitHub App 需要已批准的 `Pull requests: Read-only` 和 `Contents: Read-only`。

可选 `commit_sha` 固定之前选择的提交。PR 提交须属于 GitHub 返回的 PR 提交列表
（最多 250 个）；分支提交须为当前 head 的祖先；Tag 须仍指向相同提交。强推后不可用或
不属于该来源的提交会拒绝，不会静默替换成新 head。每个版本保存 `source_repository_id`、
`source_repository_full_name` 和 `source_ref`；旧记录通过新增字段升级，来源证据保持为空。
PR 编号保存在完整 ref 中，并通过 `pull_request_number` 返回。最新重部署重新解析 PR head，
历史版本重部署复制已保存的来源与计划。历史内容仍从本地读取，并向有仓库权限的成员共享。

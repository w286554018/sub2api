# 吸收 ranxi2001/sub2api(production) 功能 — 前端/文档/运维只读取证审计

## 0. 取证基线与方法
- 本地 HEAD `1f08460552fbac66106baa97d0e3841f93057d58`；来源 `refs/remotes/ranxi-review/production` = `1d2ed630e292eaaac95454c1786bf3b7e5ed0828`；共同祖先 `efe9aab1e4ec89a42ba45e8dac20e882c5409a6a`（`git rev-parse` / `git merge-base` 实测）。
- `git diff --stat efe9aab..source`：3002 文件，+328000/−11377。前端范围 `-- frontend docs deploy README.md`：1089 文件，+69830/−3575。来源是 tosky.io 维护的独立生产 fork（README.md:14-18），已长期分叉，不是小增量。
- 方法：`git show <ref>:<path>` + `git grep <ref> -- <path>` 只读取证；全部结论有代码/文件行号支撑，未用截图推断。来源分支自带的 docs/screenshots/*.png 一律不作为证据。
- 来源 docs 版本号自相矛盾（README 与 docs/新人入门.md 写 v2.8.13；docs/MIHOMO_MANAGED_CN.md 写「2.7.3 开发中」；deploy/kubernetes/README.md 提到 2.9.4），移植时不能据此判断功能完成度。

## 1. 关键结论：本地已有一批「同目的、不同实现」的能力（重叠但非同一功能的）
| 本地已有 | 来源对应 | 关系 |
|---|---|---|
| `/admin/account-health`（AccountHealthView.vue）+ `/admin/intelligent-tests`（IntelligentTestsView.vue）+ api/admin/accountHealth.ts、intelligentTests.ts；路由 `registerAccountHealthRoutes`/`registerIntelligentTestRoutes`（本地 admin.go:115-121、801-811） | `/admin/account-quality`（质量运维/降智）+ `/admin/account-ops`（账号运维） | **不同实现**。已核实：`git grep -l -i intelligent 1d2ed630… -- backend frontend/src` 只命中 3 个无关文件；共同祖先的 admin.go 也没有 intelligent/account-health 路由 → 本地的「账号探测」是本地分支独有；来源的 account-quality 由 ScheduledTest handler 提供（来源 admin.go:868-874）。两者语义重叠（都做账号质量判定），但数据表、API、页面全不同，**不能互相替代，必须先做取舍决策** |
| 渠道监控 V1/V2：ChannelMonitorView.vue、ChannelStatusV1/V2View.vue、api/channelMonitorV2.ts、featureFlags `ChannelMonitorMode = 'v1' | 'v2'`（本地 frontend/src/utils/featureFlags.ts:181） | 渠道监控 V3 + V2 candy 探针 | **同族扩展**。V3 新增；V2 新增 candy（合成探针）与多模型 |
| Codex 292 打票设置（本地 SettingsView.vue:5829「Codex 292 门票打票」） | 打票流水 Harvest Flow（/admin/harvest-flow）+ codex 并行采集 | **同族扩展**，来源新增并行采集与流水视图 |
| ProxiesView.vue + `/proxies` CRUD（本地 admin.go:606-622） | Mihomo 节点→静态代理绑定 | **复用点**：来源用 `proxies.mihomo_node_id` 把 Mihomo 节点挂进既有代理表，而不是另建代理体系 |
| RiskControlView.vue + `risk-control` 路由 | 同名（来源有修改） | 共享，来源仅增量修改 |
| ops 监控/图表（admin/ops/components，本地已有 20 个组件，与来源基本一致） | 仅新增 TokenGuardView/TokenGuardV2View | 图表/监控大盘基本共享 |

## 2. 功能组（15 项）

### A. Mihomo 出口/节点管理 + 受管节点静态代理（全栈最大新增，本地完全缺失）
- **缺失证据**：`backend/internal/mihomo/`、`backend/internal/handler/admin/system_mihomo.go`、`frontend/src/views/admin/settings/MihomoSettings.vue` 本地 Test-Path 均为 MISSING。
- **来源路径**：`backend/internal/mihomo/*.go`（约 48 个文件：manager.go、sources.go、collection.go、country.go、node_probe.go、subscription_download.go / subscription_download_mode.go、dynamic_subscription.go、static_bindings.go、controller_status.go、lease.go、migrate.go、node_names.go、process_linux.go / process_other.go、harvest_directed.go、harvest_managed.go，另有 bps_*.go 20 个）；`backend/internal/handler/admin/system_mihomo.go`；前端 `frontend/src/views/admin/settings/MihomoSettings.vue`(560 行)、`MihomoCountryFilter.vue`、`MihomoProxySelector.vue`、`mihomoCountry.ts`。
- **路由入口**（`backend/internal/server/routes/admin.go:761-766`）：
  - `GET  /api/v1/admin/system/mihomo` → `Admin.System.GetMihomo`
  - `POST /api/v1/admin/system/mihomo` → `ManageMihomo`（订阅增删改、节点 probe/country_probe/disable/recover、dynamic_remove 等操作字面量见 MihomoSettings.vue）
  - `PUT  /api/v1/admin/system/mihomo/download-mode` → `SetMihomoDownloadMode`（auto/proxy/direct）
  - `POST /api/v1/admin/system/mihomo/nodes/:name/test` → `TestMihomoNode`
  - `POST /api/v1/admin/system/mihomo/nodes/:name/quality-check` → `CheckMihomoNodeQuality`
  - `POST /api/v1/admin/system/mihomo/nodes/static-proxies` → `AddMihomoNodesAsStaticProxies`
  - 前端入口是 SettingsView 内的分区（无独立 router 路由），页面分 subscriptions / nodes / dynamic 三区。
- **配置与运维要求**：仅 Linux amd64/arm64；内核固定 v1.19.31，走官方 Release asset + SHA-256 校验；内核运行在 `DATA_DIR/mihomo-codex`，以应用用户身份，不需 sudo；订阅/节点凭据/控制器 secret 存 0600 文件且 API 不回显；`deploy/install-mihomo-codex.sh`、`deploy/migrate-mihomo-managed.sh`（旧 systemd 内核迁移，root 入口）、`deploy/mihomo-codex.service`（`User=mihomo-codex`、`WorkingDirectory=/var/lib/mihomo-codex`）、`deploy/mihomo-codex.config.example.yaml`、`deploy/tests/mihomo-arm64.Dockerfile`。端口：BPS 基线 `19000`（bps.go:12 bpsPortBase），静态代理固定出口 `32000-41999`（docs/mihomo-static-node-proxies.md）。
- **数据库**：`backend/migrations/274_mihomo_static_proxy_bindings.sql`（`proxies.mihomo_node_id` + 未删除唯一索引）。
- **测试**：`backend/internal/mihomo/*_test.go`（约 25 个）+ `handler/admin/system_mihomo_download_test.go`、`system_mihomo_node_test.go`；前端 `MihomoSettings.spec.ts`、`ProxiesView.management.spec.ts`、`ProxiesView.credentials.spec.ts`。命令见 docs/MIHOMO_MANAGED_CN.md 末尾（`MIHOMO_INSTALL_SMOKE=1 go test ./internal/mihomo …`）。
- **文档**：docs/MIHOMO_MANAGED_CN.md、docs/mihomo-static-node-proxies.md、docs/design/mihomo-backend-api.md、docs/dynamic-proxy-regions.md、docs/bps-mihomo-sessions.md。

### B. Excel / BPS（Basispoints）路由 + 2FA/OAuth 重登工具链
- **缩写定性**：README.md:61 明确写作「**Excel / Basispoints**」→ BPS 是既有的 OpenAI「Excel 会话」通道（强制 HTTP/SSE 走 `/v1/responses`、`/v1/responses/compact`），**不是带宽/字节每秒**。docs/excel-bps.md 说明：开启后请求强制走 BPS，不与 Codex 通道静默回退；`credentials.plan_type=free` 禁止开启。
- **缺失证据**：本地无 `docs/excel-bps.md`、无 `handler/openai_excel_bps_image.go`、无 `AccountAutoBPSSection.vue` / `ExcelBPS403Badge.vue` / `ExcelBPSCredentialStatus.vue` / `ExcelBPSModeSwitches.vue`，也没有 `tools/openai_oauth_reauth_worker.py` 与 `prism-adapter/`。本地已有 `tools/` 目录及指纹、Adobe 等工具，应保留并增量接入新工具。
- **来源路径**：`backend/internal/handler/openai_excel_bps_image.go`、`backend/internal/mihomo/bps*.go`（bps、bps_dynamic、bps_errors、bps_health、bps_protocol_probe、bps_quality、bps_race、bps_static、bps_warm_pool）；前端 `frontend/src/components/account/{AccountAutoBPSSection,ExcelBPS403Badge,ExcelBPSCredentialStatus,ExcelBPSModeSwitches}.vue`、`api/admin/openaiTotp.ts`、`api/admin/credentialEncryption.ts`；工具链 `tools/openai_oauth_reauth_worker.py`(1347 行)、`openai_excel_2fa_login.py`、`openai_excel_oauth_adapter.py`、`openai_excel_password_flow.mjs`、`openai_totp_journal.mjs`、`openai_totp_rotation.py`、`tools/reauth-runtime/`、`tools/Dockerfile.openai-oauth-reauth-worker`；`prism-adapter/`（约 20 文件，含 .service/.conf）；`scripts/verify-controlled-experiments.ps1`。
- **路由入口**（`backend/internal/server/routes/gateway.go:53-57`）：`GET|HEAD /api/bps-images/:token` → `h.OpenAIGateway.ExcelBPSImage`，**免鉴权仅限 capability URL**（无公开上传口，仅鉴权 BPS 请求可创建）；`middleware.ExcelBPSImageAdmission()` 标记 BPS 图片准入（别名标记见 excel_bps_image_admission_test.go）。
- **配置**：setting key `SettingKeyExcelBPSImageRelayEnabled` / `SettingKeyExcelBPSImageBaseURL`（测试内实证 excel_bps_image_admission_test.go:22）；账号 extra `openai_excel_bps`、`openai_excel_bps_models`（gateway_models_pinned_test.go:117）。
- **数据库**：271_openai_excel_dual_oauth.sql、254_openai_oauth_reauth.sql、256/257（reauth proxy override/source）、262（reauth engine）、269_openai_totp_rotation.sql。
- **测试**：`openai_excel_bps_error_test.go`、`openai_excel_bps_failover_test.go`、`routes/excel_bps_image_test.go`、`routes/excel_bps_image_admission_test.go`、`dto/excel_bps_credential_state_test.go`、`tools/test_openai_*`（5+）。
- **文档**：docs/excel-bps.md、docs/bps-*.md（12 篇）、docs/openai-2fa-*.md、docs/features/bps-*.md。**注意**：本项与本任务排除的「协议后端实现」高度重叠，此处只覆盖入口/前端/文档/工具。

### C. 渠道监控 V3（用户状态页 + 管理配置）+ V2 candy 探针
- **来源路径**：后端 `handler/channel_monitor_v3_handler.go`(259 行，+test)、`service/channel_monitor_v3.go`、`service/channel_monitor_v3_probe.go`；前端 `views/user/ChannelStatusV3View.vue`(69 行，薄封装) → 共享模块 `frontend/src/features/channel-monitor-v3/{StatusPage,IncidentsDialog}.vue`，`api/channelMonitorV3.ts`。
- **路由入口**：
  - 管理（admin.go:1047-1064）：`PUT /admin/channel-monitor-mode`→SetMode；`/admin/channel-monitor-v3`：GET settings、PUT config、**PUT idle-probe**、POST/PUT/DELETE categories/:id、POST/PUT/DELETE components/:id、POST reorder、GET status、GET incidents。守卫：仅 feature guard（任何 mode 都可编辑/预览）。
  - 用户（routes/user.go:182-188）：`GET /channel-monitor-v3/status`、`GET /channel-monitor-v3/incidents`，守卫 mode=v3 + `panelRateLimiter.Heavy()`。
  - 守卫实现 admin.go:1067-1108（feature/modeV2/modeV3 guard，错误 `ErrChannelMonitorDisabled`、`ErrChannelMonitorModeMismatch`）。
- **「降智」澄清**：渠道监控 V3 只有 **degraded 阈值判定**——`DegradedErrorRate` + `DegradedTTFTMs`（channel_monitor_v3.go:24、73-77、923-924；TTFT 阈值档 1s…120s，取 P50 桶）。真正的「降智」业务判定在 Account Quality（见 D，`account_quality*.go` 的 FailureThreshold）。**两者不要混为一谈**。
- **空闲探测**：`backend/migrations/273_channel_monitor_v3_idle_probe.sql`——对选定分组，真实流量过少的时段在结束前发一次短探测，避免空白变灰，**默认关闭**；前端 `MonitorV3Cell.probed` / `probe_latency_ms`（api/channelMonitorV3.ts）。
- **数据库**：264_channel_monitor_v3.sql（categories/components；注释明确「V3 自身不发请求、不存事实，只映射 V2 被动聚合」）；V2 candy：254_channel_monitor_v2_candy.sql（`candy_probes` JSONB + `channel_monitor_v2_candy_results`）、275_..._candy_multi_model.sql、276_..._candy_history_min.sql。
- **i18n**：`frontend/src/i18n/locales/{zh,en}/channelMonitorV3.ts`。
- **测试**：`handler/channel_monitor_v3_handler_test.go`、`repository/channel_monitor_v3_integration_test.go`、`channel_monitor_v3_probe_integration_test.go`；本地既有 `views/admin/__tests__/ChannelMonitorView.{duplicate,grok,modeSwitch,checkModeBadge}.spec.ts`、`views/user/__tests__/ChannelStatusView.mode.spec.ts`。

### D. 质量运维 / 降智检测（Account Quality）
- **来源路径**：`service/account_quality.go`、`account_quality_bps.go`（第三种处置：判定降智→自动开 Excel/BPS）；前端 `views/admin/AccountQualityView.vue`(1099 行)、`api/admin/accountQuality.ts`；i18n 需 qualityOps。
- **路由入口**（admin.go:868-874）：`GET /admin/account-quality-results`、`GET /admin/account-quality-plans`、`POST /admin/account-quality-plans/:id/run`、`GET|POST|PUT|DELETE /admin/account-quality-templates[/:id]`（全部走 `Admin.ScheduledTest` handler）。
- **数据库**：247_account_quality_ops.sql（按账号唯一索引 + account_quality_states）、248_account_quality_judgment.sql、254_quality_bps_coexist.sql（每账号按处置类型分别唯一）、258_quality_observation_scope.sql、260_quality_rule_templates.sql。
- **运维要求**：BPS 规则默认 30 分钟经正常协议探测（不经 BPS）；糖果题可走 BPS，状态探针始终测正常协议。
- **测试**：repository/account_quality_*（8 个）、repository/account_quality_bps_test.go、service/account_quality*_test.go。
- **文档**：docs/account-quality-operations.md、docs/quality-bps-observation.md、docs/quality-ops-evidence/。

### E. 账号运维 / 通知（Account Ops）
- **来源路径**：`handler/admin/account_ops_handler.go`(+test)、`service/account_ops_balance_test.go`、`account_ops_batch_test.go`；前端 `views/admin/AccountOpsView.vue`(279 行)、`components/admin/operations/AccountOps*.vue`（AddChannelDialog、BatchRuleDialog、ChannelFields、DeliveryDetails、EmailDialog、GlobalSettings、GroupDialog、RuleDialog、RuleList、WebhookDialog 共 10 个 + 6 个 spec）、`api/admin/accountOps.ts`；i18n accountOps。
- **路由入口**（admin.go:828-844）：`/account-ops/auto-config` GET/PUT、`/auto-config/events` GET；`/account-ops/config` GET/PUT；`/notification-settings` PUT；`/webhooks/:id` PUT/DELETE、`/webhooks/:id/test` POST；`/rules/batch` PUT、`/rules/:id` PUT/DELETE；`/alerts` GET；`/balance-accounts`、`/threshold-accounts`、`/groups` GET、`/groups` PUT。
- **数据库**：249_account_ops_alerts.sql、266_account_ops_notifications.sql（扩展 kind/state CHECK，新增 details/deliveries/identity）、267_account_ops_threshold_episodes.sql、272_account_ops_groups.sql（主键改为 group_id+kind）。
- **运维要求**：邮件复用既有 SMTP；Webhook 支持企微/钉钉/飞书自动识别 + 签名密钥，地址与密钥加密保存、读取只回是否已保存。
- **测试**：`handler/admin/account_ops_handler_test.go`、`repository/account_ops_{batch,once,names}_integration_test.go`、`service/account_ops_{balance,batch}_test.go`、`api/__tests__/admin.accountOps{,.scoped}.spec.ts`、10 个组件 spec。
- **文档**：docs/account-ops-notifications.md。

### F. 凭证守护 + 凭证运营（TokenGuard V1 / V2）
- **来源路径**：前端 `views/admin/ops/TokenGuardView.vue`(439 行)、`TokenGuardV2View.vue`(645 行)+各自 spec、`api/admin/accountTokenGuard.ts`、`accountTokenGuardV2.ts`、`credentialEncryption.ts`、`components/account/CredentialEncryptionSetup.vue`、`OpenAITOTPDialog.vue`、`OpenAITwoFAImport.vue`；i18n tokenGuard/tokenGuardV2。
- **路由入口**（admin.go:846-867）：`/account-ops/token-guard/*`（status、config、run、run/start、jobs/:id、jobs/:id/cancel、events、accounts/:id/relogin、two-fa-login、two-fa-login/:id）与 `/account-ops/token-guard-v2/*`（encryption、encryption/initialize、accounts、rules、runtime、accounts/:id/switches、accounts CRUD、accounts/:id/probe、accounts/:id/relogin）。
- **数据库**：250_account_token_guard.sql、255_account_token_guard_v2.sql。
- **⚠ 源站特定默认值（不应照搬）**：docs/account-token-guard.md 写明代码内置参考测活/重登服务 `https://session.ameng2027.xyz`（含对应请求头与模型 gpt-6-astra），启用前须自行替换。
- **测试**：`api/admin/__tests__/accountTokenGuard.spec.ts`、`repository/account_token_guard*`（3 个）。
- **文档**：docs/account-token-guard.md、docs/account-token-guard-v2.md、docs/credential-encryption.md。
- **命名坑**：docs 明确 TokenGuardV2 不是 V1 的升级，而是独立的「凭证运营(Credential Operations)」，URL/API/DB/内部标识 `TokenGuardV2` 保留兼容。

### G. 优先调度（Priority Scheduling）
- **来源路径**：前端 `views/admin/PrioritySchedulingView.vue`(124 行)、`api/admin/priorityScheduling.ts`；i18n priorityScheduling；`components/account/AccountPriorityCell.vue`、`AccountCostMultiplier` 相关（AccountAutoBPSSection 同批）。
- **路由入口**（admin.go:825-827）：`GET|PUT /admin/priority-scheduling/config`、`GET /admin/priority-scheduling/snapshot`。
- **配置**：存 `priority_scheduling_v1`，**无数据库迁移**；默认关闭。
- **测试**：`handler/admin/priority_scheduling_compat_test.go`、`repository/priority_scheduling_integration_test.go`、`views/admin/__tests__/PrioritySchedulingView.spec.ts`、`components/account/__tests__/AccountPriorityCell.spec.ts`、`AccountsView.priorityColumn.spec.ts`、`AccountsView.schedulerScore.spec.ts`。
- **文档**：docs/priority-scheduling.md、docs/reviews/priority-scheduling-audit-20261006.md、docs/account-cost-multiplier 截图组。
- **注意**：来源声明本页**不提供** Teams/Pro/Plus/API 按类批量排序（改由账号成本倍率 `accounts.extra.cost_multiplier`，默认 0.1，无迁移）；旧配置 `teams` 字段被忽略。

### H. 自动配置（Auto Config + 历史）
- 来源路径：`views/admin/AutoConfigView.vue`、`api/admin/autoConfig.ts`、`components/admin/operations/AutoConfigHistory.vue`(+spec)；i18n autoConfig。路由（admin.go:828-830）：`/account-ops/auto-config` GET/PUT + `/auto-config/events`。数据库 259_account_auto_config_events.sql。文档 docs/account-auto-config.md、docs/auto-config-history.md（含 BPS 默认模板复用 settings.excel_bps，无迁移）。

### I. 受控实验（Controlled Experiments）
- 来源路径：`views/admin/ControlledExperimentsView.vue`、`api/admin/controlledExperiments.ts`；i18n controlledExperiments；`scripts/verify-controlled-experiments.ps1`。数据库 268_controlled_experiments.sql（不可变实验记录，状态机 draft…interrupted）。文档 docs/controlled-experiments.md。权限：仅完整管理员可读题库/报告/启动，观察员不可访问该组 API。

### J. 请求采集（Request Capture）
- 来源路径：`views/admin/RequestCaptureView.vue`、`api/admin/requestCaptures.ts`、`middleware/request_capture.go`(+test)、`requestcapture/response_observer.go`。数据库 251_request_captures.sql（**正文落 DATA_DIR/request-captures，不入 PG**）。路由 admin.go:91 `admin.Group("/request-captures")`；导航项由 `requestCaptureEnabled` 门控（SmartOpsNav.vue）。导出用 File System Access API（需 HTTPS 安全上下文）。文档 docs/request-capture.md、docs/ops-client-rejections.md。

### K. 鹈鹕测智（Pelican 分组测试 + 用户展示 + 开放 API）
- 来源路径：前端 `views/admin/PelicanTestsView.vue`(+spec)、`views/user/PelicanShowcaseView.vue`(415 行)+spec、`api/admin/pelicanTests.ts`、`api/pelicanShowcase.ts`、`components/account/Pelican*`；i18n pelicanTests/pelicanShowcase。后端 `handler/pelican_showcase_handler.go`、`pelican_showcase_public.go`、`handler/admin/pelican_group_tests_handler.go`。
- 路由入口：admin.go:902-912（`/pelican-showcase/settings` GET/PUT、`/pelican-showcase/items/:id` DELETE、`/pelican-group-tests` CRUD + `/:id/run`、`/pelican-group-test-results`、`/pelican-group-test-results/:id`）；`GET /admin/pelican-test-results`（admin.go:875）；`POST /admin/accounts/:id/pelican-test`（admin.go:448）；用户端 routes/user.go:161-166 `GET /pelican-showcase`、`GET /pelican-showcase/items/:id`。
- 数据库：243_pelican_scheduled_tests.sql、249_pelican_showcase_items.sql、253_pelican_group_tests.sql、261_pelican_group_test_costs.sql、263_strip_pelican_prompt_restriction.sql。
- 运维：成本按上游 token × 实际作答模型美元定价 × 账号 `cost_multiplier` 折算，不扣用户余额；开放 API 用当前面板站点 origin，**不是模型 API 地址**（docs/pelican-showcase-api.md）。前端路由 `/pelican-showcase`（用户）、`/admin/pelican-tests`（管理）。

### L. 网站工单（Support Tickets）
- **缺失证据**：本地无 `backend/internal/handler/support_ticket_handler.go`(201 行)、无用户/管理 SupportTicketsView.vue。
- 来源路径：`handler/support_ticket_handler.go`(+test)、前端 `views/user/SupportTicketsView.vue`(155 行)、`SupportTicketDetailView.vue`、`views/admin/SupportTicketsView.vue`、`SupportTicketDetailView.vue`、`api/supportTickets.ts`、共享模块 `features/support-tickets/supportTickets.ts`(+spec)、i18n supportTickets。
- 路由入口：用户端 routes/user.go:126-136（`/support-tickets` GET/POST、`/summary`、`/:id`、`/:id/messages`、`/:id/close`、`/:id/reopen`）；管理端 admin.go:528-536（`/support-tickets` GET、`/summary`、`/:id`、`/:id/messages` POST、`/:id/status` POST、`/:id` DELETE）。
- **开关契约**：`support_ticket_enabled`（dto/settings.go:359、496；admin/setting_handler_update.go:386、2332）＋ `support_ticket_config`；关闭时 service 返回 `SUPPORT_TICKET_DISABLED`（routes/user.go:126 注释）。前端路由守卫 `requiresSupportTickets` 读 `cachedPublicSettings.support_ticket_enabled !== true`（router/index.ts）。
- 数据库：268_support_tickets.sql（注释：与 Codex turn ticket 区分，故命名 support_ticket）。
- 测试：`handler/support_ticket_handler_test.go`、`repository/support_ticket_integration_test.go`、`views/{user,admin}/__tests__/SupportTicketsViews.spec.ts`、`SupportTicketsAdmin.spec.ts`、`features/support-tickets/__tests__/supportTickets.spec.ts`。

### M. 分组缓存测试工作台（纯前端，无后端新接口）
- **定性（与截图无关、代码实证）**：`frontend/src/api/groupCacheTest.ts` + `frontend/src/utils/groupCacheTest.ts` + `views/admin/GroupCacheTestView.vue`(226 行) + `api/__tests__/groupCacheTest.spec.ts` + `views/admin/__tests__/GroupCacheTestView.spec.ts`（499 行）。
- **机制**：用当前登录用户自己的分组 API Key，直接 `fetch(buildGatewayUrl(CACHE_ENDPOINTS[protocol]))` 打 `/v1/responses`、`/v1/chat/completions`、`/v1/messages` 三个正常网关端点，逐轮比对 usage 的 cache read/write 桶；账单核对通过 `listUsage`（`api/admin/usage.ts`）按 `request_id + api_key_id + user_id` 精确匹配，**不做时间/模型猜测**。
- **唯一后端依赖**：CORS 暴露头 `X-Client-Request-ID`（`backend/internal/server/middleware/cors.go:108` `Access-Control-Expose-Headers: ETag, Server-Timing, X-Client-Request-ID`；中间件 `middleware/client_request_id.go:14`；测试 `cors_test.go:111`）。
- **成本/风险**：每轮都是一次**真实计费**模型请求；默认 3 轮，最多 6 轮，失败即停、不自动重试；历史仅存浏览器本地（按用户隔离，最多 20 次），无服务端历史、无 DB 迁移、无定时测试。回滚前端即可移除。
- 文档：docs/group-cache-tests.md。

### N. 打票流水 / Codex 并行采集（Harvest Flow）
- 来源路径：`views/admin/HarvestFlowView.vue`、`api/admin/codexHarvest.ts`、`api/admin/astraGateway.ts`、`components/admin/operations/HarvestControls` 相关、i18n admin/harvestFlow；后端 `internal/mihomo/harvest_directed.go`、`harvest_directed_config.go`、`harvest_managed.go`。
- 路由入口（admin.go:404-410）：`GET /admin/accounts/codex-harvest-flow`、`/codex-harvest-controls` GET/PUT、`/codex-harvest-nodes` GET、`/codex-harvest-nodes/reset` POST、`PUT /admin/accounts/:id/codex-skip-harvest`、`POST /admin/accounts/:id/manual-harvest`（新增 `collect_lanes` 2–32 路并行）；`/admin/astra-gateway` 重定向到 `/admin/harvest-flow`（router/index.ts）。
- 数据库：239_codex_harvest_node_learning.sql、240_codex_harvest_flow_events.sql、244_astra_gateway_history.sql、249_astra_scheduling_states.sql、265_new_api_site_authorizations.sql。
- 运维硬依赖：并行采集**必须用内置托管 Mihomo**（采票代理 `http://127.0.0.1:3101`），外部代理明确报错；内核新增回环监听 `127.0.0.1:17893–17924`，升级时须确认端口未被占用（docs/codex-parallel-harvest.md）。
- 文档：docs/codex-parallel-harvest.md、docs/astra-*.md、docs/codex-780-native-mint.md、docs/codex-ticket-pinning.md、docs/harvest-gateway-borrow.md、docs/CODEX_HARVEST_SCOPE_CN.md、docs/astra-ticket-acquisition.md。

### O. 观察员（Observer）与新增用户侧页面/API
- `views/user/UsageEntryView.vue`、`api/observerUsage.ts`、`middleware/observer_routes.go`、`handler/usage_observer.go`；数据库 252_user_observer_groups.sql（`users.observer_group_ids`）；`admin.go:38 admin.Use(h.Admin.Account.AuthorizeObserver)`。测试 `api/__tests__/observerUsage.spec.ts`、`views/user/__tests__/UsageEntryView.spec.ts`。
- 用户侧新增/改动页面还含：KeysView(+271 行)、PaymentView(+157)、CustomPageView、ChannelStatusV3、SupportTickets*、PelicanShowcase。

## 3. 跨层契约（移植时必须一并改，否则静默失效）
1. **导航聚合**：来源新增 `frontend/src/components/admin/operations/SmartOpsNav.vue`（+spec），把「智能运维」子页聚合为一组：`/admin/auto-config`、`/admin/priority-scheduling`、`/admin/account-quality`、`/admin/controlled-experiments`、`/admin/account-ops`、`/admin/token-guard`、`/admin/token-guard-v2`、`/admin/pelican-tests`、`/admin/group-cache-tests`、`/admin/request-captures`（受 `requestCaptureEnabled` 门控）、`/admin/harvest-flow`。本地 AppSidebar.vue 现有的是 `/admin/account-health`(820) 与 `/admin/intelligent-tests`(821)，与这套完全不同。
2. **功能开关注册流程**：`frontend/src/utils/featureFlags.ts` 头部注释给出 11 步登记清单（domain_constants.go → settings_view.go → setting_service.go 含 `PublicSettingsInjectionPayload` → dto/settings.go → setting_handler.go → admin/setting_handler.go 审计 diff → types/index.ts → api/admin/settings.ts → featureFlags.ts → SettingsView.vue → AppSidebar.vue），并有漂移测试 `backend/internal/handler/dto/public_settings_injection_schema_test.go` 强制后端注入字段。新增 `support_ticket_enabled`、`request_capture_enabled` 等必须走完这条链。
3. **渠道监控模式枚举**：`ChannelMonitorMode` 需从 `'v1' | 'v2'` 扩到含 `'v3'`（本地 featureFlags.ts:181），否则 V3 路由与守卫无法生效。
4. **i18n 双语**：每个新页面在 `frontend/src/i18n/locales/{zh,en}/` 下都有一对同名文件（accountOps.ts、channelMonitorV3.ts、autoConfig.ts、controlledExperiments.ts、groupCacheTest.ts、harvestFlow.ts、pelicanTests.ts、priorityScheduling.ts、requestCapture.ts、supportTickets.ts、tokenGuard.ts、tokenGuardV2.ts）。缺一个即页面文案空洞。
5. **路由守卫**：`requiresSupportTickets`、`requiresRiskControl`、`requiresSubscription`、`requiresPayment` 组合守卫在 router/index.ts 中与 `publicSettingsLoaded` 联判（router/index.ts:…），新增 feature 必须登记。
6. **迁移谱系重叠**：本地 SQL 最大编号到 242，来源到 276；237、239、240、241、242 等编号下双方都有新增文件。当前迁移器按完整文件名识别已执行记录并校验内容 SHA256，编号相同而文件名不同并不天然冲突。本轮直接对比确认 5 个共有旧 SQL 内容不同，双方还有 5 个独立新增但同路径的 SQL，须逐条核对内容与依赖顺序。保留当前已发布 SQL 及校验兼容规则，通过新的追加迁移接入需要的字段/表；不以编号重叠为由统一重编号，不让来源较窄的平台约束移除 Kiro/Adobe。
7. **CORS 暴露头**：`X-Client-Request-ID`（cors.go:108）是分组缓存测试工作台的唯一后端依赖；跨域部署若去掉该头，工作台只能显示「无关联 ID」。

## 4. 用户仓库不应照搬的源站内容（客观清单，无需警告，仅不可复用）
- **README 与站点品牌**：README.md:14「tosky.io 维护的 Sub2API 独立分支」、README.md:18 平台/文档/Release 链接（tosky.io、github.com/ranxi2001/sub2api/releases）、README.md:24-43 QQ 群号 1004036018 与赞赏二维码、README.md:141+ 大量 `avatars.githubusercontent.com` 贡献者头像。
- **安装/镜像发布规则**：`docs/新人入门.md:17-26` 用 `curl https://raw.githubusercontent.com/ranxi2001/sub2api/v2.8.13/deploy/install.sh` 与 `docker pull ghcr.io/ranxi2001/sub2api:2.8.13`；`deploy/docker-compose{,.local,.standalone}.yml` 镜像 `ghcr.io/ranxi2001/sub2api:latest`（本地现为 `${SUB2API_IMAGE:-ghcr.io/nianzs/sub2api:latest}`）；`deploy/kubernetes/{gateway,kustomization}.yaml`、`deploy/kubernetes/README.md`、`deploy/upgrade-k3s.py`（`IMAGE='ghcr.io/'+REPO`、ghcr.io token 拉取）全部绑定 `ranxi2001` 与 `tosky-canary` namespace / `tosky-nerd` Deployment。
- **内置第三方默认服务**：docs/account-token-guard.md 参考测活/重登服务 `https://session.ameng2027.xyz`（含请求头与模型 gpt-6-astra）。
- **不入库内容**：`deploy/tests/`、`deploy/copilot-sdk/`、`tools/reauth-runtime/`、`prism-adapter/` 等为源站自建的运维/实验脚手架，若干含源站运行参数，按需取舍而非整体搬运。
- 本地自有、来源没有的部署件同样要保留：`deploy/online/{compose.yml,nginx.conf,init_env.py,.env.example}`、`deploy/Dockerfile.fast`、`deploy/build_image_fast.sh`。

## 5. 建议实施顺序（按「依赖少→依赖多、可独立回滚」排序）
1. **P0 实施方案确认**：推荐保留本地账号探测（intelligent-tests/account-health），另增来源 account-quality/account-ops 的独立入口；监控模式在保留 V1/V2 的基础上扩展 V3。此为待确认方案，不是已执行变更。
2. **P1 纯前端、零后端、零迁移**：M 分组缓存测试工作台（只需确认 CORS 暴露头 + 前端 key 校验）。可独立回滚。
3. **P1 低耦合后端**：G 优先调度（存 settings、无迁移）、H 自动配置、E 账号运维（需 249/266/267/272 四迁移）、L 网站工单（需 featureFlags 全链 + 268 迁移 + 路由守卫）。
4. **P2 依赖既有基础设施**：C 渠道监控 V3（依赖 V2 被动聚合，需 264/273 + V2 candy 254/275/276）、K 鹈鹕（需 243/249/253/261/263）、J 请求采集（需 251 + 中间件）、I 受控实验（需 268 + 管理员权限收口）、O 观察员（需 252 + 中间件）。
5. **P3 重运维依赖**：D 质量运维（降智判定 + BPS 规则共存，需 247/248/254/258/260）、F 凭证守护/凭证运营（需 250/255，且要先替换源站默认外部服务）。
6. **P4 最重**：A Mihomo 全栈 + N 打票流水/并行采集（A 是 N 的硬前置：并行采集强制内置托管 Mihomo；两者还共同依赖 B 的 BPS 内核与 `proxies.mihomo_node_id`）。
7. **P5**：B Excel/BPS 路由与 tools/ 工具链（协议后端实现由另一代理负责，本条只落前端/文档/运维面）。
8. 全程穿插：逐条核对迁移完整文件名、校验和与依赖顺序（§3.6），保留已发布 SQL，并完成 featureFlags 全链登记及中英文文案。

## 6. 未完成/无法确证的部分
- 支付/计费/并发/协议后端实现按要求未审计（属另一代理）。
- 来源 `frontend/src/features/channel-monitor-v3/`、`features/support-tickets/` 等共享模块的内部组件文件清单只做了抽样确认，未逐一读取。
- 来源 docs 的截图（docs/screenshots/**、docs/images/**）与 `docs/quality-ops-evidence/*.jpg` 未采用为证据；其 UI 断言均以 .vue/.ts 源码为准。
- 来源 `docs/新人入门*.md` 只读取前 30 行以定位源站特定内容，未全文通读。
- 未验证任何来源功能的运行时行为（无构建/无启动/无测试执行），本报告全部为静态代码与文件取证。

# ranxi2001/sub2api production 后端功能吸收审计（只读，Grill 阶段）

基线：HEAD=1f08460552fbac66106baa97d0e3841f93057d58（F:\sub2api，mine/main）
来源：refs/remotes/ranxi-review/production=1d2ed630e292eaaac95454c1786bf3b7e5ed0828
共同祖先：efe9aab1e4ec89a42ba45e8dac20e882c5409a6a

## 0. 核心事实

- merge-base(HEAD, prod)=efe9aab；HEAD 自分叉后 1191 提交、prod 1426 提交，双方互不包含。prod 不是 HEAD 的超集，是另一条分叉线。
- base..prod：3002 文件、+328000/-11377。backend 侧 A=1131 / M=696。
- HEAD 4760 文件，prod 5956 文件；backend/internal 非测试「仅 prod 有」358 文件。
- 迁移：HEAD 340 个（最大编号 242），prod 361 个（最大 276）。仅 prod 独有 47 个迁移文件，仅 HEAD 独有 20 个（含全部 kiro / adobe 迁移）。
- 测试文件：prod 独有 514，HEAD 独有 161。
- 已字节相同（无需迁移）：backend/internal/service/openai_codex_pat_service.go（blob fa89aa96）。
- 平台清单：HEAD 15 个（含 Kiro、Adobe），prod 13 个（无 Kiro、无 Adobe）。
- 迁移执行器 `repository.ApplyMigrations`（HEAD:130 / prod:114）按**文件名**跳过已应用迁移 + checksum 校验，编号重复不会互相覆盖，但 checksum 兼容表会丢（见冲突 C1/C3）。
- ranxi-review/main(e1317a4fd) 相对共同祖先 efe9aab 只有 2 次 VERSION 提交，文件差异只有 `backend/cmd/server/VERSION`。main 与 production 之间则有 3002 个文件差异；两种比较对象不同，不构成矛盾。功能吸收以 production 的锁定提交为准。

## 1. 功能组（按建议迁入顺序）

### G1 Codex ticket 采集/复用核心引擎（最高价值 + 最高冲突）
来源：`backend/internal/service/openai_codex_ticket.go`（prod 约 2100 行 vs HEAD 约 900 行）；
新增 `openai_codex_ticket_egress.go`（出口身份绑定、剥离 `x-codex-beta-features`/`X-OpenAI-Internal-Codex-Responses-Lite`/`prompt_cache_key`/`client_metadata`/`device_id`）、
`openai_codex_ticket_feedback.go`（`observeCodexTicketResponse`/`codexResponseMismatches` 响应失配失效）、
`openai_codex_ticket_manual.go`（单号定向打票）、`openai_ticket_candidates.go`、`openai_codex_probe_response.go`/`openai_codex_probe_status.go`、`openai_codex_state_probe.go`。
关键差异函数：`ticketIdentity/ticketIdentityMatches`、`openAICodexTicketIsTeamPlan/ExpectedBlocks/ExpectedLength`、`pickHarvestIPPoolExit*`、`openAICodexTicketShouldYieldSticky*`、`holdCodexTicketChat`、`openAICodexSkipHarvest`。
HEAD 重叠：`service/openai_codex_ticket.go`（票库/主票+standby/身份绑定/fail-closed/probe/harvester 已有）、`openai_codex_pat_service.go`（**已与 prod 字节相同**）、`openai_codex_ticket_lifecycle_test.go`。
契约：`OpenAICodexTicketStatuses(account,cfg[,targetLength],now)`、`applyOpenAICodexTicket(..., transport ...string)`、`isOpenAICodexTicketAccount(account[,upstreamModels...])`、`storeOpenAICodexTicket` 返回值（HEAD void / prod error）**均不兼容**。
必须保留：HEAD 的 `openai_codex_ticket_default_length` + `openai_codex_ticket_plan_lengths`（按 plan type 定长度）——prod **已删除该机制**（改用 team-plan expected blocks/length）。
测试：HEAD `openai_codex_ticket_plan_length_test.go`、`openai_codex_ticket_lifecycle_test.go`；prod 另有 8 个（cookie/scheduler/feedback/manual/lifecycle/candidates）。
顺序：G1 最后落地（需 G2/G3/G11 就位）。

### G2 Codex Harvest 采票流水线（prod 全新）
来源：`service/codex_harvest_{flow,hunt,nodes,parallel,probe,coverage,controls,attempt,status,edge,errors,780,780_ws}.go`；
`setting_codex_harvest_wake.go`（进程内 wake channel，`NotifyCodexHarvest`）、`setting_codex_harvest_scope.go`（`openai_codex_ticket_harvest_scope`：all/selected/selected_accounts + schedulable_only/prioritize_schedulable）。
迁移：`239_codex_harvest_node_learning.sql`（codex_harvest_nodes + learning_epoch）、`240_codex_harvest_flow_events.sql`（脱敏流水事件）。
依赖：`internal/mihomo`（边车组 `CODEX-ROTATE`）、`mihomo/harvest_directed*.go`；入口 `openai_gateway_service.go:666 svc.StartOpenAICodexTicketHarvester()`。
HEAD 重叠：仅有 `StartOpenAICodexTicketHarvester`（HEAD 版 518 行处）与 `openAICodexTicketHarvestProxyURL`，无 hunt/节点学习/流水/预算。
保留：HEAD harvester 的 fail-closed、proxy URL 校验与脱敏（`ValidateOpenAICodexTicketHarvestProxyURL`/`MaskProxyURL`）。
测试：prod `codex_harvest_*` 13 个测试 + `admin_codex_harvest_wake_test.go`。

### G3 Codex Harvest / ticket 管理面与设置（公共契约 |
路由（prod `server/routes/admin.go`）：
401-403 `/accounts/astra-gateway/{status,history,test}`；404-410 `/accounts/codex-harvest-{flow,controls,nodes,·/reset}`、`/:id/codex-skip-harvest`、`/:id/manual-harvest`；657-658 settings `/astra-routing`；825-827 `/priority-scheduling/{config,snapshot}`。
设置 DTO（`handler/dto/settings.go`）：prod 新增 `openai_codex_ticket_harvest_scope|strict_response|candy_observation|strategy|static_proxy_url`；HEAD 独有 `openai_codex_ticket_default_length` + `plan_lengths`（prod 丢弃）。共同保留：enabled / fail_closed / harvest_proxy_url(+configured)。
新增 setting key：`openai_codex_ticket_strategy`(standby|fixed)、`openai_codex_ticket_strict_response`、`openai_codex_ticket_candy_observation`（缺省关闭，Beta 观测开关）、`openai_codex_ticket_static_proxy_url`。
依赖：admin 写入后触发 `NotifyCodexHarvest()`（`service/admin_account.go:1501,1519`）。

### G4 Astra 门票复用（快速复用/借票）+ Astra Gateway（Beta）
来源：`service/astra_ticket_lifecycle.go`（`UnifiedAstraTicketsEnabled`、`PrepareStoredAstraSources`、`tryBorrowStoredAstraTicket`、`acquireStoredAstraTarget`、`VerifyStoredAstraTarget`、`ApplyStoredAstraTarget`、`StoredAstraSnapshot`）、`astra_route_failover.go`、`astra_gateway_{questions,runtime,history}.go`、`handler/admin/account_astra_gateway.go`、`repository/astra_gateway_{history,observations}.go`、`mihomo/astra.go`、`config/astra_routing.go|astra_automatic.go`。
文档契约：`docs/astra-ticket-acquisition.md`（明确：**无数据库迁移**；票据 JSON 仅增可选 `acquisition`(reuse|self) 与 `source_account_id`；SSE 才建目标票，WS 需已有同协议票；一次快速复用最多 2 次短请求；旧二进制忽略新字段会退回旧路由覆盖行为）；`docs/changes/targeted-ticket-probe.md`。
迁移：`244_astra_gateway_history.sql`、`249_astra_scheduling_states.sql`。
HEAD 重叠：仅 GPT-6「Astra Pro」推理模式（`service/openai_astra_pro_mode_test.go`、`openai_codex_astra_image_test.go`、`pkg/openai/instructions_gpt6_astra.txt`），**无网关/借票/调度**；HEAD 无 `CodexGatewayPinConfig`/`CodexWSAnchorConfig`。
保留：Astra Pro 推理模式与 `needsOpenAICodexAstraVersion` 行为。

### G5 Astra 调度与自动配置
来源：`service/astra_{account_scheduling,automatic_setup,scheduled_candy,scheduling_action,scheduling_history}.go`、`repository/astra_{rotation,rotation_cache,node_store,scheduling_action,scheduling_groups,routing_upstream,target_validation,ip_affinity,readiness,observation_only,concurrent_admission}*.go`、`setting_astra_automatic.go`。调度模式 account|model|groups（≤100 组）；Cookie pool（来源→目标账号借票，`RotateNodes⇒IPAffinity`）+ WS 锚点。
测试：repo/service 下 astra_* 26 个测试（含 integration/postgres）。
依赖：G4 + G11（mihomo 节点）+ 分组表。

### G6 API Key 级并发等待队列（prod 全新，含 DB 变更）
来源：`service/api_key_queue.go`（`APIKeyQueuePolicy{MaxWaiting,Timeout}`，Lua 状态机 8 个返回码 `Acquired/Waiting/Timeout/Aborted/TicketLost/PolicyChanged/QueueFull/LimitReached`；ENTER/POLL 两阶段 + `AbortAPIKeyQueueAttempt` fence + `GetAPIKeyQueueStats`）、`api_key_slot_lease.go`、`api_key_slot_lifecycle.go`；
`server/middleware/api_key_admission_owner.go`（`nextWithAPIKeyAdmissionOwner`：WS 也装 revalidator）、`service/WithAPIKeyAdmissionOwner|WithAPIKeyQueueAuthRevalidator|WithAPIKeyQueueImagePermission`；
handler：`api_key_queue_test.go`/`api_key_queue_ws_test.go`/`gateway_api_key_admission_test.go`。
配置：`config.APIKeyQueueConfig`（`gateway.api_key_queue.max_waiting` 默认 5、`timeout_seconds` 默认 30；env `GATEWAY_API_KEY_QUEUE_MAX_WAITING`/`GATEWAY_API_KEY_QUEUE_TIMEOUT_SECONDS`；非法值阻止启动）。
错误码（`handler/concurrency_error_response.go:67-69`）：新增 `api_key_queue_full`、`api_key_queue_timeout`，保留 `gateway_concurrency_limit`。
迁移：`237_add_api_key_concurrency_limit.sql`（`api_keys.concurrency_limit BIGINT DEFAULT 0`，ent/schema/api_key.go 同步）。
HEAD 重叠：用户级等待队列（`handler/gateway_helper.go:158 WaitQueueFullError` → `gateway_queue_full`、`:296`）、`service/concurrency_service.go`、`repository/concurrency_cache.go`（账号/分组槽位 + Redis Lua）、`concurrency_error_response.go`（HEAD 无 `api_key_queue_*`）。**HEAD 无 api_keys.concurrency_limit**。
保留：`gateway_queue_full`、账号/分组级并发语义与 `concurrency_cache_*` 测试。

### G7 DeepSeek / Responses-Lite / Codex 协议适配
来源新增 `service/openai_deepseek_compact.go`（`buildDeepSeekCompactChatBody`：丢弃 compaction_trigger、追加摘要 user 消息、强制 stream=false；`compactSummaryTextFromResponses`）。当前已有 `openai_compact_stream_bridge.go`，来源对此是增量修改（直接对比为 +73/-4），应合并到已有桥接；同时核对 Responses-Lite 的 DeepSeek 识别与 chat fallback 路由（`openai_responses_deepseek_lite_test.go`/`..._aggregator_test.go`）。
HEAD 重叠（已具备）：`openai_gateway_responses_chat_fallback.go`、`openai_gateway_deepseek_chat_reasoning_test.go`、`openai_gateway_deepseek_input_image_test.go`、`openai_codex_model_metadata.go`（use_responses_lite 8 处）、`openai_responses_lite_tools.go`、`pkg/apicompat/*`、`model_protocol_catalog.go`、`provider_profile.go`。→ 属**增量补齐**，冲突低。
保留：HEAD 的 lite 路由、namespace/custom tool、DeepSeek pricing 测试。

### G8 Excel / Basispoints(BPS) 上游适配（prod 全新，大组）
来源：`service/openai_excel_bps*.go`（14+ 文件：images/附件/取消/加密/model access+cooldown/transport 恢复）、`service/basispoints/`（34 文件：catalog/catalog_cache/attachments/agent_assignment/结构化输出校验）、`pkg`+`account_excel_bps*`、`repository/account_repo_excel_bps*`、`server/middleware/excel_bps_image_admission.go`。
迁移：`254_quality_bps_coexist.sql`、`271_openai_excel_dual_oauth.sql`；文档 `docs/features/bps-{credential-diagnostics,model-permission-cooldown,redis-state}.md`、`excel-bps-refresh-recovery.md`、`docs/quality-ops-evidence/README.md`。
HEAD：完全无（0 命中 `excel_bps|ExcelBPS`）。README 宣称「开启 BPS 且账号未因 403 关闭时强制走 BPS，不再静默回退 Codex」——源码存在对应分支，但在线成功率/费用收益**未验证**（文档自述未测量）。

### G9 凭证守护与 OAuth 重登运行时（账号故障恢复核心）
来源：`service/account_token_guard.go`（`account_token_guard_config_v1`；探活 ok/auth/transient → 邮箱+密码+2FA 重登写回凭据并恢复调度；错误态自愈）、`account_token_guard_v2.go`（规则/账号级开关/`RunDue`/`ProbeNow`/`ReloginNow`，`WorkerStatus` 接 `reauthruntime`）、`account_token_guard_v2_controls.go`、`repository/account_token_guard{,_v2,_recovery,_v2_controls}.go`、`handler/admin/account_token_guard{,_v2}_handler.go`；
`service/openai_oauth_reauth_*`、`internal/reauthruntime/`（下载并监管与 release 匹配的登录运行时，进程隔离 process_linux/other）、`tools/reauth-runtime/`、`tools/test_openai_oauth_reauth_worker.py`、`tools/test_openai_totp_rotation.py`。
路由：846-867（`/admin/account-ops/token-guard/*`、`/token-guard-v2/*`、two-fa-login、relogin、encryption initialize）、414-421（账号级 openai-reauth / totp-rotation / totp-export）、498-510（`/v1/internal/openai-reauth/*` worker claim/progress/callback/credentials/fail，`OPENAI_REAUTH_WORKER_TOKEN` 鉴权 + 任务归属校验）。
迁移：`250_account_token_guard.sql`、`255_..._v2.sql`、`254/256/257/262_openai_oauth_reauth*.sql`、`269_openai_totp_rotation.sql`。
HEAD：完全无。保留：HEAD 现有 token_refresh_service / token_cache_invalidator / 账号错误态与冷却逻辑（不得被 guard 覆盖）。
风险：内置进程监管 + 凭据加密（`CredentialEncryption`/initialize）；TOTP 轮换与导出属敏感面。

### G10 账号质量运维 account_ops（告警/通知/分组/阈值）
来源：`service/account_ops{,_balance,_config,_group,_group_manual,_notification,_once,_robot,_scoped,_signal,_webhook}.go`、`service/account_quality{,_bps}.go`、`repository/account_ops*.go`、`repository/account_quality*.go`、`handler/admin/account_ops_handler.go`；
路由 828-844、868-875（`/admin/account-ops/{auto-config,config,notification-settings,webhooks,rules,alerts,balance-accounts,threshold-accounts,groups}`、`/account-quality-{results,plans,templates}`）。
迁移：247/248/249/258/260/266/267/272（quality ops、judgment、alerts、observation scope、rule templates、notifications、threshold episodes、groups）、`259_account_auto_config_events.sql`。
依赖：`pkg/qualityqueue`（Redis）、`service/oauth_auto_config{,_bps,_models,_quality}.go`、ScheduledTest 框架。
HEAD 重叠：`ops_concurrency.go`、`ops_cleanup_*`、`usage_cleanup*`、channel monitor 质量结果（部分）；**无 account_ops/quality**。

### G11 mihomo 节点与静态代理绑定（prod 全新包，58 文件）
来源：`internal/mihomo/{manager,process_linux,process_other,sources,dynamic_subscription,subscription_download*,collection,node_probe,controller_status,lease,static_bindings,astra,harvest_directed*,harvest_managed,country,node_names,migrate}.go`、`mihomo/bps*.go`（19000+ 端口会话池、健康/质量/竞速/热池）。
路由 761-766：`/admin/system/mihomo`、`/mihomo/download-mode`、`/mihomo/nodes/:name/{test,quality-check}`、`/mihomo/nodes/static-proxies`。迁移 `274_mihomo_static_proxy_bindings.sql`；文档 `docs/design/mihomo-backend-api.md`、`docs/design/ip-management.md`。
依赖：外部 mihomo 二进制进程（linux/other 双实现）、订阅下载。G2/G5 的出口选择依赖本包。

### G12 请求抓取与重放取证（prod 全新 16 文件）
来源：`internal/requestcapture/{manager,session,store,filter,export,websocket,response_observer,http_diagnostics,json_validator}.go`；路由 90-101 `/admin/request-captures*`（Gate 中间件、按 task 列请求/详情/内容/导出）。迁移 `251_request_captures.sql`。HEAD：无。

### G13 Serverless 多 Pod 路由（prod 全新）
来源：`internal/serverless/{manager,routing,config,geo}.go`（Redis `serverless:v1:` 前缀、HMAC、pod 路由）；路由 42-44 `/admin/serverless`、`/serverless/pods/:id/probe`；`server/routes/gateway.go:38-48` 在 apiKey 鉴权后按 key.ID+可信 IP 路由/图片路由。文档 `docs/serverless-routing.md`。HEAD：无。

### G14 渠道监控 v3 / candy 观测（Beta 糖果）
来源：`service/channel_monitor_v3.go`、`channel_monitor_v2_candy.go`、`candy_answer.go`、`astra_scheduled_candy.go`、`repository/channel_monitor_v2_candy.go`；迁移 `254_channel_monitor_v2_candy.sql`、`264_channel_monitor_v3.sql`、`273_channel_monitor_v3_idle_probe.sql`、`275/276_candy_multi_model/history_min.sql`。HEAD 重叠：channel-monitor v1/v2 路由（HEAD admin.go:882/895/935、user.go:146/153）→ v3/idle-probe/candy 为增量。
Beta 开关：`openai_codex_ticket_candy_observation`（默认关，缺省/异常即关闭）。

### G15 其他有源码证据的中等组（各自独立，可后置）
- 受控实验：`service/controlled_experiment{,_gateway,_grader,_report,_service,_sql,_transport}.go`、`controlled_experiment_suite.json`、`handler/admin/controlled_experiment_handler.go`、`repository/controlled_experiment_repo.go`、路由 891-899、迁移 268。
- Pelican 测试/展示：`service/pelican_{group_tests,scheduled,showcase,test_service,test_usage}.go`、`handler/pelican_showcase*.go`、路由 448、875、903-905、迁移 243/249/253/261/263 + `docs/pelican-scheduled-tests.md`。
- 新站点授权 new_api：`service/new_api_{authorization,upstream}.go`、`handler/admin/account_new_api_authorization.go`、迁移 265。
- 分组策略：组模型白名单（HEAD 有 235/236）+ prod `244_account_group_allowed_models`、`245_user_group_denied_models`、`246_group_stream_only`、`252_user_observer_groups`；路由 383-384 `user-denied-models`。
- 支持工单：`service/support_ticket{,_config}.go`、`repository/support_ticket_repo.go`、`handler/support_ticket_handler.go`、路由 529 `/admin/support-tickets`、迁移 268（同名不同文件）。
- 优先级调度：`service/priority_scheduling.go`（`priority_scheduling_v1`）、路由 825-827、`handler/admin/priority_scheduling_compat_test.go`。
- 工具包：`pkg/{prismbridge,upstreamroute,requesttiming,qualityqueue}`、`util/transportdiag`、`repository/http_upstream`（prod 独有）。

## 2. 最重要的冲突/依赖
- C1 平台与迁移谱系：prod 无 Kiro/Adobe（platforms.go 13 vs 15）；prod 缺 HEAD 的 13 个 fork 迁移（135/145/151/152/153×2/192/227/229×2/230/238×2/239×2/240/242）；prod `migrations_runner.go` **丢了 5 条 checksum 兼容规则**（145_allow_kiro_user_platform_quotas、157_user_platform_quotas_add_grok、224_user_platform_quotas_add_cn_providers、237_add_minimax_platform、238_opencode_go_platform），并把 `242_drop_platform_check_constraints.sql` 重编号为 `270_...`（内容相同、DROP IF EXISTS 幂等）。整文件采纳 prod 会回退这些能力/校验。
- C2 Codex ticket 契约不兼容（G1 列表），来源移除了当前 plan_length 机制。迁入时保留当前配置与行为兼容，适配新增接口和回归测试；不以删除已有测试消除差异。
- C3 迁移同号不同文件：237/239/240/241/242 双侧各自新增；runner 按文件名跳过，新增文件可安全应用，但**必须保留 HEAD 的 checksum 兼容表**（C1）。
- C4 并发错误码/语义双轨：HEAD `gateway_queue_full`+`gateway_concurrency_limit`（用户级）/ prod 增 `api_key_queue_full`+`api_key_queue_timeout`（Key 级队列）+ 新列 `api_keys.concurrency_limit`，两套需共存，且 Key 队列新增启动期 env 校验（非法值阻止启动）。
- C5 引入顺序依赖：harvest(G2) → mihomo(G11) 边车 `CODEX-ROTATE` + astra 票库(G4/G5) + settings(G3)；(G5) 还依赖 CodexGatewayPinConfig/CodexWSAnchorConfig（HEAD 无，需先引入 `config/astra_routing.go` 的 pin/anchor 类型）。
- C6 契约面清单：HEAD 独有 161 个测试（kiro/adobe/prompt_rules/intelligent_tests/astra_pro/concurrency_cache 等）必须保留不被 prod 版本覆盖；PAT 服务字节相同无需处理。

## 3. 命令与统计（可复现）
```
git rev-parse HEAD; git merge-base HEAD refs/remotes/ranxi-review/production
git log --oneline --merges HEAD..refs/remotes/ranxi-review/production | Select-Object -First 25
git ls-tree -r --name-only <ref>  # → 写入 $env:PI_SCRATCH_DIR\{prod,head}_files.txt
Compare-Object prod_files head_files   # 逐目录/迁移/测试集合差
git grep -n -E '<pattern>' <ref> -- 'backend/...'
git show <ref>:backend/internal/service/openai_codex_ticket.go | Select-String '^func '
git rev-parse HEAD:<path> ; git rev-parse refs/.../production:<path>   # blob 同一性
```
统计：prod 独有迁移 47 / HEAD 独有迁移 20；prod 独有测试 514 / HEAD 独有 161；prod-only 非测试 backend/internal 文件 358；prod 平台 13 / HEAD 15；HEAD 最大迁移 242，prod 276。

## 4. 未完成/未知
- 未逐行审查 `backend/ent/*` 生成代码（任务排除），实体字段级差异只在 schema/api_key.go、schema/usage_cleanup_task.go 级别确认（`api_keys.concurrency_limit`、`usage_cleanup_tasks` 属 prod 新增）。
- 未编译、未运行任何测试（只读）；prod 独有代码的可编译性/测试通过性未验证。
- main 比较性质已由主代理重新核实：相对共同祖先仅两次 VERSION 提交，与 production 的差异是另一比较维度，详见 §0。
- 未测线上行为：harvest 成功率、快速复用费用节省、Astra 780/240s 票上限、BPS 强制路由收益，均只有文档/离线 mock 证据（文档自述未测量）。
- 排除范围：frontend、deploy/ops、docs 渲染、1426 提交逐条。

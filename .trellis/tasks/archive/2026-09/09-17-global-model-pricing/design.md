# 全局模型价格技术设计

## Architecture

数据流：

`Admin UI -> Admin API -> GlobalModelPricingService -> Repository -> PostgreSQL`

计费流：

`Gateway usage -> ModelPricingResolver -> Global snapshot -> Group -> Channel -> LiteLLM -> Fallback -> Billing`

缓存流：

`Admin write -> local snapshot invalidate -> Redis publish -> peer snapshot invalidate -> lazy reload`

## Database and Repository

- 新增 `238_global_model_pricing.sql`，表结构基于来源实现，文件名按当前仓库最大迁移 `237` 顺延。
- 使用独立 repository 接口封装 CRUD。鉴于表很小且来源实现已稳定使用直接 SQL，本次不增加 Ent schema/codegen；后续若全局价格成为其他实体的关系目标，再评估迁移到 Ent。
- 唯一索引落在规范化后的 `model_pattern`。服务层写入前复用渠道模型名规范化规则并保留尾部通配符，数据库唯一约束作为并发最终防线。
- 迁移增加 billing mode、非负价格、必填价格组合等 CHECK 约束；服务层另外拒绝 NaN/Infinity，避免只依赖数据库浮点比较。
- repository 将 PostgreSQL 唯一约束转换为领域级 duplicate error，handler 映射为 HTTP 409。

## Service and Matching

- `GlobalModelPricingService` 持有 repository、进程内快照、加载时间和可选 Pub/Sub。
- `Match` 先匹配规范化请求模型，再对 OpenAI/Codex 已知变体使用基名重试。
- 匹配规则：精确优先；通配仅允许尾部 `*`；通配取最长前缀。
- 快照行转换为 `ChannelModelPricing` 时深拷贝价格指针，避免下游原地修改污染共享快照。
- CRUD 成功后先清本地缓存，再发布失效通知。订阅端只清缓存，不反向发布，避免通知环。
- 保留短 TTL；数据库临时失败时返回旧快照。首次加载失败且没有旧快照时返回未命中。

## Resolver Integration

- 新增 `PricingSourceGlobal`。
- 保留 `NewModelPricingResolver` 供旧单测和局部构造使用；生产 wire 使用 `NewModelPricingResolverWithGlobal`。
- `Resolve` 在 group 之前检查 global。
- resolver 先计算旧链路的完整有效结果，再应用 global：token 模式在旧结果上覆盖必填 input/output 及可选 cache 字段；非 token 模式直接替换计费模式和按次价格。
- global 查询不依赖 `Group` 是否存在；group/channel 阶段继续按现有条件执行，修复无分组 API Key 绕过全局价格的问题。
- 非 token 模式构造现有按次定价结果，继续使用统一按次结算函数。
- 搜索并更新所有基于 `PricingSourceGroup`/`PricingSourceChannel` 判断的计费分支，将 global 纳入适用条件，而不是只改 resolver。

## API Contract

- `GET /api/v1/admin/global-pricing`
- `POST /api/v1/admin/global-pricing`
- `PUT /api/v1/admin/global-pricing/:id`
- `DELETE /api/v1/admin/global-pricing/:id`
- `POST /api/v1/admin/global-pricing/:id/enable`

写入字段：

- `model_pattern: string`
- `billing_mode: token | per_request | image | video`
- token 模式：必填 `input_price`、`output_price`；可选 `cache_write_price`、`cache_write_1h_price`、`cache_read_price`
- 非 token 模式：必填 `per_request_price`
- `enabled: boolean`

创建默认启用；更新价格不接收或不修改 `enabled`；启停接口使用单条 `UPDATE ... RETURNING` 返回新行，消除 GET-UPDATE 和 UPDATE-GET 窗口。

金额存储单位与现有渠道定价一致：token/cache 为美元/token，按次为美元/request。

## Frontend

- 新增模块化 API 文件；类型尽量留在 API 模块或专属类型文件，并复用 `frontend/src/constants/channel.ts` 的 `BillingMode`。
- 新页面沿用 `AppLayout`、`BaseDialog`、`Select`、`Toggle` 和应用通知机制。
- 复用 `mTokToPerToken`、`perTokenToMTok` 与 `formatScaled`，如需严格区分“空”和“非法”则补充小型共享校验 helper，而不是复制来源页面的价格转换代码。
- 页面根据计费模式渲染字段；切换模式时构造 payload 时显式清空无关字段。
- 新建默认启用；编辑弹窗只编辑规则和价格，启停统一在列表切换，避免两个入口产生并发覆盖。
- 列表采用模式感知摘要：token 展示 input/output/cache，非 token 展示每次价格。
- 路由置于渠道价格之后、渠道监控之前；侧栏置于渠道管理分组，保留 Prompt Rules 与现有功能顺序。

## Compatibility

- Kiro：global 只改变美元成本，credits 字段继续记录。
- Prompt Rules：不改变模型匹配字段；token 数仍使用上游/现有统计结果。
- OpenAI Responses：统一计价和图片/视频辅助路径均需识别 global 来源。
- Astra：无 global 命中时保持当前长上下文、service tier、reasoning 和 cache 定价。
- Batch Image：继续通过共享 resolver 获取快照单价，新增 global 命中回归。
- Group-less API key：普通/OpenAI token、image、video 路径仍执行 global，不能因 `Group == nil` 回退旧 catalog 快捷路径。

## Authorization and Audit

- 路由沿用当前 admin group 的认证与授权，不在本任务引入不完整的 super-admin 角色。
- 所有 mutation 保持在现有 audit middleware 覆盖范围内，并增加路由级测试确认动作、操作者、资源路径和结果被记录。
- 超级管理员功能落地后再将价格 mutation 收紧为 super-admin-only；读取权限可继续评估是否保留给普通管理员。

## Rollback

- 功能使用独立迁移、独立文件和小范围 resolver 分支，可通过回退该功能提交并停止暴露管理入口恢复旧解析顺序。
- 已创建的表可保留而不影响旧二进制；回滚不执行破坏性 DROP。
- 部署前继续遵守现有流程：备份服务器原始镜像，健康检查失败时回退镜像。

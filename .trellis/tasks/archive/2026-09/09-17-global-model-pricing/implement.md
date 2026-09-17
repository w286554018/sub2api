# 全局模型价格实施计划

## 1. 锁定旧行为

- 扩展 resolver 测试，固定当前 `Group -> Channel -> LiteLLM -> Fallback` 行为。
- 为普通网关、OpenAI 网关、Responses、媒体和批量图片补充“未配置 global 时结果不变”的回归断言。

## 2. 数据层与服务层

- 新增 `238_global_model_pricing.sql` 及迁移结构测试。
- 新增 repository 接口与 PostgreSQL 实现，覆盖 CRUD、规范化唯一冲突、数据库约束和 not found。
- 新增 service 校验、快照、精确/最长前缀匹配和深拷贝。
- 依照现有 channel cache 模式增加 Redis Pub/Sub 失效接口、实现和测试。

## 3. 计价链路

- 在 resolver 增加 global 来源、构造器和“旧有效价格 + global 覆盖”逻辑。
- 更新所有来源判断分支，使 token、按次、image、video、audio、普通/OpenAI 网关与 batch image 一致识别 global。
- 修复无分组 API Key 跳过 resolver 的快捷分支。
- 增加 Kiro credits、Prompt Rules/Astra 不回归、未知私有模型及 global image 双网关测试。

## 4. 管理 API 与依赖注入

- 新增 handler 及 API 测试；价格 PUT 不修改 enabled，启停使用单条 RETURNING 更新，并验证现有审计中间件覆盖写操作。
- 更新 handler/repository/service wire provider、路由注册和生成的 `wire_gen.go`。
- 保留当前 Kiro、Prompt Rules 和其他 admin handler，不用来源文件覆盖现有聚合文件。

## 5. 管理前端

- 新增 API 模块与类型，复用 canonical `BillingMode`。
- 新增模式感知的 `GlobalPricingView`，对齐服务端校验，处理 pending、409、404 和删除确认。
- 更新 API barrel、路由、侧栏和中英文 i18n。
- 增加 API、页面、路由/侧栏和 locale 测试。

## 6. Verification

后端定向：

```powershell
cd backend
go test ./internal/service ./internal/repository ./internal/handler/admin ./internal/server/routes
go generate ./cmd/server
git diff --exit-code -- cmd/server/wire_gen.go
```

前端定向：

```powershell
cd frontend
pnpm test:run -- src/views/admin/__tests__/GlobalPricingView.spec.ts src/api/__tests__/admin.globalPricing.spec.ts
pnpm run check:i18n
pnpm run typecheck
pnpm run lint:check
```

全量：

```powershell
cd backend
go test ./...
golangci-lint run ./...
cd ../frontend
pnpm test:run
pnpm run build
```

## Risky Files and Review Points

- `backend/internal/service/model_pricing_resolver.go`: 不能覆盖当前新增的 OpenAI/Codex 归一化与长上下文行为。
- `backend/internal/service/openai_gateway_usage.go`、`gateway_usage_billing.go`、`billing_token_cost_request.go`: 必须逐个审查所有 source 分支。
- `backend/internal/*/wire.go` 与 `backend/cmd/server/wire_gen.go`: 只增加 provider，不丢失 Kiro/Prompt Rules 依赖。
- `frontend/src/router/index.ts`、`AppSidebar.vue`、`api/admin/index.ts`: 采用小范围插入，不用来源版本整文件替换。
- migration 只能新增，不能修改历史文件。

## Commit and Rollback Boundary

- 此功能完成全部验证后单独提交，commit 使用 Lore trailers。
- 只精确暂存本功能文件，不提交 `.trellis/`、镜像 tar、部署脚本或其他原有未跟踪文件。
- 未通过全量验证前不构建或部署生产镜像。

# Kiro Phase 3 Web Search Server Tools — 实现检查清单

## 总体进度：6/8 Steps 完成 (75%)

---

## ✅ Step 0: 公共契约与基础设施（前置依赖）

- [x] `SearchContract` 类型定义
- [x] `SearchRound` 和 `SearchResultItem` 数据模型
- [x] Tool ID 分配规则
- [x] Golden fixtures（11 个测试用例）
- [x] 测试验证通过

**文件**：
- `backend/internal/pkg/kiro/search_contract.go`
- `backend/internal/pkg/kiro/testdata/golden_search_*.json`

---

## ✅ Step 1: Query 提取统一

- [x] 非流式 query 提取
- [x] 流式 query 提取（增量 JSON 缓冲）
- [x] 错误处理（空 query、无效 JSON）
- [x] 单元测试（100% 覆盖）

**文件**：
- `backend/internal/pkg/kiro/search_query_extractor.go`
- `backend/internal/pkg/kiro/search_query_extractor_test.go`

---

## ✅ Step 2: SearchOrchestrator 核心编排器

- [x] 多轮搜索循环（最多 5 轮）
- [x] 重复 query 检测
- [x] 超时控制（单轮 30s，总计 120s）
- [x] 上下文取消处理
- [x] 混合工具策略
- [x] 配置参数（环境变量可覆盖）
- [x] Golden fixtures 测试（11/11 通过）

**文件**：
- `backend/internal/pkg/kiro/search_orchestrator.go`
- `backend/internal/pkg/kiro/search_orchestrator_test.go`

---

## ✅ Step 3: SSE 流式响应包装器

- [x] SSE 事件流写入器（`AnthropicSSEWriter`）
- [x] 搜索投影器（`SearchProjector`）
- [x] Tool ID 分配器（`SearchToolIDAllocator`）
- [x] 事件顺序保持
- [x] 错误和取消处理
- [x] 单元测试

**文件**：
- `backend/internal/pkg/kiro/sse_writer.go`
- `backend/internal/pkg/kiro/search_projector.go`
- `backend/internal/pkg/kiro/sse_writer_test.go`
- `backend/internal/pkg/kiro/search_projector_test.go`

---

## ✅ Step 4: 真实 MCP web_search 工具调用

- [x] Feature flag 检测（`isKiroServerToolsWebSearchEnabled`）
- [x] MCP 客户端调用（复用 `callKiroWebSearchMCP`）
- [x] 数据转换（`WebSearchResults` → `SearchRound`）
- [x] Token 传递链路
- [x] 错误处理（MCP 失败、空结果）
- [x] 集成测试

**文件**：
- `backend/internal/service/kiro_runtime.go`（新增 `executeKiroMCPWebSearch`）
- `backend/internal/service/kiro_http_helpers.go`（新增 `isKiroServerToolsWebSearchEnabled`）
- `backend/internal/service/kiro_websearch_integration_test.go`

---

## ✅ Step 5: 网关集成与特性门控

- [x] 双模式 feature flag（`single` vs `orchestrated`）
- [x] 流式请求路径集成
- [x] 流式包装器框架（`streamKiroWithSearchInjection`）
- [x] 零回归验证（所有现有测试通过）
- [x] 编译检查通过
- [ ] ⚠️ SSE 注入逻辑未完成（TODO for Step 6）

**文件**：
- `backend/internal/service/kiro_runtime.go`（主流程集成）
- `backend/internal/service/kiro_http_helpers.go`（`getKiroWebSearchMode`）

**技术债务**：
- `streamKiroWithSearchInjection` 当前仅为框架，回退到与 `single` 模式相同的行为
- 需要在 Step 6 实现真正的 SSE 事件解析和搜索块注入

---

## ✅ Step 6: 搜索投影器集成与端到端测试

**目标**：完善流式响应的搜索块注入，实现真正的 `orchestrated` 模式

### 已完成 ✅
- [x] SSE 事件解析器（`kiropkg.SSEScanner`）
  - [x] 解析 Kiro 上游的 SSE 事件流
  - [x] 提取 event 类型和 data payload
  - [x] 错误处理和 EOF 检测
  - [x] 8 个单元测试覆盖所有边界情况

- [x] 完善 `streamKiroWithSearchInjection`
  - [x] 在 `message_start` 后注入搜索块
  - [x] 使用 `SearchProjector` 生成 Anthropic SSE 事件
  - [x] 调整上游事件的 block index（offset）
  - [x] 正确中继上游事件
  - [x] 新增 `SkipMessageStart()` 方法支持中流注入

- [x] 端到端集成测试
  - [x] Mock Kiro 上游响应
  - [x] 验证搜索块正确注入
  - [x] 验证 block index 正确偏移
  - [x] 验证完整的 SSE 事件流
  - [x] 3 个场景测试 + 4 个单元测试

- [x] 降级路径测试
  - [x] 块索引调整失败时使用原始数据
  - [x] 零回归验证（所有 60+ Kiro 测试通过）

**完成文件**：
- `backend/internal/pkg/kiro/sse_scanner.go` (217 行)
- `backend/internal/pkg/kiro/sse_scanner_test.go` (229 行)
- `backend/internal/pkg/kiro/search_projector.go` (122 行)
- `backend/internal/pkg/kiro/search_projector_test.go` (197 行)
- `backend/internal/pkg/kiro/sse_writer.go` (+12 行)
- `backend/internal/service/kiro_runtime.go` (+78 行)
- `backend/internal/service/kiro_stream_injection_test.go` (280 行)

**文档**：
- `docs/kiro-phase3-search-streaming-implementation.md`
- `.trellis/tasks/09-01-kiro-p3s1-search-orchestration/step6-summary.md`

---

## ⏳ Step 7: SearchOrchestrator 多轮循环集成

**目标**：将 `SearchOrchestrator` 集成到实际请求流程，支持多轮搜索

### 待实现
- [ ] 在 `streamKiroWithSearchInjection` 中调用 `orchestrator.Run()`
- [ ] 处理模型返回的后续 `web_search` tool_use
- [ ] 实现搜索轮次追踪和限制
- [ ] 集成测试（多轮搜索场景）

**涉及文件**：
- `backend/internal/service/kiro_runtime.go`
- `backend/internal/service/kiro_websearch_integration_test.go`

---

## ⏳ Step 8: 性能优化与监控

**目标**：优化搜索性能，添加监控指标

### 待实现
- [ ] 性能测试
  - [ ] 搜索延迟 < 5s（单轮）
  - [ ] 并发搜索性能
  - [ ] 缓存命中率

- [ ] 监控指标
  - [ ] 搜索轮次分布
  - [ ] MCP 调用成功率
  - [ ] 降级触发频率
  - [ ] 搜索延迟分布

- [ ] 优化措施
  - [ ] 并行搜索（多个 query）
  - [ ] 结果缓存
  - [ ] 超时和取消优化

**涉及文件**：
- `backend/internal/service/kiro_runtime.go`
- `backend/internal/pkg/kiro/search_orchestrator.go`
- `backend/internal/metrics/` (可能需要新建)

---

## 测试覆盖总览

### 单元测试（已完成）
- ✅ Query 提取：`TestExtractSearchQuery`
- ✅ 搜索编排器：`TestSearchOrchestrator`（11 golden fixtures）
- ✅ SSE 写入器：`TestAnthropicSSEWriter`
- ✅ 搜索投影器：`TestSearchProjector`
- ✅ Feature flag：`TestIsKiroServerToolsWebSearchEnabled`
- ✅ Web search 结果转换：`TestWebSearchResultConversion`

### 集成测试（部分完成）
- ✅ 注入搜索上下文：`TestInjectSearchContextIntoBody`
- ⏳ 端到端流式响应（待 Step 6）
- ⏳ 多轮搜索循环（待 Step 7）
- ⏳ 降级路径（待 Step 6-7）

### 回归测试（已验证）
- ✅ 所有 Kiro 测试：`go test ./internal/service -run "Kiro"`
- ✅ 编译检查：`go build ./cmd/server`

---

## 技术债务与风险

### 当前已知限制
1. ⚠️ **`orchestrated` 模式未完全实现**（Step 5 遗留）
   - 当前行为：回退到 `single` 模式
   - 需要：SSE 事件解析和 block index 重编号

2. ⚠️ **多轮搜索未启用**（Step 7）
   - `SearchOrchestrator` 已实现但未集成
   - 需要处理模型返回的后续 tool_use

3. ⚠️ **混合工具场景未覆盖**
   - 搜索 + 用户工具的复杂交互
   - Phase 3 不覆盖，留待后续版本

### 风险评估
- **低风险**：Steps 0-5 已完成，基础架构稳定
- **中风险**：Step 6 SSE 注入逻辑较复杂，需要仔细测试
- **中风险**：Step 7 多轮循环可能有边界情况需要处理

---

## 参考资料

- **PRD**：`.trellis/tasks/09-01-kiro-p3s1-search-orchestration/prd.md`
- **进度跟踪**：`.trellis/tasks/09-01-kiro-p3s1-search-orchestration/progress.md`
- **Step 总结**：
  - Step 5: `.trellis/tasks/09-01-kiro-p3s1-search-orchestration/step5-summary.md`
- **协议讨论**：`docs/KIRO_PROTOCOL_IMPROVEMENT_DISCUSSION.md`
- **Golden Fixtures**：`backend/internal/pkg/kiro/testdata/golden_search_*.json`

---

## 下一步行动

**优先级 1（Step 6）**：
1. 实现 `kiropkg.SSEScanner`
2. 完善 `streamKiroWithSearchInjection`
3. 编写端到端测试

**优先级 2（Step 7）**：
1. 集成 `SearchOrchestrator.Run()`
2. 处理多轮搜索循环
3. 验证复杂场景

**优先级 3（Step 8）**：
1. 性能测试和优化
2. 添加监控指标
3. 文档完善

---

**最后更新**：2026-09-01  
**当前阶段**：Step 5 完成，开始 Step 6

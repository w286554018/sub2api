# Step 1: Model-Driven Search Orchestration — PRD

## 目标

复用/整理现有 Kiro 多轮搜索循环，统一搜索编排逻辑。使 WebSearch 成为模型驱动的多轮对话而非网关直接执行。

## 背景

- `kiro_websearch.go` 已有 5 轮搜索循环但缺少：重复 query 检测、超时取消、混合工具策略、统一的流式/非流式 query 提取
- `gateway_websearch_emulation.go` 完全无模型参与
- Step 0 完成后有统一的 `SearchRound` 模型和 tool ID 分配规则

## 依赖

- Step 0（公共契约）必须先完成
- 与 Step 2（协议投影）可并行

## 范围

### 1. 统一搜索编排器

```go
type SearchOrchestrator struct {
    MaxRounds       int           // 默认 5，可配置
    RoundTimeout    time.Duration // 单轮超时
    TotalTimeout    time.Duration // 总超时
    MaxResultBytes  int           // 单条结果最大字节
    MaxResults      int           // 单轮最大结果数
}
```

编排流程：
```
客户端请求（含 web_search 工具）
  → 模型生成 tool_use（搜索词）
  → 编排器提取 query
  → 执行搜索（MCP 或 HTTP）
  → 结果转为 SearchRound
  → 注入为 tool_result 回传模型
  → 模型决定：继续搜索 / 输出答案
  → 循环或终止
```

### 2. Query 提取统一

- 非流式：从 `tool_use.input` 直接提取
- 流式：从增量 `input_json_delta` 缓冲完整 JSON 后提取
- 两条路径共用同一提取函数

### 3. 终止条件

| 条件 | 行为 |
|------|------|
| 模型输出文本（非工具调用） | 正常终止，输出答案 |
| 达到 MaxRounds | 强制终止，用已有结果生成答案 |
| 重复 query（与前轮相同） | 终止循环，避免死循环 |
| TotalTimeout 到期 | 终止，用已有结果 |
| ctx.Done() | 立即停止 |
| MCP 搜索失败 | 降级：用空结果告知模型，或 fallback 到网关搜索 |
| 模型返回空 query | 终止循环 |

### 4. 混合工具策略

当请求同时包含 `web_search` 和用户自定义工具时：
- 搜索工具调用由编排器处理
- 用户工具调用正常返回给客户端（`stop_reason: tool_use`）
- 不支持"搜索后调用用户工具再继续搜索"的混合循环（过于复杂，Phase 3 不覆盖）

### 5. 降级策略

| 场景 | 降级行为 |
|------|---------|
| MCP 不可用 | fallback 到 gateway HTTP 搜索 |
| 搜索返回 0 结果 | 告知模型"未找到结果"，让模型自行回答 |
| 搜索结果超大 | 截断到 MaxResultBytes |
| 搜索限流 | 等待 + 重试一次，仍失败则告知模型 |

## Feature Flag

`SUB2API_KIRO_WEBSEARCH_MODEL_LOOP=false`（默认关闭）

关闭时完全走现有搜索路径。

## 可配置参数

| 参数 | 环境变量 | 默认值 |
|------|---------|--------|
| 最大搜索轮数 | `SUB2API_KIRO_MAX_SEARCH_ROUNDS` | 5 |
| 单轮超时 | `SUB2API_KIRO_SEARCH_ROUND_TIMEOUT` | 30s |
| 总超时 | `SUB2API_KIRO_SEARCH_TOTAL_TIMEOUT` | 120s |
| 单条结果最大字节 | `SUB2API_KIRO_MAX_SEARCH_RESULT_BYTES` | 8192 |
| 单轮最大结果数 | `SUB2API_KIRO_MAX_SEARCH_RESULTS` | 10 |

## 验收标准

1. 模型能驱动多轮搜索（≥2 轮 fixture 测试通过）
2. 重复 query 检测有效
3. 超时和最大轮数终止正确
4. MCP 失败降级到 HTTP 搜索
5. 混合工具场景正确分流
6. 流式和非流式 query 提取一致
7. Flag 关闭时走原有路径，零变化
8. 现有测试无回归

## 参考

- Step 0 输出的 SearchRound 模型和 tool ID 规则
- `service/kiro_websearch.go` 现有循环逻辑
- `service/gateway_websearch_emulation.go` 网关搜索

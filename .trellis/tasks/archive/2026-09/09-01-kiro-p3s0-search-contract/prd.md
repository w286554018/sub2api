# Step 0: Search Contract and Baseline — PRD

## 目标

盘点现有两条搜索路径，定义统一的搜索轮次内部模型和 tool ID/index 分配规则，建立回归 fixture。为 Step 1/2 的并行开发提供公共契约。

## 背景

当前项目有两条独立的搜索路径：

1. **Kiro MCP 搜索**（`service/kiro_websearch.go`，475 行）
   - 已有最多 5 轮模型参与搜索循环
   - 调用 MCP 搜索 → 注入工具结果 → 再请求 Kiro → 分析是否继续搜索

2. **通用网关搜索模拟**（`service/gateway_websearch_emulation.go`，471 行）
   - 网关直接执行搜索 + 生成摘要
   - 模型不参与搜索词选择

两条路径各自维护搜索逻辑，流式和非流式行为存在不一致（如流式结果 block 缺 `tool_use_id`）。

## 范围

### 1. 路径盘点文档

输出一份搜索路径对照表：

| 维度 | Kiro MCP 路径 | 通用 Emulation 路径 |
|------|-------------|-------------------|
| 触发条件 | ? | ? |
| 搜索执行方式 | MCP 调用 | 直接 HTTP |
| 模型参与度 | 多轮循环 | 无 |
| 结果注入方式 | tool_result | 摘要拼接 |
| 输出 block 类型 | server_tool_use? | server_tool_use? |
| 流式 tool_use_id | ? | ? |
| 最大轮数 | 5 | 1 |
| 错误处理 | ? | ? |

### 2. SearchRound 内部模型定义

```go
type SearchRound struct {
    RoundNumber   int
    Query         string
    ToolUseID     string          // 稳定、不重复
    Results       []SearchResult
    Source        string          // "mcp" / "gateway" / "native"
    StopReason    string          // "done" / "continue" / "max_rounds" / "timeout" / "error"
    DurationMs    int64
}

type SearchResult struct {
    Title       string
    URL         string
    Snippet     string
    PageAge     string
    EncryptedContent string      // 空 = 非原生
}
```

### 3. Tool ID 和 Block Index 分配规则

- Tool ID 格式：`srvtoolu_{uuid}` 前缀区分 server tool
- 多轮搜索不得重复 ID
- 合成 block 与上游 block 的 index 不得冲突
- 流式和非流式使用同一分配规则

### 4. 回归 Fixture

在 `testdata/golden/` 下新增：

```
websearch_single/     — 单轮搜索（Kiro 事件 + 期望 SSE + JSON）
websearch_multi/      — 多轮搜索
websearch_empty/      — 搜索无结果
websearch_error/      — MCP 搜索失败
```

## Feature Flag

不涉及新 flag（Step 0 只定义契约和 fixture，不改运行时行为）。

## 验收标准

1. 搜索路径对照表填写完整（基于代码审查，非猜测）
2. SearchRound struct 定义并编译通过
3. Tool ID 分配规则文档化
4. 4 组搜索 golden fixture 创建
5. 现有测试无回归

## 参考

- `backend/internal/service/kiro_websearch.go`
- `backend/internal/service/gateway_websearch_emulation.go`
- `backend/internal/pkg/kiro/websearch.go`
- `backend/internal/pkg/kiro/websearch_stream.go`
- KIRO_PROTOCOL_IMPROVEMENT_DISCUSSION.md

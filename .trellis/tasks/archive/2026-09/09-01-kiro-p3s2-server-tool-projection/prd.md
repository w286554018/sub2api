# Step 2: Anthropic Server Tool Projection — PRD

## 目标

将内部搜索结果转换为 Anthropic 兼容的 `server_tool_use` / `web_search_tool_result` content blocks，在流式和非流式路径都输出正确的协议格式。

## 背景

- Anthropic WebSearch 使用专用 block 类型：`server_tool_use`（搜索请求）和 `web_search_tool_result`（搜索结果）
- 现有 `websearch.go` 已有部分 server tool block 生成（`GenerateSearchIndicatorEvents`、`InjectSearchIndicatorsInResponse`）
- 已知问题：流式结果 block 缺 `tool_use_id`，与非流式不一致
- Phase 1 的 `AnthropicSSEWriter` 状态机需要扩展支持 server tool block 类型

## 依赖

- Step 0（公共契约）必须先完成
- 与 Step 1（搜索编排）可并行
- 使用 Phase 1 的 `AnthropicSSEWriter` 和 `SSEValidator`

## 范围

### 1. Anthropic Server Tool Block 格式

数据来源：Anthropic API 参考

#### server_tool_use（搜索请求 block）

```json
{
  "type": "server_tool_use",
  "id": "srvtoolu_abc123",
  "name": "web_search",
  "input": {"query": "latest AI safety research"}
}
```

#### web_search_tool_result（搜索结果 block）

```json
{
  "type": "web_search_tool_result",
  "tool_use_id": "srvtoolu_abc123",
  "content": [
    {
      "type": "web_search_result",
      "title": "AI Safety Research 2026",
      "url": "https://example.com/ai-safety",
      "encrypted_content": "",
      "page_age": "2 days ago"
    }
  ]
}
```

注意：`encrypted_content` 是 Anthropic 原生专有数据。Kiro 搜索结果不具备该字段，应设为空字符串并在内部标记 `source=emulated`。

### 2. 流式 SSE 事件序列

搜索场景的完整 SSE 序列：

```
message_start
→ content_block_start(server_tool_use, index=0)     # 搜索请求
→ content_block_delta(input_json_delta, index=0)     # query
→ content_block_stop(index=0)
→ content_block_start(web_search_tool_result, index=1) # 搜索结果
→ content_block_stop(index=1)
→ content_block_start(text, index=2)                 # 模型答案
→ content_block_delta(text_delta, index=2)*
→ content_block_stop(index=2)
→ message_delta(end_turn)
→ message_stop
```

### 3. SearchRound → Content Blocks 转换

```go
// 将 SearchRound 转换为 Anthropic content blocks
func ProjectSearchRound(round SearchRound) []ContentBlock {
    blocks := []ContentBlock{
        {
            Type:  "server_tool_use",
            ID:    round.ToolUseID,
            Name:  "web_search",
            Input: map[string]any{"query": round.Query},
        },
        {
            Type:       "web_search_tool_result",
            ToolUseID:  round.ToolUseID,
            Content:    projectSearchResults(round.Results),
        },
    }
    return blocks
}
```

### 4. AnthropicSSEWriter 扩展

在 Phase 1 的 SSEWriter 中新增：

```go
func (s *AnthropicSSEWriter) StartServerToolUseBlock(id, name string) error
func (s *AnthropicSSEWriter) StartWebSearchToolResultBlock(toolUseID string) error
```

SSEValidator 也需要扩展支持 `server_tool_use` 和 `web_search_tool_result` block 类型的验证。

### 5. 统一 tool_use_id

- 非流式和流式路径使用同一 ID 分配（从 Step 0 的规则）
- `server_tool_use.id` 与对应 `web_search_tool_result.tool_use_id` 必须一致
- 多轮搜索每轮使用不同 ID

### 6. 上游历史消息过滤

合成的 `server_tool_use` / `web_search_tool_result` blocks 不能原样回传给 Kiro 上游（上游不认识这些类型）。

- 在构建 Kiro payload 时，通过 `FilterWebSearchHistoryBlocks`（已有）过滤掉这些 blocks
- 确保过滤逻辑覆盖新增的 block 类型
- 不误删真实的上游 block

## Feature Flag

`SUB2API_KIRO_SERVER_TOOL_PROJECTION=false`（默认关闭）

关闭时搜索结果不生成 `server_tool_use` blocks，走现有路径。

## 验收标准

1. 非流式响应包含正确的 `server_tool_use` + `web_search_tool_result` blocks
2. 流式 SSE 事件序列合法（通过 SSEValidator）
3. `server_tool_use.id` == `web_search_tool_result.tool_use_id`
4. 多轮搜索每轮 ID 不重复
5. Block index 单调递增，与文本/thinking blocks 不冲突
6. 流式和非流式语义等价
7. 上游历史消息正确过滤
8. Golden fixture 测试通过
9. Flag 关闭时走原有路径
10. 现有测试无回归

## 参考

- Step 0 输出的 SearchRound 模型和 tool ID 规则
- Phase 1 的 `sse_writer.go`（AnthropicSSEWriter + SSEValidator）
- `backend/internal/pkg/kiro/websearch.go` 现有 block 生成
- Anthropic API: `web_search_20260209` server tool 规格

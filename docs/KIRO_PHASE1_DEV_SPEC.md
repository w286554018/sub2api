# Kiro Phase 1 开发规格说明书

## 文档信息

- **目的**：补充 KIRO_ANTHROPIC_COMPATIBILITY_PLAN.md 和 KIRO_PROTOCOL_IMPROVEMENT_DISCUSSION.md 中缺失的实施细节
- **适用阶段**：Phase 1（流式状态机 + 结构化输出闭环）
- **数据来源**：
  - Anthropic 官方 API 参考（claude-api skill, 版本 2023-06-01）
  - 项目源码 `backend/internal/pkg/kiro/translator.go`（4506 行）
  - 项目源码 `backend/internal/pkg/kiro/signature.go`（111 行）
  - 项目现有测试 `backend/internal/pkg/kiro/translator_test.go`（3094 行，120 个测试函数）

---

## 1. Anthropic SSE 事件规格

### 1.1 事件类型与 JSON 载荷

数据来源：Anthropic API curl/examples.md

#### message_start

首个事件，包含消息元数据和 input usage。

```json
{
  "type": "message_start",
  "message": {
    "id": "msg_01XFDUDYJgAACzvnptvVoYEL",
    "type": "message",
    "role": "assistant",
    "content": [],
    "model": "claude-opus-5",
    "stop_reason": null,
    "stop_sequence": null,
    "usage": {
      "input_tokens": 25,
      "output_tokens": 1,
      "cache_creation_input_tokens": 0,
      "cache_read_input_tokens": 0
    }
  }
}
```

#### content_block_start

每个内容块的开始。`index` 从 0 开始单调递增。

```json
// text 块
{"type": "content_block_start", "index": 0, "content_block": {"type": "text", "text": ""}}

// thinking 块
{"type": "content_block_start", "index": 0, "content_block": {"type": "thinking", "thinking": ""}}

// tool_use 块
{"type": "content_block_start", "index": 1, "content_block": {"type": "tool_use", "id": "toolu_abc123", "name": "get_weather", "input": {}}}
```

#### content_block_delta

内容块的增量数据。`index` 必须与对应 `content_block_start` 的 `index` 一致。

```json
// text_delta
{"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "Hello"}}

// thinking_delta
{"type": "content_block_delta", "index": 0, "delta": {"type": "thinking_delta", "thinking": "Let me think..."}}

// signature_delta（thinking 块结束前的签名）
{"type": "content_block_delta", "index": 0, "delta": {"type": "signature_delta", "signature": "ErUB..."}}

// input_json_delta（tool_use 块的参数增量）
{"type": "content_block_delta", "index": 1, "delta": {"type": "input_json_delta", "partial_json": "{\"location\":"}}
```

#### content_block_stop

内容块结束。

```json
{"type": "content_block_stop", "index": 0}
```

#### message_delta

消息级别的最终更新，包含 `stop_reason` 和累计 `output_tokens`。

```json
{
  "type": "message_delta",
  "delta": {"stop_reason": "end_turn", "stop_sequence": null},
  "usage": {"output_tokens": 12}
}
```

#### message_stop

流结束标记。

```json
{"type": "message_stop"}
```

#### ping

心跳事件，可在任意位置出现。

```json
{"type": "ping"}
```

#### error（流式错误）

```json
{"type": "error", "error": {"type": "overloaded_error", "message": "Overloaded"}}
```

### 1.2 stop_reason 枚举值

数据来源：Anthropic API 参考

| 值 | 含义 |
|---|---|
| `end_turn` | 模型自然结束 |
| `max_tokens` | 达到 max_tokens 限制 |
| `stop_sequence` | 命中 stop_sequence |
| `tool_use` | 模型请求调用工具 |
| `refusal` | 安全分类器拒绝（Fable 5/Opus 5） |
| `pause_turn` | 服务端工具执行暂停 |

### 1.3 合法 SSE 事件序列（状态转换矩阵）

数据来源：Anthropic API 参考 + 样本 B 行为描述

```
INIT
  → message_start                         (恰好一次)

MESSAGE_STARTED
  → content_block_start                   (开始第一个块)
  → message_delta                         (无内容块的空消息，罕见)

IN_CONTENT_BLOCK
  → content_block_delta                   (同 index 的增量数据，可重复)
  → content_block_stop                    (关闭当前块)

BETWEEN_BLOCKS
  → content_block_start                   (开始下一个块，index 单调递增)
  → message_delta                         (所有块已结束)

MESSAGE_DELTA_SENT
  → message_stop                          (流结束)

任意位置:
  → ping                                  (心跳，不改变状态)
  → error                                 (错误，终止流)
```

#### 典型完整序列示例

**普通文本：**
```
message_start → content_block_start(text,0) → content_block_delta(text_delta,0)* → content_block_stop(0) → message_delta(end_turn) → message_stop
```

**Thinking + 文本：**
```
message_start → content_block_start(thinking,0) → content_block_delta(thinking_delta,0)* → content_block_delta(signature_delta,0) → content_block_stop(0) → content_block_start(text,1) → content_block_delta(text_delta,1)* → content_block_stop(1) → message_delta(end_turn) → message_stop
```

**工具调用：**
```
message_start → content_block_start(text,0) → content_block_delta(text_delta,0)* → content_block_stop(0) → content_block_start(tool_use,1) → content_block_delta(input_json_delta,1)* → content_block_stop(1) → message_delta(tool_use) → message_stop
```

**Thinking + 工具调用：**
```
message_start → content_block_start(thinking,0) → ... → content_block_stop(0) → content_block_start(text,1) → ... → content_block_stop(1) → content_block_start(tool_use,2) → ... → content_block_stop(2) → message_delta(tool_use) → message_stop
```

---

## 2. Kiro 上游事件格式与映射

### 2.1 Kiro 上游事件类型

数据来源：`translator.go` L3705 `extractSemanticEvents()` switch 分支 + L3015 `parseEventStream()` switch 分支

| Kiro 上游事件类型 | 嵌套结构 | 语义 |
|---|---|---|
| `assistantResponseEvent` | `{"assistantResponseEvent": {"content": "..."}}` | 文本内容 + 可能包含工具调用 |
| `reasoningContentEvent` | `{"reasoningContentEvent": {"text": "..."}}` | 推理/思考内容 |
| `toolUseEvent` | `{"toolUseEvent": {"toolUseId": "...", "name": "...", "input": ..., "stop": bool}}` | 工具调用（开始、输入片段、结束） |
| `messageMetadataEvent` | metadata 对象 | usage、模型信息等元数据 |
| `metadataEvent` | 同上 | `messageMetadataEvent` 的别名 |
| `supplementaryWebLinksEvent` | 搜索链接 | Web 搜索辅助链接 |
| `usageEvent` | usage 对象 | token 用量更新 |
| `messageStopEvent` | — | 消息结束 |
| `message_stop` | — | `messageStopEvent` 的别名 |
| `meteringEvent` | 计量数据 | 计费计量 |

### 2.2 Kiro 语义事件到 Anthropic SSE 的映射

数据来源：`translator.go` L235-262 `kiroSemanticEvent` struct + L3705 `extractSemanticEvents()`

| Kiro 语义类型 | 常量名 | 源上游事件 | 目标 Anthropic SSE |
|---|---|---|---|
| `content` | `kiroSemanticContent` | `assistantResponseEvent.content` | `content_block_start(text)` + `content_block_delta(text_delta)` |
| `reasoning` | `kiroSemanticReasoning` | `reasoningContentEvent.text` | `content_block_start(thinking)` + `content_block_delta(thinking_delta)` |
| `tool_use` | `kiroSemanticToolUse` | `toolUseEvent` (完整的，有 id+name+input) | `content_block_start(tool_use)` + `content_block_delta(input_json_delta)` |
| `tool_input` | `kiroSemanticToolInput` | `toolUseEvent` (增量输入片段，只有 id) | `content_block_delta(input_json_delta)` |
| `tool_stop` | `kiroSemanticToolStop` | `toolUseEvent` (stop=true) | `content_block_stop` |
| `usage` | `kiroSemanticUsage` | `metadataEvent`/`usageEvent`/`meteringEvent` | `message_start.usage` 或 `message_delta.usage` |
| `assistant_tool_use` | `kiroSemanticAssistantTU` | `assistantResponseEvent` 内嵌工具 | 同 `tool_use` |

### 2.3 Kiro 语义事件 Go struct

数据来源：`translator.go` L247-262

```go
type kiroSemanticEvent struct {
    Type                   kiroSemanticEventType  // content/reasoning/tool_use/tool_input/tool_stop/usage/assistant_tool_use
    Content                string                 // 文本内容（Type=content 时）
    Reasoning              string                 // 推理文本（Type=reasoning 时）
    ToolUseID              string                 // 工具调用 ID
    ToolName               string                 // 工具名
    ToolInput              string                 // 工具输入（字符串形式）
    ToolInputMap           map[string]any         // 工具输入（对象形式）
    ToolStop               bool                   // 工具调用是否结束
    ToolUse                *KiroToolUse           // 完整工具调用（非流式路径）
    SourceEventType        string                 // 原始事件类型名
    RawEvent               map[string]any         // 原始事件
    SourceStopReason       string                 // 上游 stop reason
    IsDuplicateContent     bool                   // 是否重复内容
    ContextUsagePercentage float64                // 上下文使用百分比
}
```

---

## 3. 流式状态机设计

### 3.1 状态定义

```go
type StreamState int

const (
    StateInit             StreamState = iota // 初始状态
    StateMessageStarted                      // message_start 已发送
    StateInContentBlock                      // 在内容块中（delta 可发送）
    StateBetweenBlocks                       // 内容块之间
    StateMessageDelta                        // message_delta 已发送
    StateMessageStopped                      // message_stop 已发送（终态）
    StateError                               // 错误（终态）
)
```

### 3.2 状态转换表

| 当前状态 | 事件 | 目标状态 | 验证规则 |
|---------|------|---------|---------|
| `Init` | `message_start` | `MessageStarted` | 只允许一次 |
| `MessageStarted` | `content_block_start` | `InContentBlock` | index 必须为 0 |
| `MessageStarted` | `message_delta` | `MessageDelta` | 空消息（无内容块） |
| `InContentBlock` | `content_block_delta` | `InContentBlock` | delta.index 必须等于当前块 index |
| `InContentBlock` | `content_block_stop` | `BetweenBlocks` | stop.index 必须等于当前块 index |
| `BetweenBlocks` | `content_block_start` | `InContentBlock` | index 必须为 prev_index + 1 |
| `BetweenBlocks` | `message_delta` | `MessageDelta` | — |
| `MessageDelta` | `message_stop` | `MessageStopped` | — |
| 任意非终态 | `ping` | 不变 | 直接转发，不改变状态 |
| 任意非终态 | `error` | `Error` | 终止流 |

### 3.3 不变量断言（从 KIRO_ANTHROPIC_COMPATIBILITY_PLAN.md §7.2 提取）

1. `message_start` 恰好出现一次且为第一个非 ping 事件
2. `content_block_start` 的 `index` 从 0 开始单调递增，不跳跃
3. `content_block_delta` 的 `index` 必须等于最近一个 `content_block_start` 的 `index`
4. 每个 `content_block_start` 必须有且仅有一个对应的 `content_block_stop`
5. `content_block_delta.delta.type` 必须与 `content_block_start.content_block.type` 匹配：
   - `text` → `text_delta`
   - `thinking` → `thinking_delta` 或 `signature_delta`
   - `tool_use` → `input_json_delta`
6. `message_delta` 恰好出现一次，在所有 `content_block_stop` 之后
7. `message_delta.delta.stop_reason` 不为 null
8. `message_delta.usage.output_tokens` 为累计值
9. `message_stop` 为最后一个事件

### 3.4 序列化层接口设计

```go
// AnthropicSSEWriter 是序列化层的核心接口
// 实现：从 kiroSemanticEvent 流生成合法的 Anthropic SSE 流
type AnthropicSSEWriter struct {
    ctx            context.Context
    w              io.Writer
    state          StreamState
    blockIndex     int            // 当前块 index（单调递增）
    currentBlock   string         // 当前块类型（"text"/"thinking"/"tool_use"）
    totalOutput    int            // 累计 output_tokens
    model          string
    requestID      string
    err            error
}

// 公共 API
func NewAnthropicSSEWriter(ctx context.Context, w io.Writer, model, requestID string) *AnthropicSSEWriter
func (s *AnthropicSSEWriter) WriteMessageStart(inputTokens int, cacheCreate, cacheRead int) error
func (s *AnthropicSSEWriter) StartTextBlock() error
func (s *AnthropicSSEWriter) StartThinkingBlock() error
func (s *AnthropicSSEWriter) StartToolUseBlock(id, name string) error
func (s *AnthropicSSEWriter) WriteTextDelta(text string) error
func (s *AnthropicSSEWriter) WriteThinkingDelta(thinking string) error
func (s *AnthropicSSEWriter) WriteSignatureDelta(signature string) error
func (s *AnthropicSSEWriter) WriteInputJSONDelta(partialJSON string) error
func (s *AnthropicSSEWriter) StopContentBlock() error
func (s *AnthropicSSEWriter) WriteMessageDelta(stopReason string, outputTokens int) error
func (s *AnthropicSSEWriter) WriteMessageStop() error
func (s *AnthropicSSEWriter) WritePing() error
func (s *AnthropicSSEWriter) WriteError(errType, message string) error

// 状态查询
func (s *AnthropicSSEWriter) State() StreamState
func (s *AnthropicSSEWriter) BlockIndex() int
func (s *AnthropicSSEWriter) Err() error
```

### 3.5 现有函数拆分计划

| 现有函数 | 行数 | 拆分后归属 |
|---------|------|-----------|
| `StreamEventStreamAsAnthropicWithContext` (L557) | ~800 | 拆为：事件解析循环 + AnthropicSSEWriter 调用 |
| `extractSemanticEvents` (L3705) | ~140 | 保留在协商层（Kiro → 语义事件） |
| `buildClaudeResponse` (非流式构建) | ~100 | 新增 `AnthropicJSONBuilder`（统一内部模型 → JSON） |
| `parseEventStream` (L2966) | ~120 | 保留（Kiro 二进制格式解析） |

改造策略：`StreamEventStreamAsAnthropicWithContext` 内部原有的 SSE 拼接逻辑替换为 `AnthropicSSEWriter` 调用。现有函数签名不变，调用方（`kiro_runtime.go`、`gateway_forward.go`）无需修改。

---

## 4. 结构化输出规格

### 4.1 请求格式

数据来源：Anthropic API 参考 + `translator.go` L1748 `extractStructuredOutputFormat()`

```json
{
  "model": "claude-opus-5",
  "max_tokens": 16000,
  "output_config": {
    "format": {
      "type": "json_schema",
      "name": "my_schema",
      "schema": {
        "type": "object",
        "properties": {
          "title": {"type": "string"},
          "summary": {"type": "string"},
          "tags": {"type": "array", "items": {"type": "string"}}
        },
        "required": ["title", "summary"],
        "additionalProperties": false
      }
    }
  },
  "messages": [{"role": "user", "content": "Analyze this article..."}]
}
```

项目代码兼容的请求路径（`extractStructuredOutputFormat` L1749）：
1. `output_config.format` — 当前标准路径
2. `output_format` — 已弃用但仍接受
3. `response_format` — OpenAI 兼容路径

format.type 枚举值（`buildStructuredOutputTool` L1711）：
- `json_schema` — 带 schema 约束的结构化输出（主要目标）
- `json_object` — 无 schema 约束的 JSON 输出（降级为 prompt 指令）

### 4.2 期望响应格式

#### 非流式

```json
{
  "id": "msg_...",
  "type": "message",
  "role": "assistant",
  "model": "claude-opus-5",
  "content": [
    {
      "type": "text",
      "text": "{\"title\":\"Example\",\"summary\":\"...\",\"tags\":[\"ai\"]}"
    }
  ],
  "stop_reason": "end_turn",
  "usage": {"input_tokens": 100, "output_tokens": 50}
}
```

关键点：
- `content[0].type` 为 `"text"`（不是 `tool_use`）
- `content[0].text` 为合法 JSON 字符串，满足请求中的 schema
- 无 Markdown 围栏、无注释、无额外文本
- `stop_reason` 为 `"end_turn"`

#### 流式

```
event: message_start
data: {"type":"message_start","message":{"id":"msg_...","type":"message","role":"assistant","content":[],"model":"claude-opus-5","stop_reason":null,"usage":{"input_tokens":100,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"{\"title\":"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"\"Example\","}}

... (更多 text_delta)

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":50}}

event: message_stop
data: {"type":"message_stop"}
```

### 4.3 现有代码复用分析

数据来源：`translator.go` 源码

| 函数 | 位置 | 复用判定 | 改造内容 |
|------|------|---------|---------|
| `extractStructuredOutputFormat()` | L1748 | ✅ 直接复用 | 无需改动，已支持三种路径 |
| `buildStructuredOutputTool()` | L1705 | ✅ 复用，需小幅扩展 | 需确保内部工具名不与用户工具冲突（当前用 `__structured_output__`，L57） |
| `extractStructuredOutputToolText()` | L3309 | ⚠️ 复用，需补充 Schema 校验 | 当前直接 `json.Marshal(tool.Input)` 返回，缺少 Schema 验证步骤 |
| `normalizeKiroJSONSchema()` | 存在 | ✅ 复用 | schema 标准化逻辑 |

### 4.4 需要新增的逻辑

#### Schema 校验器

推荐库：`github.com/santhosh-tekuri/jsonschema/v6`

理由：
- 支持 Draft 2020-12 和之前版本
- 无外部依赖
- 广泛使用，维护活跃
- 支持自定义格式校验

```go
func validateStructuredOutput(jsonText string, schemaJSON []byte) error {
    compiler := jsonschema.NewCompiler()
    schema, err := compiler.Compile("schema.json", schemaJSON)
    if err != nil {
        return fmt.Errorf("invalid schema: %w", err)
    }
    var v any
    if err := json.Unmarshal([]byte(jsonText), &v); err != nil {
        return fmt.Errorf("invalid JSON: %w", err)
    }
    return schema.Validate(v)
}
```

#### 失败处理策略

```
请求 → buildStructuredOutputTool → 发送到 Kiro 上游（工具强制调用）
                                           ↓
                                    提取工具结果
                                           ↓
                               extractStructuredOutputToolText
                                           ↓
                                    JSON Schema 校验
                                     ↓            ↓
                                   通过          失败
                                     ↓            ↓
                            转为 JSON text    构造修复请求
                            block 返回       （最多一次）
                                               ↓
                                          重新发送到上游
                                               ↓
                                        再次提取+校验
                                         ↓        ↓
                                       通过      失败
                                         ↓        ↓
                                   返回 JSON   返回错误
```

修复请求策略：
- 由 `kiro_runtime.go` 层负责重试（不在 translator 层）
- 修复 prompt：`"The previous JSON output did not satisfy the schema. Specific error: {validation_error}. Please output valid JSON that satisfies the schema."`
- 不重新注入内部工具（复用同一次对话上下文）
- 超时：继承原始请求超时，不额外增加
- 修复请求的 token 计入客户端 usage

#### 错误响应格式

Schema 校验最终失败时，返回 Anthropic 兼容错误：

```json
{
  "type": "error",
  "error": {
    "type": "invalid_request_error",
    "message": "Structured output validation failed: property 'title' is required"
  }
}
```

流式模式下，发送 `error` 事件后终止流。

---

## 5. Thinking 块规格

### 5.1 请求格式

数据来源：Anthropic API 参考

```json
{
  "model": "claude-opus-5",
  "max_tokens": 16000,
  "thinking": {"type": "adaptive", "display": "summarized"},
  "output_config": {"effort": "high"},
  "messages": [...]
}
```

### 5.2 响应中的 thinking 块

非流式：
```json
{
  "content": [
    {"type": "thinking", "thinking": "Let me analyze...", "signature": "ErUB..."},
    {"type": "text", "text": "The answer is..."}
  ]
}
```

redacted_thinking（模型选择不公开推理内容时）：
```json
{"type": "redacted_thinking", "data": "<opaque base64>"}
```

### 5.3 Signature 处理

数据来源：`signature.go` L1-111

当前实现：项目使用 HMAC-SHA256 合成 signature（非 Anthropic 原生）。

```go
// signature.go 现有逻辑
func GenerateThinkingSignature(thinkingText string) string
```

Phase 1 策略：
- **保持现有 HMAC 合成签名**作为 legacy/compat 默认行为
- 如果 Kiro 上游将来透传原生 signature，优先使用
- 不在 Phase 1 引入 strict 模式（strict 模式移除合成签名是 Phase 2+ 的事）

### 5.4 Kiro reasoning 事件到 thinking 块的映射

数据来源：`translator.go` L3032-3046（非流式）+ L3743-3755（语义提取）

```
Kiro: reasoningContentEvent {text: "..."} （可能多个连续片段）
  ↓
内部语义：kiroSemanticReasoning {Reasoning: "..."}
  ↓
Anthropic 流式：
  content_block_start(thinking, index=N) → thinking_delta* → signature_delta → content_block_stop(N)
  ↓
Anthropic 非流式：
  content[N] = {type: "thinking", thinking: "...", signature: "..."}
```

---

## 6. Feature Flag 机制

### 6.1 配置方式

使用环境变量，与项目现有配置模式一致（参考 `buildInjectedSystemPrompt` 中的 `SUB2API_KIRO_TIME_CONTEXT`）。

| Flag 名称 | 默认值 | 含义 |
|-----------|-------|------|
| `SUB2API_KIRO_SSE_STATE_MACHINE` | `false` | 启用流式状态机（Phase 1 Step 1） |
| `SUB2API_KIRO_STRUCTURED_OUTPUT_VALIDATION` | `false` | 启用结构化输出 Schema 校验（Phase 1 Step 2） |
| `SUB2API_KIRO_CONDITIONAL_INJECTION` | `false` | 启用条件化 System Prompt 注入（Phase 2） |

### 6.2 回滚策略

Flag 为 `false` 时完全走现有代码路径，零行为变化。允许运行时通过 Redis 热更新覆盖环境变量（如果项目已有 Redis 配置热更新机制）。

---

## 7. 测试策略与 Golden Fixture

### 7.1 Golden Fixture 目录结构

```
backend/internal/pkg/kiro/testdata/golden/
├── plain_text/
│   ├── kiro_events.jsonl        # Kiro 上游事件序列
│   ├── anthropic_sse.txt        # 期望的 Anthropic SSE 输出
│   └── anthropic_json.json      # 期望的非流式 JSON 输出
├── thinking/
│   ├── kiro_events.jsonl
│   ├── anthropic_sse.txt
│   └── anthropic_json.json
├── tool_use/
│   ├── kiro_events.jsonl
│   ├── anthropic_sse.txt
│   └── anthropic_json.json
└── structured_output/
    ├── request.json              # 包含 json_schema 的请求
    ├── kiro_events.jsonl         # Kiro 上游工具调用响应
    ├── anthropic_sse.txt         # 期望的 JSON text SSE
    ├── anthropic_json.json       # 期望的非流式 JSON text 响应
    ├── validation_fail.jsonl     # Schema 校验失败的 Kiro 响应
    └── validation_fail_response.json  # 失败时的错误响应
```

### 7.2 Fixture 生成方法

1. **Kiro 上游事件**：从 `translator_test.go` 现有测试提取（已有 120 个测试函数构造了大量 Kiro 事件帧）
2. **Anthropic SSE/JSON**：基于本文档 §1 的规格手工构造，然后用现有测试验证一致性
3. **结构化输出**：手工构造 json_schema 请求 + 工具调用响应

### 7.3 验收标准

| 测试类别 | 验收条件 | 对应 CCTest 项 |
|---------|---------|---------------|
| SSE 事件顺序 | 状态机断言全部通过 | 协议指纹 (5分) |
| Block index 单调递增 | 无跳跃、无重复 | 流式结构 (剩余 5分) |
| message_delta 包含 stop_reason + usage | 非 null 且与内容一致 | 流式结构 |
| 结构化输出 JSON 校验 | Schema 验证通过 | 结构化输出 (10分) |
| 结构化输出无 Markdown 围栏 | 输出为纯 JSON text | 结构化输出 |
| 流式/非流式语义等价 | 相同输入产生语义等价输出 | 协议指纹 + 流式结构 |

### 7.4 Fixture 随机字段处理

- `msg_id`：固定为 `msg_test_000001` 格式
- `toolu_id`：固定为 `toolu_test_000001` 格式
- 时间戳：fixture 中不包含时间戳（SSE 事件本身不含时间戳字段）
- `signature`：固定为 `"test_signature_placeholder"` 或使用确定性 HMAC（固定 key）

---

## 8. 阶段编号对照表

两份前置文档的阶段编号不一致，此处统一：

| 本文档编号 | 原始计划文档编号 | 讨论记录编号 | 内容 |
|-----------|---------------|------------|------|
| **Phase 1 Step 1** | 阶段 2（统一响应模型/流式） | Phase 1 Step 1 | 流式状态机 + 协议指纹修复 |
| **Phase 1 Step 2** | 阶段 3（结构化输出） | Phase 1 Step 2 | 结构化输出 Schema 校验闭环 |
| **Phase 2** | 阶段 1（最小提示词） | Phase 2 Step 3 | 条件化 System Prompt + Thinking |
| **Phase 3** | 阶段 4-5（WebSearch+工具） | Phase 3 | WebSearch + 服务端工具 |

以本文档编号为准。

---

## 9. 并发安全说明

- **流式状态机是 per-request 的**，每次 `StreamEventStreamAsAnthropicWithContext` 调用创建独立的 `AnthropicSSEWriter` 实例
- 没有全局状态共享，不需要加锁
- 现有 `translator.go` 的所有公共函数都是无状态的纯函数或接收独立上下文，并发模型不变

---

## 10. 补充：非流式聚合消息模型

### 10.1 统一内部结果 struct

流式路径使用 `AnthropicSSEWriter` 增量输出，非流式路径需要一个聚合结构收集所有内容块后一次性序列化。两条路径共享同一组内容块类型定义：

```go
// ContentBlockType 表示 Anthropic content block 类型
type ContentBlockType string

const (
    ContentBlockText     ContentBlockType = "text"
    ContentBlockThinking ContentBlockType = "thinking"
    ContentBlockToolUse  ContentBlockType = "tool_use"
)

// ContentBlock 是统一内部内容块模型
type ContentBlock struct {
    Type      ContentBlockType       `json:"type"`
    Text      string                 `json:"text,omitempty"`       // type=text 时
    Thinking  string                 `json:"thinking,omitempty"`   // type=thinking 时
    Signature string                 `json:"signature,omitempty"`  // type=thinking 时的签名
    ID        string                 `json:"id,omitempty"`         // type=tool_use 时
    Name      string                 `json:"name,omitempty"`       // type=tool_use 时
    Input     any                    `json:"input,omitempty"`      // type=tool_use 时
    Source    string                 `json:"-"`                    // "native"/"derived"/"emulated"（不序列化）
}

// AnthropicMessageResult 是非流式路径的统一输出模型
// 流式路径的 AnthropicSSEWriter 也基于相同的 ContentBlock 类型
type AnthropicMessageResult struct {
    ID           string         `json:"id"`
    Type         string         `json:"type"`           // 固定 "message"
    Role         string         `json:"role"`           // 固定 "assistant"
    Model        string         `json:"model"`
    Content      []ContentBlock `json:"content"`
    StopReason   string         `json:"stop_reason"`
    StopSequence *string        `json:"stop_sequence"`
    Usage        MessageUsage   `json:"usage"`
}

// MessageUsage 统一 usage 结构
type MessageUsage struct {
    InputTokens              int `json:"input_tokens"`
    OutputTokens             int `json:"output_tokens"`
    CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
    CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}
```

### 10.2 两条路径的共享关系

```
Kiro 上游事件
    ↓
extractSemanticEvents() → []kiroSemanticEvent
    ↓                          ↓
流式路径                    非流式路径
AnthropicSSEWriter         收集到 []ContentBlock
逐事件增量输出 SSE          ↓
                          AnthropicMessageResult
                          一次性 json.Marshal
```

`ContentBlock` 是两条路径的共同语言。流式路径不构建 `AnthropicMessageResult`，而是用 `AnthropicSSEWriter` 逐块发射。非流式路径先收集全部 `ContentBlock`，再一次性输出。

---

## 11. 补充：Schema 库采用决定

### 采用决定

**正式采用** `github.com/santhosh-tekuri/jsonschema/v6`。

```bash
go get github.com/santhosh-tekuri/jsonschema/v6@v6.0.1
```

### 理由

1. 支持 JSON Schema Draft 2020-12 及所有之前版本
2. 零外部依赖
3. Go 生态中使用最广泛的 JSON Schema 库（GitHub 1.7k+ stars）
4. 积极维护（2024-2026 持续发布）
5. 性能优秀，支持 schema 预编译复用

### 不支持的 Schema 关键字处理

| 情况 | 行为 |
|------|------|
| schema 编译成功 | 正常校验 |
| schema 包含未知关键字 | 编译器默认忽略（宽松模式），不阻塞 |
| schema 编译失败（语法错误） | 跳过校验，直接返回模型输出（降级而非阻塞）；记录警告日志 |
| 校验失败 | 进入修复重试流程（§4.4） |

---

## 12. 补充：异常处理矩阵

### 12.1 取消/超时/断开处理

| 异常场景 | 触发条件 | SSE 已开始输出？ | 处理行为 |
|---------|---------|---------------|---------|
| `context.Canceled` | 客户端主动断开连接 | 是 | 立即停止写入，关闭流，不发 error 事件（客户端已不在） |
| `context.Canceled` | 客户端主动断开连接 | 否 | 返回 HTTP 499（或静默关闭） |
| `context.DeadlineExceeded` | 请求超时 | 是 | 发送 `error` 事件 `{"type":"timeout_error","message":"Request timed out"}`，关闭流 |
| `context.DeadlineExceeded` | 请求超时 | 否 | 返回 HTTP 408 JSON 错误 |
| Kiro 上游读取失败 | `io.ErrUnexpectedEOF` / TCP 断开 / 非 `io.EOF` 错误 | 是 | 发送 `error` 事件 `{"type":"api_error","message":"Upstream connection lost"}`，关闭流 |
| Kiro 上游读取失败 | `io.ErrUnexpectedEOF` / TCP 断开 | 否 | 返回 HTTP 502 JSON 错误 |
| Kiro 上游返回畸形事件 | JSON 解析失败 | 是 | 记录警告日志，跳过该事件，继续处理（best-effort） |
| Kiro 上游返回畸形事件 | 缺少必需字段 | 是 | 记录警告日志，跳过该事件 |
| SSE 写入失败 | `io.Writer.Write` 返回错误 | 是 | 标记 writer 为 error 状态，停止后续写入 |

### 12.2 实现规则

```go
// 每次 Write 调用前检查 context
func (s *AnthropicSSEWriter) writeSSE(event string, data []byte) error {
    if s.err != nil {
        return s.err // 粘滞错误：一旦出错，后续所有写入立即返回
    }
    if err := s.ctx.Err(); err != nil {
        s.err = err
        return err
    }
    // ... 实际写入
}
```

### 12.3 ctx 检查时机

| 时机 | 行为 |
|------|------|
| 每次 `writeSSE` 调用前 | 检查 `ctx.Err()` |
| 每次从 Kiro 上游读取事件后 | 检查 `ctx.Err()` |
| 结构化输出修复重试前 | 检查 `ctx.Err()`，超时则不重试 |

---

## 13. 补充：Writer 边界行为规则

### 13.1 非法状态转换

**策略：Fail-closed**

```go
func (s *AnthropicSSEWriter) checkTransition(event string) error {
    if !s.isValidTransition(event) {
        s.err = fmt.Errorf("illegal SSE transition: %s in state %s", event, s.state)
        // 记录警告日志（含当前 state、事件类型、block index）
        // 不发送 error 事件（状态已经不可信）
        // 标记 writer 为 error 状态
        return s.err
    }
    return nil
}
```

非法转换后：
- Writer 进入 `StateError`（终态）
- 后续所有 Write 调用立即返回已记录的错误（粘滞）
- 不尝试恢复（恢复可能产生更多不合法事件）

### 13.2 粘滞错误

第一个写入错误（无论来源：IO 错误、非法转换、ctx 取消）被存储在 `s.err` 中。所有后续调用立即返回 `s.err`，不执行任何写入。

```go
func (s *AnthropicSSEWriter) WriteTextDelta(text string) error {
    if s.err != nil {
        return s.err  // 粘滞
    }
    // ... 正常逻辑
}
```

### 13.3 Flush 策略

| 事件 | 是否 Flush |
|------|-----------|
| `message_start` | 是 |
| `content_block_start` | 是 |
| `content_block_delta` | 否（批量 delta 不逐个 flush） |
| `content_block_stop` | 是 |
| `message_delta` | 是 |
| `message_stop` | 是 |
| `ping` | 是 |
| `error` | 是 |

如果 `io.Writer` 实现了 `http.Flusher`，在标记为"是"的事件写入后调用 `Flush()`。

### 13.4 空 delta 处理

| 情况 | 行为 |
|------|------|
| `WriteTextDelta("")` | 忽略，不发射事件（空 delta 无意义且浪费带宽） |
| `WriteThinkingDelta("")` | 忽略 |
| `WriteInputJSONDelta("")` | 忽略 |
| `WriteSignatureDelta("")` | 忽略 |

### 13.5 重复操作处理

| 情况 | 行为 |
|------|------|
| 重复调用 `WriteMessageStart` | 非法转换 → fail-closed |
| 重复调用 `StopContentBlock`（同一个块） | 非法转换 → fail-closed |
| 重复调用 `WriteMessageStop` | 非法转换 → fail-closed |
| 在 `StateMessageStopped` 后调用任何 Write | 非法转换 → fail-closed |

### 13.6 上游 EOF（正常结束）

Kiro 上游事件流读完（`io.EOF`，不是 `io.ErrUnexpectedEOF`）时：

1. 如果当前在 `InContentBlock` → 自动发送 `content_block_stop`
2. 如果在 `BetweenBlocks` 或补完 stop 后 → 自动发送 `message_delta`（用累计 usage 和推导的 stop_reason）
3. 发送 `message_stop`
4. 进入 `StateMessageStopped`

即：上游正常 EOF 时，writer 自动补全缺失的收尾事件，确保客户端收到完整的事件序列。

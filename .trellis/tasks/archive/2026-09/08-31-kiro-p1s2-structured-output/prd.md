# Step 2: Structured Output Validation Loop — PRD

## 目标

实现结构化输出 Schema 校验闭环，使 `json_schema` 请求返回经过校验的 JSON text block。预期 CCTest +8~10 分。

## 范围

修改文件：
- `backend/internal/pkg/kiro/translator.go` — 在 `extractStructuredOutputToolText` 后增加 Schema 校验
- `backend/internal/service/kiro_runtime.go` — 增加校验失败时的修复重试逻辑

新增文件：
- `backend/internal/pkg/kiro/schema_validator.go` — JSON Schema 校验封装

新增依赖：
- `github.com/santhosh-tekuri/jsonschema/v6@v6.0.1`

## 关键设计

### 请求路径兼容

支持三种请求格式（见 KIRO_PHASE1_DEV_SPEC.md §4.1）：
- `output_config.format.type=json_schema` — 当前标准
- `output_format` — 已弃用但接受
- `response_format` — OpenAI 兼容

### 校验流程

```
请求 → buildStructuredOutputTool（已有，复用）
     → Kiro 上游工具调用
     → extractStructuredOutputToolText（已有，复用）
     → JSON Schema 校验（新增）
     → 通过 → JSON text block 返回
     → 失败 → 修复重试（最多一次，kiro_runtime 层负责）
     → 仍失败 → 返回 invalid_request_error
```

### Schema 校验器

```go
// schema_validator.go
func ValidateStructuredOutput(jsonText string, schemaJSON []byte) error
```

- 编译成功 → 正常校验
- 编译失败（schema 语法错误）→ 跳过校验，降级返回原始输出 + 警告日志
- 校验失败 → 返回 validation error 含具体字段信息

### 修复重试

- 由 `kiro_runtime.go` 负责
- Prompt: `"The previous JSON output did not satisfy the schema. Specific error: {error}. Please output valid JSON."`
- 不重新注入内部工具
- 超时继承原始请求
- Token 计入客户端 usage
- 最多一次

### 流式特殊处理

流式模式下，结构化输出必须完全缓冲工具参数后再校验：
- 先缓冲完整 JSON（不提前输出未验证内容）
- 校验通过后一次性发送 text block 的 delta 序列
- 校验失败走修复重试流程

## Feature Flag

`SUB2API_KIRO_STRUCTURED_OUTPUT_VALIDATION=false`（默认关闭）

## 验收标准

1. `json_schema` 请求 → 返回合法 JSON text block
2. JSON 满足请求中的 schema
3. 无 Markdown 围栏、无注释、无额外文本
4. 流式和非流式输出语义等价
5. Schema 校验失败 → 一次修复 → 成功则返回
6. 修复也失败 → 返回 `invalid_request_error`
7. Schema 编译失败 → 降级返回原始输出
8. Golden Fixture structured_output 测试通过
9. Flag 关闭时走原始代码路径
10. 现有测试无回归

## 参考

- KIRO_PHASE1_DEV_SPEC.md §4（结构化输出规格）、§10（聚合模型）、§11（Schema 库）

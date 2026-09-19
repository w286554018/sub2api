# Step 1: SSE State Machine + Protocol Fingerprint — PRD

## 目标

抽取 AnthropicSSEWriter 序列化层，引入流式状态机，修复协议指纹。预期 CCTest +5~8 分。

## 范围

修改文件：
- `backend/internal/pkg/kiro/translator.go` — 从 `StreamEventStreamAsAnthropicWithContext` 中抽取序列化逻辑
- 新增 `backend/internal/pkg/kiro/sse_writer.go` — `AnthropicSSEWriter` 实现

不修改文件：
- `gateway_forward.go`、`kiro_runtime.go` — 调用方签名不变

## 关键设计

- 7 个状态：Init → MessageStarted → InContentBlock → BetweenBlocks → MessageDelta → MessageStopped → Error
- 10 条状态转换规则（见 KIRO_PHASE1_DEV_SPEC.md §3.2）
- 9 条不变量断言（见 §3.3）
- Fail-closed 非法转换策略（见 §13.1）
- 粘滞错误（见 §13.2）
- Flush 策略：block 边界事件 flush，delta 不 flush（见 §13.3）
- ctx 检查：每次 writeSSE 前检查 context（见 §12）
- EOF 收尾：正常 `io.EOF` 自动补全缺失事件（见 §13.6）

## Feature Flag

`SUB2API_KIRO_SSE_STATE_MACHINE=false`（默认关闭）

## 验收标准

1. 4 组 Golden Fixture SSE 输出匹配
2. 状态机断言测试全部通过
3. 非法转换 → fail-closed 测试
4. 空 delta 忽略测试
5. EOF 自动收尾测试
6. 现有 120 个测试无回归
7. Flag 关闭时走原始代码路径

## 参考

- KIRO_PHASE1_DEV_SPEC.md §1（SSE 事件规格）、§2（Kiro 映射）、§3（状态机设计）、§12（异常处理）、§13（边界行为）

# Kiro Phase 2: Conditional System Prompt Injection + Thinking — PRD

## 目标

条件化 System Prompt 注入 + 改进 Thinking 内容传递。预期 CCTest +3~5 分。

## 背景

当前 `buildInjectedSystemPrompt`（translator.go L1492）无条件注入：
- Kiro 身份覆盖提示（`<CRITICAL_OVERRIDE>`）
- 时间上下文
- 分块写入策略（`systemChunkedWritePolicy`）
- Thinking mode XML 标签
- 工具选择提示

问题：
- 普通聊天也被注入编程助手身份，扭曲模型行为
- 浪费 input tokens
- 与用户 system prompt 冲突风险
- CCTest "提示词注入"项虽暂时通过但边界场景不稳定

## 范围

### Step 1: 条件化 System Prompt 注入

修改 `buildInjectedSystemPrompt` 使其按需注入：
- 普通聊天：零注入（或仅保留 Kiro 协议必需的最小控制）
- 工具请求：注入工具选择提示
- 结构化输出：注入结构化输出提示
- Thinking 请求：注入 thinking 控制标签
- 身份提示：默认不注入（让模型保持原生 Claude 行为）
- 分块写入策略：仅当请求包含文件工具时注入

### Step 2: Thinking 内容传递改进

- 优先使用 Kiro 上游的结构化 `reasoningContentEvent` 事件
- 减少对 `<thinking>` 文本标签正则解析的依赖
- 保持现有 HMAC 合成签名（Phase 1 决策：不在此阶段引入 strict 模式）

## Feature Flag

`SUB2API_KIRO_CONDITIONAL_INJECTION=false`（默认关闭）

## 验收标准

1. Flag 开启时，无工具/无结构化输出/无 thinking 的普通聊天请求注入 token 数显著减少
2. 工具/结构化输出/thinking 请求仍正确注入必要提示
3. 现有测试无回归
4. CCTest 提示词注入项保持通过
5. Flag 关闭时行为零变化

## 参考

- KIRO_PHASE1_DEV_SPEC.md §5（Thinking 块规格）
- KIRO_PROTOCOL_IMPROVEMENT_DISCUSSION.md 第二轮/第三轮（条件化注入共识）
- translator.go L1492 `buildInjectedSystemPrompt`

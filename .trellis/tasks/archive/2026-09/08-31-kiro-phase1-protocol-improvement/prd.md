# Kiro Phase 1: Protocol Improvement — PRD

## 目标

将 Kiro 反代链路的 CCTest 得分从 50/100 提升到 65-68/100，通过改进协议转换层的流式输出质量和结构化输出能力。

## 背景

- 当前 CCTest 基线：50/100（2026-08-26）
- 报告地址：`https://cctest.ai/zh/result/fd514e3e-3611-43bd-95b3-d9d75597601b`
- 零分项：协议指纹(0/5)、流式结构(5/10)、结构化输出(0/10)、Thinking签名(0/10)、WebSearch(0/10)、服务端工具(0/10)
- 本阶段聚焦：协议指纹 + 流式结构 + 结构化输出

## 红线约束

- 不能破坏现有 Kiro 反代功能
- 所有新功能通过 Feature Flag 控制，默认关闭
- 不修改现有公共函数签名

## 子任务

### Step 1: SSE 状态机 + 协议指纹修复（3-4 天）

- 抽取 `AnthropicSSEWriter` 序列化层
- 引入显式流式状态机（7 状态 + 10 条转换规则）
- 确保 SSE 事件序列严格合法
- 修复 block index 单调递增、message_delta 包含 stop_reason + usage
- 预期收益：协议指纹 +5，流式结构修复 +2~3

### Step 2: 结构化输出 Schema 校验闭环（4-5 天）

- 集成 `santhosh-tekuri/jsonschema/v6` 做 Schema 校验
- 在 `extractStructuredOutputToolText` 后增加校验步骤
- 校验通过 → JSON text block；失败 → 一次修复重试 → 仍失败返回错误
- 流式模式下完全缓冲后再输出
- 预期收益：结构化输出 +8~10

## 验收标准

1. 4 组 Golden Fixture 测试全部通过（plain_text/thinking/tool_use/structured_output）
2. 状态机 9 条不变量断言全部通过
3. 流式与非流式输出语义等价
4. Feature Flag 关闭时行为零变化
5. 现有 120 个 translator 测试无回归

## 参考文档

- `docs/KIRO_PHASE1_DEV_SPEC.md` — 实施规格说明书（904 行，13 章）
- `docs/KIRO_ANTHROPIC_COMPATIBILITY_PLAN.md` — 原始兼容性计划
- `docs/KIRO_PROTOCOL_IMPROVEMENT_DISCUSSION.md` — 多模型讨论记录

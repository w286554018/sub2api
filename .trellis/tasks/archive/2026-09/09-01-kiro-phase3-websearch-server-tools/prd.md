# Kiro Phase 3: WebSearch + Server Tools — PRD

## 目标

将 WebSearch 改造为模型参与的搜索循环，完善服务端工具语义。基于 Phase 1/2 的协议基础。

## 背景

CCTest 当前零分项（Phase 1/2 未覆盖）：
- WebSearch: 0/10
- 服务端工具: 0/10

当前实现（从代码调查）：
- `websearch.go`（368 行）：工具定义替换、结果注入
- `websearch_stream.go`（297 行）：流式搜索缓冲分析、SSE 过滤
- `service/kiro_websearch.go`（475 行）：服务层搜索执行
- `service/gateway_websearch_emulation.go`（471 行）：网关搜索模拟

现有问题：搜索由网关直接执行，模型不参与搜索词选择和结果分析。Anthropic 的 WebSearch 是 `server_tool_use` + `web_search_tool_result` 专用 block，Kiro 没有对应协议。

## 子任务

### Step 1: WebSearch 模型参与循环

将"网关直接搜索"改为"模型驱动搜索"：
1. 模型决定搜索词（通过 tool_use 请求 web_search）
2. 网关执行搜索
3. 结果作为 tool_result 回传模型
4. 模型基于结果生成最终答案
5. 引用指向真实搜索结果

保留旧快捷搜索为 fallback（feature flag 控制）。

### Step 2: 服务端工具语义

- `server_tool_use` content block 类型支持
- `web_search_tool_result` 响应 block 构建
- 工具定义翻译（Anthropic `web_search_20260209` → Kiro 内部表示）
- 多轮工具调用循环
- 稳定 tool ID 生成
- 超时控制和错误处理

## 红线约束

- 不能破坏现有 Kiro 反代功能
- 旧搜索路径通过 Feature Flag 保留
- 不伪造搜索结果为 Anthropic 原生数据（标记为本地模拟）

## 预期效果

- CCTest WebSearch: 0 → 部分得分（搜索循环 + 引用）
- CCTest 服务端工具: 0 → 部分得分（block 类型 + 外形兼容）

## 参考

- KIRO_PROTOCOL_IMPROVEMENT_DISCUSSION.md — 第二轮/第三轮 WebSearch 讨论
- KIRO_PHASE1_DEV_SPEC.md §2（Kiro 上游事件映射）
- Anthropic API: `web_search_20260209` 类型定义
- 现有代码：`websearch.go`、`websearch_stream.go`、`kiro_websearch.go`

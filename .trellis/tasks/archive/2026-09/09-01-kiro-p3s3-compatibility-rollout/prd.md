# Step 3: Compatibility and Rollout — PRD

## 目标

端到端验证 Phase 3 所有改动，建立 golden fixture，实现三层 Feature Flag 灰度，确保上游兼容性和安全回滚能力。

## 依赖

- Step 1（搜索编排）和 Step 2（协议投影）都完成后启动

## 范围

### 1. Golden Fixture 测试

在 `testdata/golden/` 下新增（Step 0 创建目录，Step 3 填充完整内容）：

| 场景 | 文件 | 覆盖点 |
|------|------|--------|
| 单轮搜索 | kiro_events + anthropic_sse + anthropic_json | 基本搜索 → server tool block |
| 多轮搜索 | 同上 | 2+ 轮循环，每轮 ID 不同 |
| 搜索无结果 | 同上 | 空结果处理 |
| 搜索失败 | 同上 | MCP 超时/错误降级 |
| 搜索 + 用户工具 | 同上 | 混合工具分流 |
| 搜索 + thinking | 同上 | thinking block + server tool block 共存 |

### 2. CCTest Replay

- 构造 CCTest WebSearch 检测场景的请求
- 验证响应满足 CCTest 检查点：
  - `server_tool_use` block 存在且格式正确
  - `web_search_tool_result` block 包含搜索结果
  - 引用指向真实 URL
  - 流式事件顺序合法

### 3. 上游兼容性测试

| 场景 | 验证 |
|------|------|
| Kiro 上游不认识 server_tool_use | 历史消息过滤正确，不发 400 |
| 多轮对话含搜索历史 | FilterWebSearchHistoryBlocks 正确剥离 |
| 搜索后模型继续对话 | 不遗留脏状态 |
| 不同模型（GPT-5.6 vs Claude） | 搜索工具翻译适配 |

### 4. 三层 Feature Flag

| Flag | 控制层 | 默认值 |
|------|--------|--------|
| `SUB2API_KIRO_WEB_SEARCH_EMULATION` | 是否允许网关接管搜索 | 现有默认值 |
| `SUB2API_KIRO_WEBSEARCH_MODEL_LOOP` | 是否启用模型参与多轮搜索 | `false` |
| `SUB2API_KIRO_SERVER_TOOL_PROJECTION` | 是否输出 server_tool_use/web_search_tool_result blocks | `false` |

灰度策略：
- 先单个测试账号开启 `MODEL_LOOP`
- 验证搜索循环稳定后开启 `SERVER_TOOL_PROJECTION`
- 确认协议兼容后逐步扩大范围
- 任何异常：关闭单个 flag，不影响其他功能

### 5. 监控指标

| 指标 | 说明 |
|------|------|
| `kiro_websearch_rounds_total` | 搜索轮数分布 |
| `kiro_websearch_duplicate_query` | 重复 query 次数 |
| `kiro_websearch_mcp_success_rate` | MCP 搜索成功率 |
| `kiro_websearch_fallback_count` | 降级到 HTTP 搜索次数 |
| `kiro_websearch_timeout_count` | 超时次数 |
| `kiro_websearch_avg_latency_ms` | 平均额外搜索延迟 |
| `kiro_server_tool_block_violations` | SSEValidator 违规次数 |

### 6. 回滚方案

| 触发条件 | 动作 |
|---------|------|
| 搜索成功率 <80% | 关闭 `MODEL_LOOP`，回退到网关搜索 |
| SSE 违规率 >1% | 关闭 `SERVER_TOOL_PROJECTION` |
| 上游 400 增加 | 检查历史消息过滤，必要时关闭全部搜索 flag |
| 延迟 p99 >10s | 降低 MaxRounds / 缩短超时 |

## 验收标准

1. 6 组 golden fixture 全部通过
2. CCTest WebSearch 场景得分 >0（目标部分得分）
3. 上游兼容性 4 个场景全部通过
4. 三层 flag 独立控制验证
5. 每个 flag 关闭时行为零变化
6. 所有监控指标可采集
7. 回滚方案经测试可执行
8. Phase 1/2 现有测试无回归

## 参考

- Step 0/1/2 的输出
- Phase 1 的 SSEValidator（协议验证）
- Phase 2 的条件化注入（搜索不应触发身份注入）
- CCTest WebSearch 检测项规格

# Kiro 转 Anthropic 兼容性与能力提升计划

## 1. 文档目的

本文记录目前观察到的 CCTest 检测项目，将这些检测项目与本项目现有 Kiro 转换逻辑对应起来，并制定一套分阶段的改进方案，用于提升真实的 Anthropic API 兼容性以及 Claude 的实际任务能力。

本计划的目标不是把 Kiro 伪装成 Anthropic 官方接口，而是：

- 尽可能保留 Kiro 上游 Claude 已经具备的能力；
- 减少会改变模型行为的隐藏提示词注入；
- 生成内部语义一致的 Anthropic 兼容请求和响应；
- 在上游能力允许的范围内，正确支持结构化输出、工具调用、网页搜索、流式响应、多模态输入和用量统计；
- 明确区分上游原生数据、本地推导数据和本地模拟数据。

## 2. 当前检测基线与参考样本

### 2.1 CCTest 检测报告

检查时间：2026 年 8 月 26 日。

- 报告地址：`https://cctest.ai/zh/result/fd514e3e-3611-43bd-95b3-d9d75597601b`
- 请求模型：`claude-opus-5`
- 响应模型：`claude-opus-5`
- 总分：`50/100`
- 判定：`reversed`，即疑似逆向渠道
- 是否检测到 Vertex 特征：否
- 本次是否启用 Token 用量审计：否

检测得分如下：

| 检测项目 | 得分 | 满分 | 结果 |
| --- | ---: | ---: | --- |
| LLM/标签指纹 | 10 | 10 | 通过 |
| 流式结构 | 5 | 10 | 部分通过 |
| 非流式结构 | 5 | 5 | 通过 |
| WebSearch | 0 | 10 | 失败 |
| Thinking 签名/协议 | 0 | 10 | 失败 |
| 结构化输出 | 0 | 10 | 失败 |
| 服务端工具 | 0 | 10 | 失败 |
| 提示词/Token 注入 | 5 | 5 | 本次探针通过 |
| 知识行为 | 5 | 5 | 通过 |
| 文档识别 | 10 | 10 | 通过 |
| 图片识别 | 10 | 10 | 通过 |
| 协议指纹 | 0 | 5 | 失败 |

这个结果说明，当前链路已经具备基础 Claude 行为和多模态能力，但 Anthropic 高级协议能力不完整，或者部分能力是由本地转换层重新构造出来的。

### 2.2 两个中转 Key 的对比样本

2026 年 8 月 27 日，使用同一个接口 `https://api.vibelearning.top`、同一个模型 `claude-opus-5` 对两条链路进行了低额度测试。这里不记录 Key 本身，只记录外部可观察结果。

#### 样本 A：需要 Claude Code 客户端的链路

- 直接发送标准 `/v1/messages` 请求时返回 `503 model_not_found` 或客户端异常提示；
- 使用标准 Claude Code `2.1.220` 后可以成功调用；
- 普通调用的流式事件中，响应模型曾显示为 `claude-opus-4-6-thinking`，与请求的 `claude-opus-5` 不一致；
- usage 中出现 `input_tokens=0`、缓存 Token 单独存在的情况；
- 说明该链路存在客户端校验、模型映射或额外包装。

#### 样本 B：高兼容逆向链路

- 直接使用 Anthropic 协议请求即可成功，不依赖 Claude Code；
- 非流式和流式响应均保持 `model=claude-opus-5`；
- 响应含 thinking block、signature、`thinking_tokens`；
- 流式顺序为 `message_start → thinking → signature_delta → text → message_delta → message_stop`，并包含 `ping`；
- `output_config.format.type=json_schema` 可以直接返回满足 schema 的 JSON text；
- 强制工具调用返回标准 `tool_use`、工具参数和 `stop_reason=tool_use`，并保留 `caller.type=direct`；
- usage 含 `input_tokens`、`output_tokens`、缓存字段和 `output_tokens_details.thinking_tokens`。

样本 B 应作为本项目的**高兼容行为参考基线**。它用于确定请求、响应、SSE、thinking、工具和结构化输出的目标形态；不要求 Kiro 在没有对应原生能力时伪造官方签名或加密内容。

### 2.3 CCTest 能证明什么

CCTest 做的是黑盒行为检测和协议兼容性检测。它可以反映一个接口从外部观察时是否接近 Anthropic API，但不能通过密码学方式证明该接口一定直连 Anthropic 官方服务器。

可信度较高的信号包括：

- 原始 SSE 事件顺序和 content block 结构；
- 上游原生 thinking 与 signature 行为；
- 服务端工具专用内容块；
- 结构化输出是否真正受到约束；
- 多轮请求中 usage 和缓存 Token 统计是否一致。

偏启发式的信号包括：

- 回答风格；
- 模型身份回答；
- 知识边界问题；
- 提示词注入探针；
- 针对具体模型设计的行为题目。

## 3. CCTest 检测手段分析

### 3.1 标签和行为指纹

可能检查的内容：

- 是否按要求返回固定标签或输出格式；
- 存在冲突指令时的指令遵循情况；
- 是否泄漏隐藏的 system prompt；
- 是否反复出现某种代理或包装层特有话术；
- 请求模型名和响应模型名是否一致。

这部分主要是行为启发式检测。正确的改进方式是减少不必要的请求转换和提示词注入，而不是识别 CCTest 提示词后进行特殊回答。

### 3.2 流式响应结构

可能检查的内容：

- `message_start` 是否只出现一次，并且位于所有内容块之前；
- 每个内容块是否严格包含一次 `content_block_start`、零到多次合法 delta、一次 `content_block_stop`；
- block index 是否单调递增，并与最终非流式 content 顺序一致；
- delta 类型是否与当前内容块类型匹配；
- `message_delta` 是否包含合法的最终 `stop_reason` 和累计 usage；
- `message_stop` 是否为正常响应的最后一个事件；
- thinking、text、tool block 之间是否正确切换且不存在重叠；
- 上游出现异常帧时，是否转换为 Anthropic 兼容的流式错误；
- 同一个请求的流式和非流式结果在语义上是否一致。

### 3.3 非流式响应结构

可能检查的内容：

- `id`、`type`、`role`、`model`、`content`、`stop_reason`、`stop_sequence`、`usage` 是否合法；
- content block 类型及其必填字段是否完整；
- tool ID 和 tool input 是否一致；
- 是否出现 `_sub2api_*` 一类私有实现字段；
- 与同等流式请求的最终结果是否一致。

### 3.4 Thinking 和签名协议

可能检查的内容：

- 请求 thinking 后，在支持的模型上是否返回 `thinking` block；
- 流式响应是否先发送 `thinking_delta`，然后发送 `signature_delta`；
- signature 是否位于正确的 thinking block；
- 非流式响应是否保留对应 signature；
- 后续对话是否可以把 signature 原样传回；
- 如果上游返回 redacted thinking，是否把它当作不透明内容块原样保留。

Anthropic signature 是上游生成的不透明数据。本地 HMAC，或者外形类似 Anthropic signature 的字符串，都不等同于官方原生签名。

### 3.5 结构化输出

可能检查的内容：

- 是否接受 `output_config.format.type=json_schema`；
- 最终 assistant text 是否为合法 JSON；
- JSON 是否满足客户端要求的 schema；
- 是否没有 Markdown 代码围栏、说明文字或尾部多余文本；
- 流式和非流式结果是否一致；
- 遇到拒答或截断时，是否避免把无效 JSON 当作成功结果；
- strict tool schema 与 response format schema 是否分别正确处理。

### 3.6 客户端工具和服务端工具

客户端工具可能检查：

- `tool_choice` 的 `auto`、`any`、`tool`、`none` 模式；
- 是否能够强制选择指定工具；
- 单个或多个工具调用；
- `input_json_delta` 是否正确；
- `tool_use` 和 `tool_result` 之间是否保留同一个工具 ID；
- 工具调用结束时是否正确返回 `stop_reason=tool_use`。

服务端工具可能检查：

- 是否使用服务端工具专用 block，而不是普通客户端 `tool_use`；
- 工具结果 block 类型及其与调用之间的关系是否正确；
- 是否存在准确的服务端工具使用次数统计；
- 工具错误和最大调用次数行为是否正确。

### 3.7 WebSearch

可能检查的内容：

- 是否能识别 Anthropic WebSearch 工具声明；
- 搜索词是否由模型选择，而不是网关直接把用户最后一句话当成搜索词；
- 是否先出现 `server_tool_use`，再出现 `web_search_tool_result`；
- 搜索结果 block、引用和多次搜索行为是否正确；
- 后续对话是否能够原样保留不透明的搜索结果内容；
- 是否存在正确的服务端工具用量统计；
- 流式事件顺序是否正确。

### 3.8 提示词和 Token 注入

可能检查的内容：

- 隐藏指令是否改变用户原本要求的任务；
- 模型是否泄漏代理身份或内部规则；
- 大量隐藏 prompt 是否导致可观察的回答变化或 Token 变化；
- 用户指令和 system 指令的优先级是否偏离预期的 Anthropic API 行为。

本次报告中 `token_inject` 通过，只能说明这组探针没有发现问题，不能证明当前链路没有注入 system prompt。

### 3.9 知识和多模态行为

可能检查的内容：

- 针对模型设计的知识和推理任务；
- 图片内容识别；
- PDF 或文档解析；
- 输入 block 是否能被接受并正确转发；
- 请求模型声明的能力与实际观察到的行为是否一致。

### 3.10 Token 和缓存审计

CCTest 提供可选的多轮 Token 审计，目前页面显示大约会发送 11 轮请求。审计内容包括：

- input tokens；
- output tokens；
- cache creation input tokens；
- cache read input tokens；
- 随对话轮次增加时的 Token 增长；
- 缓存命中率和实际消费倍率。

本项目有一个特殊约束：原生 Kiro 不提供 Anthropic 缓存，因此**缓存模拟需要保留**，用于稳定计费、降低重复上下文的成本估算，并尽量接近高兼容参考样本的公开 usage 形态。缓存模拟必须在内部明确标记为 `emulated`，不能声称是 Kiro 上游真实返回的缓存统计。

## 4. 当前项目实现对照

### 4.1 System prompt 构造

相关代码：

- `backend/internal/pkg/kiro/translator.go` 中的 `buildInjectedSystemPrompt`
- `backend/internal/pkg/kiro/translator.go` 中的 `prependSystemHistory`

当前行为包括：

- 加入 Kiro 内置身份提示词；
- 可选的当前时间上下文；
- 工具选择和结构化输出提示；
- 分块写入策略；
- thinking 控制标签；
- 将组合后的提示词作为 Kiro 对话历史插入。

风险：

- 隐藏指令可能与调用者的 system prompt 竞争；
- 普通聊天能力可能被限制成编程助手风格；
- 实际输入上下文比调用者提供的内容更大；
- 工具选择和结构化输出依赖提示词，而不是协议级约束；
- Token 统计可能与客户端预期不一致。

### 4.2 Thinking 转换和本地签名生成

相关代码：

- `backend/internal/pkg/kiro/translator.go` 中的 reasoning 事件解析和 thinking block 输出
- `backend/internal/pkg/kiro/signature.go` 中的 `thinkingSignature`

当前转换器会从 Kiro reasoning 事件或 `<thinking>` 文本中提取思考内容，然后重建 Anthropic thinking block，并使用本地 HMAC 生成 signature。

风险：

- 本地 signature 不是 Anthropic 上游签发的不透明签名；
- 重建 thinking 时可能丢失上游原始边界或脱敏语义；
- 检测器可以区分本地构造的协议数据和上游原生数据；
- 客户端可能误以为该 signature 可以被 Anthropic 验证。

### 4.3 结构化输出适配器

相关代码：

- `backend/internal/pkg/kiro/translator.go` 中的 `buildStructuredOutputTool`

当前适配器会把 JSON Schema 转换成内部强制工具，并加入提示词要求模型调用该工具。这个思路可以作为 Kiro 不支持原生 response format 时的降级方案，但目前缺少完整的验证和响应转换闭环。

当前风险：

- 只靠提示词的 `json_object` 模式不能保证一定返回合法 JSON；
- 工具参数可能以 tool call 形式返回，而不是最终 JSON text；
- 本地可能没有验证 schema；
- 流式和非流式行为可能不同；
- 无效或被截断的 JSON 可能被当作成功响应返回。

### 4.4 普通工具转换

相关代码：

- `backend/internal/pkg/kiro/translator.go` 中的 Claude 转 Kiro 工具逻辑
- `backend/internal/pkg/kiro/translator.go` 中的流式工具参数缓冲和输出逻辑

当前转换器已经支持不少普通工具场景。仍需要重点检查：

- 强制工具选择；
- 并行工具调用；
- 被截断的工具输入；
- thinking 与工具 block 的边界；
- 流式和非流式是否完全等价。

### 4.5 WebSearch 模拟

相关代码：

- `backend/internal/service/gateway_websearch_emulation.go`
- `backend/internal/pkg/kiro/websearch.go`
- `backend/internal/pkg/kiro/websearch_stream.go`

目前存在两套相关机制：

- 网关拦截只有 WebSearch 工具的请求，直接执行搜索并构造 Anthropic 形状的结果；
- 检测 Kiro 返回的搜索工具调用，执行外部搜索，再把 tool result 注入对话并补充响应 block。

风险：

- 网关直接生成摘要时，跳过了模型正常的搜索和推理循环；
- 本地构造的 block 可能缺少官方原生字段和语义；
- 搜索结果不是 Anthropic 原生的 encrypted content；
- 引用和多次搜索行为可能与官方接口不同；
- usage 可能缺少服务端工具专用计数。

### 4.6 Usage 和缓存统计

相关代码：

- `backend/internal/pkg/kiro/translator.go` 中的上游事件 usage 提取和本地估算兜底
- 网关路径中使用的 Kiro 缓存模拟逻辑

当前实现会合并：

- 上游返回的 usage；
- 本地 Token 估算；
- 本地缓存模拟统计。

风险：

- 如果没有来源标记，客户端无法区分实际测量值和估算值；
- 隐藏 prompt 的 Token 可能与公开 input tokens 对不上；
- 流式和非流式总量可能不同；
- 私有 usage 字段可能破坏严格 Anthropic 协议兼容性；
- 多轮缓存行为可能无法通过 Token 审计，或者与高兼容参考样本的倍率差异过大。

缓存模拟不是本计划要删除的功能，而是需要重新设计为可解释、可重复、可配置的兼容层能力。

## 5. 分阶段改进方案

### 阶段 0：建立可重复的基线

目标：在修改行为前，保存可以复现问题的原始证据。

操作：

- 保存每个兼容性探针的完整请求和原始响应；
- 同时记录流式和非流式版本；
- 记录实际映射后的 Kiro 模型 ID 和账号类型；
- 同时保存样本 A、样本 B 和当前 `sub2api` 的脱敏对比结果；
- 只在受控诊断环境中开启 `KIRO_UPSTREAM_TRACE=1`；
- 保存日志前清除 API Key、Token 和用户敏感内容；
- 同一探针至少运行三次，用于识别随机性失败；
- 使用专门的低风险测试账号开启 CCTest Token 审计；
- 保存原始协议观察结果，不要只保存最终分数和结论。

建议基线矩阵：

| 场景 | 流式 | Thinking | 工具 | 媒体 | 预期观察 |
| --- | --- | --- | --- | --- | --- |
| 普通文本 | 否/是 | 否 | 否 | 否 | 最终文本和 usage 等价 |
| Thinking 文本 | 否/是 | 是 | 否 | 否 | thinking/text 边界稳定 |
| 强制工具 | 否/是 | 可选 | 单个 | 否 | tool ID、JSON 和 stop reason 正确 |
| 并行工具 | 否/是 | 可选 | 多个 | 否 | 所有工具调用按顺序保留 |
| JSON Schema | 否/是 | 可选 | 内部适配器 | 否 | 返回符合 schema 的合法 JSON text |
| WebSearch | 否/是 | 可选 | 服务端搜索 | 否 | 搜索循环和引用 block 正确 |
| 图片 | 否/是 | 可选 | 否 | 图片 | 图片识别和 usage 正确 |
| PDF | 否/是 | 可选 | 否 | PDF | 文档识别和 usage 正确 |
| 多轮缓存 | 否/是 | 可选 | 可选 | 否 | 模拟缓存命中、计费和 Token 统计稳定 |

### 阶段 1：增加最小提示词模式

目标：保留 Kiro 必需控制的同时，移除可以避免的模型行为限制。

设计：

- 将上游必需提示词与可选兼容性提示分开；
- 为 Kiro 账号增加明确的最小提示词策略；
- 保留调用者 system prompt 的原始含义，不进行身份覆盖；
- 只有请求 thinking 或模型确实需要时才添加 thinking 控制；
- 只有 Kiro 无法通过结构字段表达 tool choice 时，才添加工具选择提示；
- 只有结构化输出请求才加入结构化输出提示；
- 普通聊天请求中不加入编程、写文件或分块写入策略；
- 除非 Kiro 协议强制要求，否则避免伪造 assistant 历史；
- 只在内部诊断信息中记录使用了哪种提示词策略，不写进公开响应。

验收标准：

- 普通文本回答不再被固定为编程助手身份；
- 用户 system prompt 保持预期优先级；
- 工具调用和结构化输出功能继续工作；
- 无法解释的隐藏 prompt Token 明显减少；
- 现有图片、PDF 和普通工具测试继续通过。

### 阶段 2：建立统一的内部响应模型

目标：让流式和非流式响应从同一个语义结果生成。

设计：

- 将 Kiro 事件解析成统一的内部消息模型；
- 分别表示 text、thinking、redacted thinking、客户端工具、服务端工具和搜索结果；
- 为每个字段保存来源：上游原生、本地推导、本地估算或本地模拟；
- 从统一消息模型分别生成 JSON 和 SSE；
- 在写给客户端前检查 content block 状态转换是否合法；
- 对上游不支持的能力明确降级或报错，不静默编造数据。

验收标准：

- 流式重建后的 block 顺序和最终 stop reason 与非流式一致；
- 每个打开的 block 恰好关闭一次；
- content index 单调递增；
- 累计 usage 只在合法协议位置输出；
- 严格 Anthropic 响应中不包含私有字段。

### 阶段 3：完成结构化输出闭环

目标：通过 Kiro 工具降级方案，提供真正满足 schema 的结构化输出。

设计步骤：

1. 将客户端 JSON Schema 标准化为 Kiro 工具支持的 schema 子集。
2. 使用不会与客户端工具冲突的内部工具名。
3. 如果上游支持强制工具选择，强制调用内部结构化输出工具。
4. 缓冲并解析完整的工具参数。
5. 使用客户端原始 schema 在本地进行验证。
6. 验证成功后，将工具参数转换为标准 JSON assistant text。
7. 如果验证失败且配置允许，最多进行一次受限修复请求。
8. 修复仍失败时返回明确协议错误。
9. 流式模式下，不把无效的局部 JSON 当作成功结果提前输出。
10. 客户端 strict tool schema 与 assistant response schema 分开处理。

验收标准：

- 返回的 JSON 没有 Markdown 围栏或说明文字；
- required、类型、enum、数组和嵌套对象均经过验证；
- 无效或截断结果不会被报告为成功；
- 流式和非流式返回等价 JSON；
- 客户端工具名不会与内部适配器工具冲突。

### 阶段 4：完善普通工具调用语义

目标：保留完整工具工作流，而不是只输出外形相似的 block。

设计：

- 实现并测试所有受支持的 `tool_choice` 模式；
- Kiro 安全名称映射后仍能恢复客户端原始工具名；
- 工具调用和工具结果之间保持稳定 ID；
- 上游产生并行工具调用时全部保留；
- 适当情况下，对孤立 tool result 返回明确客户端错误；
- 区分工具 JSON 不完整、JSON 被截断和根本没有工具调用；
- 除非协议明确支持交错，否则 thinking block 必须在工具 block 开始前关闭；
- 只有至少输出一个合法工具 block 时才设置 `stop_reason=tool_use`；
- 除非 Kiro 上游明确要求，不要因为强制工具调用就全局关闭 thinking。

验收标准：

- 强制工具和自动工具在流式、非流式下行为一致；
- 多工具调用不会被错误重排、合并或去重；
- 后续 tool result 请求保持原始调用关系；
- 非法工具参数不会被静默丢弃后仍返回成功。

### 阶段 5：将快捷搜索改成模型参与的搜索循环

目标：同时提升真实搜索质量和协议一致性。

推荐流程：

1. 让模型生成或确认搜索词。
2. 通过已配置的搜索提供商执行查询。
3. 将结果转换成本地不透明搜索结果对象。
4. 将搜索结果作为 tool result 回传给模型。
5. 由模型根据检索结果生成最终回答。
6. 为最终回答附加能够追溯到真实搜索结果的引用。

额外要求：

- 支持配置范围内的多次搜索；
- 将搜索提供商失败表示为明确工具错误；
- 在上游支持时处理域名和结果数量限制；
- 后续对话原样保存本地不透明搜索结果；
- 搜索调用次数单独用于计费和诊断；
- 只有含义准确时才输出服务端工具 usage 字段；
- 不把本地不透明内容标记成 Anthropic encrypted content。

验收标准：

- 模型收到搜索结果后再生成最终答案；
- 引用指向实际返回的搜索结果；
- 多次搜索和错误重试具有确定的限制；
- 流式和非流式 block 顺序符合项目定义的本地协议；
- 原生 Anthropic 语义不可用时，明确说明该功能属于模拟实现。

### 阶段 6：保留上游原生推理数据并移除伪签名

目标：提升推理内容保真度，同时不宣称不存在的官方真实性。

设计：

- 检查 Kiro 上游所有 reasoning 事件字段，并在诊断数据中保留未知不透明字段；
- 优先使用原生 reasoning 事件，不优先依赖 `<thinking>` 文本标签解析；
- 如果 Kiro 将来返回原生 signature，必须原样保存和透传；
- 将 redacted reasoning 作为不透明数据保留；
- 没有原生 signature 时，在严格模式中省略 signature，或在 Anthropic 响应以外提供能力说明；
- 严格兼容模式中删除本地生成的 Anthropic 形状 signature；
- 如果旧客户端确实依赖合成签名，可保留 legacy 模式，但必须明确标记为 synthetic。

验收标准：

- 不使用本地生成数据替换上游原生 reasoning；
- 不把本地签名表示成 Anthropic 已认证签名；
- thinking、text 和 tool 顺序稳定；
- 上游存在原生不透明推理数据时，后续对话可原样回传。

已知限制：

如果 Kiro 不提供真实的上游 thinking signature，仅靠协议转换无法让 CCTest signature 检测等同于 Anthropic 官方接口。

### 阶段 7：保留并重设计缓存模拟与 Usage 来源

目标：原生 Kiro 不提供 Anthropic 缓存，因此保留缓存模拟；同时让 usage 可解释、可重复，并尽量接近样本 B 的高兼容响应形态。

设计：

- 分开记录上游返回、本地估算和缓存模拟 usage，并为每项记录 `source=upstream|estimated|emulated`；
- Kiro 上游返回数值时优先采用上游数值；
- Kiro 没有缓存字段时，使用稳定的缓存模拟器计算 `cache_creation_input_tokens` 和 `cache_read_input_tokens`；
- 缓存模拟器按稳定前缀、会话、模型、时间窗和命中状态计算，不能每次随机生成；
- 缓存模拟同时服务于内部计费和公开 Anthropic 兼容 usage，保持客户端体验一致；
- 对外文档和内部诊断明确 cache 字段是 `emulated`，不是 Kiro 上游原生返回；
- 严格协议响应中删除 `_sub2api_kiro_credits` 等私有计费字段；
- 定义上游缺少 input/output usage 时的确定性估算策略，并保留估算来源；
- 使用测试夹具验证流式和非流式总量、缓存命中率和多轮增长曲线；
- 如果内部计费与公开 usage 有意不同，需要写明计算方式和差异原因。

缓存模拟的推荐模型：

1. 将 system prompt、工具定义、历史消息和媒体输入分别规范化，并计算稳定前缀指纹。
2. 以账号、模型和会话为维度维护短期缓存状态，避免不同用户之间错误共享。
3. 首次出现的稳定前缀计入 cache creation；有效时间窗内再次出现时计入 cache read。
4. 发生变化的后缀只计入新增输入，不把整个历史重复计费。
5. 流式和非流式路径使用同一个缓存计算器。
6. 对缓存失效、模型切换、system prompt 改变和工具集合改变定义明确的失效规则。
7. 诊断日志记录命中原因、前缀长度和来源，但不记录 API Key 或完整用户内容。

验收标准：

- 每个公开 Token 字段都能追溯数据来源；
- 多轮 input 增长可以由可见内容、明确记录的隐藏上下文和缓存模拟状态解释；
- 在选定的统计策略下，流式和非流式总量一致；
- 连续相同前缀的多轮请求能稳定产生 cache read，首次请求产生 cache creation；
- Token 审计失败时可以通过来源记录定位原因；
- 缓存模拟不会跨账号、跨模型或跨不兼容 system prompt 错误命中。

## 6. 推荐实施顺序

由于后续工作依赖前面的可观测性和统一表示，推荐按以下顺序实施：

1. 捕获原始基线并建立协议测试夹具。
2. 增加最小提示词模式。
3. 建立统一的内部 message/content block 模型。
4. 基于统一模型重建 SSE 和非流式序列化。
5. 完成结构化输出验证和转换。
6. 完善普通工具调用工作流。
7. 将 WebSearch 重构为模型参与的搜索循环。
8. 保留上游原生 reasoning，并在严格模式移除合成签名。
9. 完成缓存模拟器和 usage 来源分层，重新执行多轮 Token 审计。

在原始协议测试夹具通过前，不要直接以提高 CCTest 总分为目标。只有分数提升、实际能力测试却没有改善时，很可能只是针对检测器做了特殊行为，而不是真正提升兼容性。

## 7. 验证矩阵

### 7.1 协议测试夹具

每个功能都应保存经过脱敏的 golden fixture，包括：

- 客户端请求；
- 发给 Kiro 的上游请求；
- Kiro 原始响应帧；
- 统一内部消息模型；
- Anthropic 非流式响应；
- Anthropic SSE 事件序列。

Golden 测试应验证语义字段，不应依赖随机 ID、时间戳等易变化内容。

### 7.2 流式协议不变量

- 成功响应中恰好存在一个 `message_start`；
- 成功响应中恰好存在一个最终 `message_stop`；
- `message_start` 之前不允许出现内容事件；
- 每个 block 只能开始和结束一次；
- delta 必须引用相同 index 的已打开 block；
- thinking 或 tool block 中不能输出 text delta；
- 最终 stop reason 必须与已经输出的内容一致；
- 最终 usage 必须是累计值并且不为负数；
- 客户端取消请求后，应及时停止上游处理。

### 7.3 结构化输出测试用例

- 扁平对象；
- 嵌套对象；
- 必填和可选属性；
- enum；
- 数组；
- Unicode 和控制字符转义；
- 很长的流式 JSON；
- schema 验证失败；
- 工具输入被截断；
- 模型拒答；
- 客户端工具名与内部工具名冲突；
- JSON Schema 与 thinking 同时启用。

### 7.4 工具调用测试用例

- 自动选择工具；
- 强制指定工具；
- `any` 和 `none`；
- 并行工具调用；
- 空对象输入；
- 增量 JSON 输入；
- 非法 JSON 输入；
- 包含文本和结构化内容的 tool result；
- 孤立 tool result；
- thinking 后调用工具；
- 多轮工具循环。

### 7.5 搜索测试用例

- 一次搜索并回答；
- 多次搜索；
- 没有结果；
- 搜索提供商超时；
- 搜索提供商部分失败；
- 非 ASCII 搜索词；
- 限定域名；
- 后续问题使用之前的搜索结果；
- 引用映射；
- 搜索 usage 与计费次数一致。

### 7.6 Usage 审计测试用例

- 单轮普通文本；
- 11 轮不断增长的对话历史；
- 重复的稳定前缀，用于测试缓存；
- 历史消息中包含工具；
- 历史消息中包含 thinking；
- 图片和 PDF 输入；
- 隐藏 prompt 模式与最小 prompt 模式对比；
- 流式和非流式对比。

## 8. 成功标准

首先用真实能力衡量改进效果，然后再参考外部检测器分数。

真实能力成功标准：

- 隐藏指令更少，无法解释的 input Token 开销更低；
- 非编程任务中的指令遵循更加稳定；
- 能够按照 schema 返回合法结构化 JSON；
- 多轮工具工作流可靠；
- 搜索答案基于真实检索证据生成；
- 流式和非流式语义等价；
- 能准确区分上游 usage、本地估算 usage 和缓存模拟 usage；
- 缓存模拟在相同会话和稳定前缀下具有可重复的命中结果；
- 缓存模拟不会跨账号、跨模型或跨不兼容提示词错误命中；
- 不把本地生成的数据表示为 Anthropic 官方签名。

外部验证可以包括：

- 使用相同配置重新运行 CCTest；
- 开启 Token Usage Audit 后重新检测；
- 每种模型和账号类型至少运行三次；
- 分别比较 Kiro OAuth 和 Kiro API Key 账号链路；
- 记录 CCTest 分数变化，同时保存项目内部协议测试结果。

## 9. 非目标和能力边界

以下内容不应实施：

- 识别检测器提示词并返回特殊结果；
- 伪造 Anthropic thinking signature；
- 伪造 Vertex、Bedrock 或 Anthropic 渠道标记；
- 伪造 Anthropic encrypted web-search content；
- 为匹配检测基线而随意伪造 Token usage；
- 将缓存模拟冒充成 Kiro 上游原生缓存；
- 为隐藏真实上游模型而改写模型名；
- 仅为提高检测分数而吞掉真实错误。

如果 Kiro 没有提供某个上游原生能力，仅靠转换层可能无法实现完全等价的官方行为。这种情况下应提供明确说明的模拟能力，或者直接报告该能力不受支持。

## 10. 后续工作检查清单

- [ ] 为 CCTest 风格的 12 项检测保存脱敏基线夹具。
- [ ] 单独保存一套 11 轮 Token 审计基线。
- [ ] 定义 Kiro minimal、legacy、strict 三种兼容策略。
- [ ] 减少无条件 system/history 注入。
- [ ] 定义统一的内部 content block 模型。
- [ ] 统一流式和非流式序列化。
- [ ] 增加结构化输出 schema 验证。
- [ ] 将内部结构化输出工具调用转换为验证后的 JSON text。
- [ ] 完成强制、并行和多轮客户端工具行为。
- [ ] 将 WebSearch 改造成模型参与的搜索循环。
- [ ] 保留上游原生 reasoning 和 redacted reasoning 字段。
- [ ] 严格模式中移除合成 signature。
- [ ] 分离上游、估算和缓存模拟 usage，并记录来源。
- [ ] 保留缓存模拟，加入稳定前缀、命中窗口和失效规则。
- [ ] 严格 Anthropic 响应中删除私有 usage 字段。
- [ ] 执行单元测试、集成测试、fixture 测试和外部黑盒验证。
- [ ] 实施完成后记录仍然存在的上游能力限制。

## 11. 参考资料

- Anthropic 流式消息：`https://platform.claude.com/docs/en/build-with-claude/streaming`
- Anthropic Extended Thinking：`https://platform.claude.com/docs/en/build-with-claude/extended-thinking`
- Anthropic 结构化输出：`https://platform.claude.com/docs/en/build-with-claude/structured-outputs`
- Anthropic WebSearch 工具：`https://platform.claude.com/docs/en/agents-and-tools/tool-use/web-search-tool`
- 本文使用的 CCTest 基线报告：`https://cctest.ai/zh/result/fd514e3e-3611-43bd-95b3-d9d75597601b`

这些参考资料描述的是可观察协议和预期 API 行为，不代表 Kiro 一定暴露了所有对应的原生能力。

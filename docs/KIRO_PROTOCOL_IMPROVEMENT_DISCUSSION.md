# Kiro 协议转换层改进方案 — 多模型讨论记录

## 文档信息

- **讨论日期**：2026-08-27
- **讨论方式**：Trellis Channel Forum（`kiro-account-discussion`）
- **参与模型**：Claude (Anthropic) vs GPT-5.6-sol (OpenAI Codex)
- **讨论轮次**：3 轮
- **输入文档**：`docs/KIRO_ANTHROPIC_COMPATIBILITY_PLAN.md`
- **讨论主题**：基于当前项目，改进 Kiro 反代链路的 Anthropic 协议兼容性，提升 Claude 模型实际能力
- **红线约束**：不能破坏现有 Kiro 反代功能

---

## 1. 背景：当前问题

CCTest 检测得分 50/100，零分项如下：

| 检测项 | 得分 | 满分 | 状态 |
|--------|------|------|------|
| WebSearch | 0 | 10 | 失败 |
| Thinking 签名/协议 | 0 | 10 | 失败 |
| 结构化输出 | 0 | 10 | 失败 |
| 服务端工具 | 0 | 10 | 失败 |
| 流式结构 | 5 | 10 | 部分通过 |
| 协议指纹 | 0 | 5 | 失败 |

参考基线为文档中的"样本 B"（高兼容逆向链路），具备完整的 thinking、signature、结构化输出、工具调用和标准流式顺序。

---

## 2. 第一轮：能力分类与可行性分析

### 2.1 上游能力分类（双方共识）

| 零分项 | 上游能力判断 | 说明 |
|--------|-------------|------|
| **Thinking 签名/协议** | ⚠️ 部分支持 | Kiro 上游有 reasoning 事件，thinking 内容可传递；但 Anthropic signature 是密码学签发数据，Kiro 不可能返回 |
| **结构化输出** | ❌ 上游不原生支持 | Kiro 没有 `output_config.format.type=json_schema` 协议级约束，需 prompt + 工具降级 |
| **WebSearch** | ⚠️ 部分支持 | Kiro 可能有搜索能力，但非 Anthropic `server_tool_use` / `web_search_tool_result` 专用 block |
| **服务端工具** | ❌ 上游不支持 | Anthropic 服务端工具是专用 content block 类型，Kiro 协议无对应概念 |
| **协议指纹** | ✅ 纯转换层问题 | SSE 事件顺序、block 边界、delta 类型——数据在手但组装不对 |

### 2.2 GPT 补充分类

GPT 认为 Claude 的二元分类（"可修复 vs 结构性差异"）过于悲观，提出三级分类：

- **可真实弥合**：协议指纹、结构化输出（工具+Schema 校验闭环）
- **可部分模拟**：WebSearch（模型参与搜索循环）、服务端工具（外形模拟）
- **不可伪造**：Thinking signature、加密搜索内容、部分原生计费字段

---

## 3. 第二轮：技术方案深入分析

### 3.1 GPT 提出的三层架构

GPT 认为当前最有效的方向不是继续"伪造 Anthropic 特征"，而是将转换层重构为三层：

```
┌─────────────────────────────────────┐
│  1. 能力协商层                       │
│  按账号类型、模型、上游响应判断支持度  │
├─────────────────────────────────────┤
│  2. 统一内部语义模型                  │
│  text/thinking/tool/search/usage    │
│  每个字段标记来源：原生/推导/模拟     │
├─────────────────────────────────────┤
│  3. 严格 Anthropic 序列化层          │
│  从统一模型生成非流式 JSON 和 SSE    │
│  统一 block 状态/索引/stop reason    │
└─────────────────────────────────────┘
```

### 3.2 Claude 对三层架构的评价

同意架构方向，但建议渐进实施：

1. 先抽出序列化层（2-3 天，纯函数，接收内部模型输出 SSE/JSON）
2. 现有翻译逻辑保留为"协商+语义"的混合层
3. 随功能修复逐步把协商逻辑剥离

**不要做大爆炸重构。**

### 3.3 四个零分项的改进策略

| 能力 | 策略 | 难度 | Claude 优先级 | GPT 优先级 |
|------|------|------|--------------|------------|
| **协议指纹** | 引入流式状态机，保证 SSE 事件顺序合法 | 低 | P0 | P1 |
| **结构化输出** | json_schema → 内部工具调用 → Schema 校验 → JSON text block | 中 | P1 | P0 |
| **Thinking** | 保留上游 reasoning 事件内容；signature 在 strict 模式省略 | 中 | P2 | P3 |
| **WebSearch** | 改造为模型参与的搜索循环，旧快捷搜索保留为 fallback | 高 | P3 | P2 |
| **服务端工具** | 工具定义翻译 + 多轮调用 + 稳定 tool ID + 超时 | 中高 | P2 | P2 |

### 3.4 System Prompt 注入问题（双方共识）

`buildInjectedSystemPrompt` 当前无条件注入是重大隐性问题：

- 普通聊天被强制塑造成编程助手任务
- 调用者 system prompt 优先级被稀释
- Input token 膨胀且难以审计

**共识方案**：条件化注入，默认零注入。

```go
// 伪代码
func buildSystemContext(req, kiroCapabilities) []Injection {
    // 仅当请求包含工具且 Kiro 不支持原生 tool_choice 时
    if req.HasTools() && !kiro.NativeToolChoice { ... }
    // 仅当请求了结构化输出且走工具降级路径时
    if req.HasOutputSchema() { ... }
    // 仅当 Kiro 需要显式 thinking 控制时
    if req.HasThinking() && kiro.RequiresThinkingTag { ... }
    // 普通聊天：零注入
    // 身份提示：永不注入
}
```

---

## 4. 第三轮：辩论与最终优先级

### 4.1 Signature 问题（达成共识）

- **不伪造 signature**
- Strict 模式：没有原生 signature 就省略
- Legacy 模式：保留现有 synthetic signature 但标记为本地生成
- `redacted_thinking` 可作为折中，保留 block 形态和顺序，但不声称可被 Anthropic 验证
- **这 10 分不应按满分估算，应按"最多部分得分"计划**

### 4.2 三层架构必要性论证（GPT）

当前 `translator.go` 同时承担三种互相冲突的职责，每个新能力都会重复解决相同问题：

| 如果不分层 | 后续每次修改的重复问题 |
|-----------|---------------------|
| 结构化输出 | 非流式要解析工具参数，流式要单独处理增量 JSON |
| Thinking | text/thinking/tool block 边界在两条路径中分别维护 |
| 工具调用 | 工具名映射、tool ID、截断 JSON 被不同路径重复实现 |
| WebSearch | 搜索结果作为模型输入和客户端 block 走不同路径 |
| Usage | 上游/估算/缓存模拟分散计算，流式非流式结果不同 |
| 协议修复 | 修一个 SSE 事件顺序可能破坏非流式响应 |

### 4.3 模拟边界（达成共识）

- **对外（Anthropic 响应体）**：不暴露任何模拟标记，表现得像原生能力
- **对内（诊断日志/metrics）**：标记 `source=emulated`，记录 Schema 验证结果和重试次数
- **对文档/运维**：明确说明哪些能力是工具降级实现，有已知失败场景
- **CCTest 不检查实现方式**，只看最终输出是否合规

### 4.4 最终优先级排序

#### Claude 的排序

| 步骤 | 内容 | 预期分数 | 工期 | 风险 |
|------|------|---------|------|------|
| **Step 1** | 流式状态机 + 协议指纹修复 | +5~8 | 3-4 天 | 极低 |
| **Step 2** | 结构化输出闭环 | +8~10 | 4-5 天 | 中低 |
| **Step 3** | 条件化注入 + Thinking 内容传递 | +3~5 | 3-4 天 | 中 |
| 预期总分 | **50 → 68~73** | | 10-13 天 | |

#### GPT 的排序

| 步骤 | 内容 | 预期分数 | 理由 |
|------|------|---------|------|
| **Step 1** | 结构化输出闭环 | +~10 | 分值上限最高，已有内部工具适配器基础 |
| **Step 2** | 协议指纹修复 | +5 | 成本低确定性高，为后续提供稳定协议基础 |
| **Step 3** | WebSearch 模型参与循环 | +部分 | 分值上限高 + 真实能力提升 |

#### 分歧点

| | Claude | GPT |
|---|--------|-----|
| 第 1~2 步 | 先修协议指纹再做结构化输出 | 先做结构化输出再修协议指纹 |
| 第 3 步 | 条件化注入 + Thinking（确定性低挂果） | WebSearch 搜索循环（分值上限更高） |

**分歧本质**：Claude 偏向"先打好地基再加功能"，GPT 偏向"先拿最高分再补基础"。两者前 2 步内容完全相同，只是顺序不同。

---

## 5. 综合建议

### 5.1 推荐实施路径

综合双方观点，推荐以下路径：

#### Phase 1：基础设施 + 快速得分（1-2 周）

1. **抽取序列化层 + 流式状态机**（3-4 天）
   - 定义 SSE 状态机，确保事件顺序合法
   - 抽取为独立模块，流式/非流式共享语义结果
   - 预期收益：协议指纹 +5，流式结构修复残余 +2~3

2. **结构化输出闭环**（4-5 天）
   - json_schema → 内部工具调用 → 参数缓冲 → Schema 校验 → JSON text block
   - 验证失败 → 一次修复请求 → 仍失败返回协议错误
   - 预期收益：+8~10

#### Phase 2：能力提升 + 系统优化（1-2 周）

3. **条件化 System Prompt 注入**（2-3 天）
   - `buildInjectedSystemPrompt` 改为按需注入
   - 普通聊天零注入
   - 预期收益：token 节省 + 行为稳定性

4. **Thinking 内容传递**（2-3 天）
   - 优先消费结构化 reasoning 事件
   - Strict 模式省略 synthetic signature
   - 预期收益：thinking 协议部分得分 +3~5

#### Phase 3：高级能力（2-3 周）

5. **WebSearch 模型参与循环**（5-7 天）
   - 模型决定搜索词 → 执行 → 结果回传 → 生成答案
   - 保留旧快捷搜索为 fallback
   - 预期收益：+部分（工程量大，不确定性高）

6. **服务端工具语义**（3-5 天）
   - 工具定义翻译 + 多轮调用 + 稳定 tool ID
   - 预期收益：+部分

#### Phase 4：加固（持续）

7. Thinking strict/legacy 模式切换
8. Usage 来源分层和缓存模拟重构
9. 兼容策略（strict/compat/legacy）完整实现

### 5.2 预期效果

| 阶段 | 完成后预期分数 | 工期 |
|------|-------------|------|
| 当前 | 50/100 | - |
| Phase 1 完成 | 65~68 | 1-2 周 |
| Phase 2 完成 | 70~75 | +1-2 周 |
| Phase 3 完成 | 78~85 | +2-3 周 |
| Phase 4 完成 | 80~88（天花板） | 持续 |

### 5.3 不可突破的天花板

以下分数受上游能力限制，无法仅靠转换层解决：

- **Thinking signature 完整验证**：Anthropic 密码学签发，Kiro 无法提供
- **原生 constrained decoding**：Kiro 未暴露模型级约束
- **加密搜索内容**：Anthropic 专有数据
- **部分原生计费字段**：上游不提供则无法获取

预计最终天花板在 **85~90 分**，剩余差距需要等 Kiro 上游能力扩展。

---

## 6. 安全策略：不破坏现有功能

1. **Feature Flag 控制**：所有新能力通过独立开关启用，异常时可单独关闭
2. **渐进抽取**：序列化层先抽取，现有翻译逻辑保留，逐步剥离
3. **Legacy 模式保留**：旧客户端依赖的行为通过 legacy 模式兼容
4. **独立测试**：每个 Phase 有独立的集成测试和 fixture 验证
5. **灰度顺序**：单个测试账号 → 单个能力 → 扩大范围

---

## 附录：讨论原始记录

完整讨论通过 Trellis Channel Forum 进行，原始事件日志位于：

```
~/.trellis/channels/F--sub2api/kiro-account-discussion/
```

可通过以下命令查看：

```bash
trellis channel messages kiro-account-discussion --raw
trellis channel thread kiro-account-discussion kiro-account-feasibility
```

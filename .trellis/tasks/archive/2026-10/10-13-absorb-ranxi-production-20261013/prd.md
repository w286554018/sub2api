# 吸收 ranxi production 功能

## 授权与目标

用户已明确“确认，并且批准实行”，批准完整吸收方案及仅本次覆盖缺失 grill_ready 检查点；沿 Trellis 实施，不修改全局规则。将来源 production 锁定提交 1d2ed630e292eaaac95454c1786bf3b7e5ed0828 的功能增量与当前基线 1f08460552fbac66106baa97d0e3841f93057d58 集成，验证后更新用户仓库开发分支与 main。

## 功能要求

- DeepSeek/Responses 转换、压缩、工具和流桥接增量。
- API Key 独立并发/等待队列、优先调度及组/用户模型策略。
- Mihomo 订阅、节点、出口管理与静态代理绑定。
- Codex ticket/Harvest、并行采集、节点学习、流水、Astra 复用与调度。
- Excel/Basispoints(BPS)、图片/工具/结构化输出、凭证恢复及运行时。
- 账号质量/运维、自动配置、阈值告警、通知。
- 监控 V3/V2 candy、缓存测试工作台、Pelican、受控实验。
- 网站工单、观察员、请求采集、Serverless 多 Pod、new-api 站点授权。

## 必须保留

当前 Kiro/Adobe、配额与计费，Anthropic→OpenAI→Gemini 优先级，账号探测与智能测试、Codex 指纹和票长、弹窗焦点/栈，已发布 SQL 与 checksum 兼容表，以及原有用户文件和独有回归测试。来源专有品牌/镜像/运行配置保持为用户仓库自己的配置；新外部服务可配置。实验性 Beta 默认关闭。

## 验收

完整功能对照有 API、页面、开关、迁移、测试；新旧行为均通过相关回归。完整 Go、前端 lint/typecheck/Vitest/build、嵌入和 Linux 构建通过；隔离数据库迁移和并发/取消验证通过，Mihomo Linux 进程实测。记录命令、输入树、输出、退出码。独立副本验证回退，四项产物重新打开并校验。最终用户仓库开发分支、main、默认 HEAD 指向验证后的集成提交。外部模型/登录的在线效果与本地验证分开记录。

## 已完成验收记录

已完成真实双亲集成、F盘完整Go和Windows/Linux构建、前端全量验证、最终二进制Linux实测、隔离数据库连续升级、独立源码回退与回退后100个顶层行为测试。310条旧SQL、旧schema字段与原302个用户文件已在正式快进前后核验保留；两个动态状态文件的更新未覆盖。

2026-10-13源码提交`0e98a5faf664f21302ab069f3afc90f3dc4c4447`已普通原子推送开发分支和main，远端默认HEAD亦已核验。四项材料及命令/退出码见`reports/ranxi-integration-20261013/VERIFICATION.txt`。Mihomo默认跳过5个联网opt-in及1个辅助入口，仓储有1个skip，前端有9个原有skip，均未当成通过。用户要求的F盘编译已持续落实。

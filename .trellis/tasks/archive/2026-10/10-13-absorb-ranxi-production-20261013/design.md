# 集成设计

## 锁定对象

基线 1f08460552fbac66106baa97d0e3841f93057d58，树 ddd6dbeacfb34579b51c7162256ef0107de367bd；来源 production 1d2ed630e292eaaac95454c1786bf3b7e5ed0828；共同祖先 efe9aab1e4ec89a42ba45e8dac20e882c5409a6a。来源 main 相对祖先仅 VERSION 改动，功能从 production 吸收。

## 集成方式

在隔离 detached worktree 形成真实双亲兼容合并，保留双方历史以支持持续同步。Git 已能自动合入大量独有模块；265 个冲突路径按各层契约处理。生成 Ent 代码以合并后的 schema 为真源，保留现有字段并新增来源字段。只更新所需依赖，不丢弃指纹传输和 tokenizer 依赖。

## 领域与接口

- 账号健康探测/智能测试与来源质量运维独立保留，不互相替换。
- 用户/账号并发限制与 API Key 队列是不同层次，共同准入；取消、超时和权限变化需释放对应槽位。
- Codex 票据兼容现有计划票长设置、fail-closed、指纹，并增加采集、Astra、Mihomo 出口功能。
- 监控模式保留 V1/V2 并新增 V3；注册设置、PublicSettings、DTO、前端类型、导航守卫和双语字段。
- 普通 Responses 与 Excel/BPS 路径按账号配置选路，不改变未启用账号的行为。

## 迁移

迁移器按完整文件名和 SHA256 识别，不按数字编号独立识别。同号不同名不天然冲突。保留基线所有已发布 SQL 原文及 checksum 兼容规则；新增来源表/字段使用追加迁移，逐项核对相同路径和执行顺序。不得覆盖来源较窄的旧平台约束而丢失 Kiro/Adobe。核对 Ent 与生产 SQL 字段一致。

## 前端和部署

保留当前账号、弹窗与平台目录契约，合入来源全部新增页面/API；导航入口和功能开关配套落地。保留用户已有品牌、镜像、发布和部署件；来源新增 Linux 工具与 Mihomo 组件可配置，来源默认第三方服务连接改为显式配置。

## 验证与回退

原工作区保持基线至隔离验证完成；保存原未跟踪文件元数据和 SHA256。新备份引用基于 1f084605，而不是上一轮旧基线。回退脚本限定预期 HEAD、分支、已跟踪干净状态与 Git 操作状态，使用 reset --keep；在另一个副本测试 HEAD、树和关键行为恢复。数据库验收只使用隔离实例，源码回退不宣称回滚已部署数据库。四项持久产物放 reports/ranxi-integration-20261013。

## 环境

Windows Go/pnpm；WSL Ubuntu-20.04 uname 为 Linux x86_64，WSL 内有 docker，但服务状态需先解决查询超时。优先准备隔离 PostgreSQL/Redis，再执行集成和 Mihomo 模拟运行。外部模型与真实登录不因离线 mock 成功而宣称在线成功。

用户纠正后，全部后续Go编译切换到`F:\sub2api\.task-cache\ranxi-20261013`：源码验证副本在source，GOCACHE在go-cache，GOTMPDIR/TEMP/TMP在go-tmp，产物在bin；GOMAXPROCS=2、GOMEMLIMIT=2GiB、GOGC=50、GOFLAGS=-p=1。最终Windows/Linux构建与独立回退后的编译均已按此执行。WSL只执行F盘最终二进制，Windows Git负责核对worktree身份。

最终真实双亲集成提交为`0e98a5faf664f21302ab069f3afc90f3dc4c4447`，源码树`20cb925d7fcce7bd172649eb1228c1ecf1991d31`。源码推送与文档封存分两次普通原子推送进行，文档后继不得改变已验证业务源码。

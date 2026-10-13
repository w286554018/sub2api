# 实施与验证

## 检查清单

- [x] 完成双分支历史、后端/页面审计并取得本轮明确实施授权。
- [x] 保存新基线、原未跟踪文件清单，建立备份与隔离 worktree。
- [x] 执行真实合并，265个初始冲突全部解决；最终双亲与输入树已核验。
- [x] 保留 fork 平台、迁移、票长/指纹、账号探测与弹窗契约，生成 Ent/Wire 必要产物。
- [x] 基线完整 Go 62包、前端397文件/3096用例通过；集成版完整 Go 72个有测试包、前端504文件/4288用例通过。
- [x] 前端lint/typecheck/build、Windows/Linux embed构建和嵌入资源测试通过。
- [x] 最终F盘二进制在隔离PostgreSQL/Redis实测；四项升级、并发、Harvest、实验回归全部实际PASS。
- [x] Mihomo最终二进制在Linux整包实测，163条含子项PASS、0FAIL；5个联网opt-in和1个辅助进程入口skip如实记录。
- [x] 父代理限定复核修正K3s默认值和预发布/通知兼容；18个部署与12个发布矩阵回归通过。外部审查中断，不计为审查通过。
- [x] 创建真实双亲合并0e98a5faf664f21302ab069f3afc90f3dc4c4447，独立F盘副本回退和行为实测通过。
- [x] 生成并重新打开四项持久材料，bundle校验、patch字节比对、回退脚本执行和哈希通过。
- [x] 快进正式分支，前后保留核验通过；普通原子推送mine开发分支和main，核对默认HEAD。
- [x] 完成中文记录、任务归档与经验记忆；封存文档作为业务源码不变的后继提交同步两个分支。

## 已确认输入与提交

基线：`1f08460552fbac66106baa97d0e3841f93057d58`，树`ddd6dbeacfb34579b51c7162256ef0107de367bd`。
来源production：`1d2ed630e292eaaac95454c1786bf3b7e5ed0828`。
真实双亲集成：`0e98a5faf664f21302ab069f3afc90f3dc4c4447`，父提交依次为基线和来源。
已验证源码树：`20cb925d7fcce7bd172649eb1228c1ecf1991d31`。
持久引用：`integration/ranxi-20261013`。
本地旧main备份：`backup/pre-ranxi-local-main-20261013-b9faa9ee5`。

## F盘编译命令

用户要求不在C盘编译，后续Go验证副本、缓存、临时目录和输出全部在F盘；旧C盘依赖缓存只读复用，原始早期验收记录按实际环境保留。

```powershell
$env:GOCACHE='F:\sub2api\.task-cache\ranxi-20261013\go-cache'
$env:GOTMPDIR='F:\sub2api\.task-cache\ranxi-20261013\go-tmp'
$env:TEMP=$env:GOTMPDIR
$env:TMP=$env:GOTMPDIR
$env:GOMAXPROCS='2'
$env:GOMEMLIMIT='2GiB'
$env:GOGC='50'
$env:GOFLAGS='-p=1'
Set-Location 'F:\sub2api\.task-cache\ranxi-20261013\source\backend'
go test -tags=unit -p 1 -timeout=20m ./... -count=1
go build -tags=embed -p 1 '-ldflags=-s -w' -o F:\sub2api\.task-cache\ranxi-20261013\bin\sub2api-windows-amd64.exe ./cmd/server
go test '-tags=unit,embed' -p 1 -timeout=5m ./internal/web -count=1
```

Linux构建额外设置`GOOS=linux GOARCH=amd64 CGO_ENABLED=0`。最终仓储和Mihomo测试二进制在F盘交叉编译后，显式按绝对路径与SHA256传入Linux进行实际执行。POSIX临时文件在任务自己的ext4目录；隔离Docker使用任务专用socket/data-root/bridge，不使用生产数据库。

## 验收与回退结果

所有成功结论均来自runner JSON的`completed / exit_code=0`和对应日志哈希；running、中断或仅编译不计通过。前端完整用例的旧index记录与实际工作文件的差异已解释，最后两个测试文件补跑39个用例通过。

独立F盘回退副本恢复基线HEAD/tree，tracked clean；错误预期HEAD/分支各退出3并保持集成HEAD，未跟踪哨兵SHA256前后一致。回退后平台/组合/Codex票长与指纹/WS入口100个顶层测试、813条含子项PASS，0FAIL/0SKIP。源码回退不等同已部署数据库恢复。

310条已发布SQL原文未变，基线schema字段缺失0，新路径碰撞0。原302个未跟踪文件全部存在，其中300个内容/mtime/SHA一致，两项`.omx/state`动态更新原样保留。

正式源码已普通原子推送开发分支和main，远端默认HEAD为main，三项均指向集成提交。文档封存后再次核验远端结果见本地`reports/ranxi-integration-20261013/evidence/remote-final.json`。

## 持久材料

`reports/ranxi-integration-20261013/MODIFIED_FILE.bundle`、`DIFF_FILE.patch`、`VERIFICATION.txt`、`ROLLBACK.sh`；全部实际命令、输入、原始输出和退出码保存在`evidence/`。生产部署、真实账号上游效果和官方Mihomo联网测试尚未执行。

## 经验

- 只迁移cwd不足以防止C盘编译，必须同时固定GOCACHE/GOTMPDIR/TEMP/TMP和产物路径。
- Windows同一时钟刻的两次time.Now()可以完全相同；测试涉及先后顺序时明确设置fixture，不改变生产边界规则迁就测试。
- 快照缓存读取后只能CAS写回，避免旧读取在失效后重填；可选Wire依赖须保留nil兼容。
- WSL Git不解析Windows worktree中的F:/指针；身份与树使用Windows Git核验，Linux仅运行实际文件。

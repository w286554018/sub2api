# ranxi 功能集成会话记录

日期：2026-10-13。项目：`F:\sub2api`。本记录单独新建，旧会话日志和原用户文件继续保留。

## 目标与执行依据

按用户已批准的完整方案吸收锁定的ranxi production功能，保留当前fork平台、计费、Codex指纹/票长和弹窗等契约。延续Trellis授权，不重开审批，不修改全局规则。用户明确要求后续编译全部放F盘。

## 结果

真实双亲合并`0e98a5faf664f21302ab069f3afc90f3dc4c4447`，第一父为基线`1f08460552fbac66106baa97d0e3841f93057d58`，第二父为来源`1d2ed630e292eaaac95454c1786bf3b7e5ed0828`；已验证源码树`20cb925d7fcce7bd172649eb1228c1ecf1991d31`，持久引用`integration/ranxi-20261013`。

完整Go最终72个有测试包通过，前端504文件/4288用例通过，9个skip；最后两测试文件补跑39用例。lint/typecheck/build和Windows/Linux embed构建通过。最终F盘二进制在Linux执行仓储与Mihomo：仓储2089条含子项PASS、四项新增回归实际PASS、1个skip；Mihomo163条含子项PASS、5个联网opt-in及1个辅助进程入口skip，0FAIL。

独立F盘副本真实运行ROLLBACK.sh恢复基线HEAD/tree，哨兵SHA256前后一致、tracked clean。回退后100个顶层测试/813条含子项PASS，0FAIL/0SKIP，domain/service均实际执行。

310条已发布SQL未改变，旧schema字段缺失0，新路径碰撞0。原302文件全部在；300个字节、mtime与哈希完全一致，两个.omx动态更新保持当前值。正式快进前后核验均通过。

源码已普通原子推送用户开发分支与main，ls-remote --symref确认远端两分支和HEAD均为上述集成提交，默认refs/heads/main。原本地main的4个旧集成提交保留在`backup/pre-ranxi-local-main-20261013-b9faa9ee5`。

## 编译位置和经验

编译副本、GOCACHE、GOTMPDIR、TEMP、TMP和所有输出在`F:\sub2api\.task-cache\ranxi-20261013`。早期C盘构建曾因空间不足失败，未计为通过，后来使用F盘成功记录替代。没有终止无关应用或清理用户依赖。

普通WS请求逐帧恢复指纹暂存，明确harvest pin才使用采票身份；fill-only元数据契约保留，预编码JSON只发送一次。fail_closed缓存CAS阻止旧读取回填。Wire兼容nil可选依赖，Kiro保持专属billing快照，刷新拒绝迟到成功。Windows质量事件测试以确定先后顺序的fixture消除delta=0s边界，生产规则保留。

限定复核修正部署仓库/镜像默认值与rc稳定标签、VERSION、通知opt-in。18项K3s与12项发布矩阵回归通过。外部审查中断没有通过报告；一次委派503没有执行测试，续接已有会话后实际完成Linux验收。

## 持久交付与边界

材料位于`reports/ranxi-integration-20261013`，包括MODIFIED_FILE.bundle、DIFF_FILE.patch、VERIFICATION.txt、可执行ROLLBACK.sh与原始证据。bundle verify通过，patch与Git实际diff字节一致，四项材料已重新打开核验。

仅在隔离数据库验证升级，没有部署生产或回滚已部署数据库。真实模型/登录上游及官方Mihomo联网内核效果尚未验证。文档封存后继只增加报告与Trellis文档，业务源码不得改变；最终两个远端分支与HEAD另记录在本地`evidence/remote-final.json`。

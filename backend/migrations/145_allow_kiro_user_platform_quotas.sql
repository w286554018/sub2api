-- 允许用户平台维度配额记录 Kiro。
--
-- 142_user_platform_quotas 建表时的平台 check 只包含四个平台；
-- 代码层和设置层已经支持 kiro，运行时会在记账时插入 platform='kiro'。
-- 此迁移可能被补入已经执行过 157_user_platform_quotas_add_grok 的数据库，
-- 因此新约束必须保留 grok，不能在 157 重放前暂时缩窄允许集合。

ALTER TABLE user_platform_quotas
  DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;

ALTER TABLE user_platform_quotas
  ADD CONSTRAINT user_platform_quotas_platform_check
  CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'kiro'));

# 额度与速率限制链

金额在组织 → 团队 → 用户之间分配，密钥金额仅为独立上限；RPM、TPM 继续沿完整层级分配。项目增加并行上限，不形成另一份分配池。

## 值的含义

~~~text
numeric limit -> fixed allocation
null          -> share remaining parent capacity
0             -> disabled
~~~

增加额度先父级后子级，减少先子级后父级。写入在事务中校验，失败不部分改角色或金额。重置密钥支出不预留金额，也不删除用户、团队、组织历史累计费用。

路由继承与额度继承独立。当前没有请求预算预占，并发请求和长任务可能超预算。

## 源码与验证

[IAM](../../../internal/iam/readme_cn.md) · [权限](permissions.md)

~~~bash
bash scripts/regression.sh --verbose "TestRate|TestQuotaHierarchy|TestBudget|TestTeamless|TestPersonalKey"
~~~


个人 API Key 金额上限不预占资金；空预算 Key 和控制台共用个人实际剩余金额。即使金额耗尽也可创建 Key，但调用仍被拒绝。RPM/TPM 以及组织、团队、个人间的分配保留原规则。

# 个人预算按月刷新

管理员可以为个人用户选择“不自动刷新”，或每月 1～31 日刷新。刷新在北京时间（UTC+8）00:00 生效；该月没有所选日期时，在当月最后一天执行。比如选 31 日，二月在 28 日（闰年 29 日）刷新，三月仍在 31 日刷新。

刷新将个人本周期已消费金额归零，保留历史用量和账单，不累积未用余额。组织、团队和 Key 自身的额度独立计算；个人刷新不解除其他层级的限制。软预算规则不变：余额为正时允许请求，按实际用量结算，耗尽后拒绝下一请求。

## 设置入口

- 用户编辑：设置“预算刷新日期”，详情展示下一次刷新时间。
- CSV 批量创建：增加可选列 `budget_reset_day`。留空或 `0` 不刷新，`1`～`31` 指定日期。旧模板仍兼容。
- 批量编辑：预算上限和刷新日期分别设置；默认“保持原值”。可以只改日期，也可以只改额度，整批原子保存。

```csv
user_email,user_role,password,user_alias,max_budget,budget_reset_day
alice@example.com,user,Replace-before-import-123!,Alice,20,1
bob@example.com,user,Replace-before-import-456!,Bob,20,15
carol@example.com,user,Replace-before-import-789!,Carol,20,0
```

示例账户和初始密码必须在导入前替换。导入仍按行返回成功或失败，批量编辑则全成功或全回滚。

## API 约定

`POST /user/new` 和 `POST /user/update` 接受 `budget_reset_day` 整数，范围 0～31。创建时缺省为 0，更新时缺省保持。`null`、字符串、小数和范围外数字返回 400。读取返回 `budget_reset_day` 和 `budget_reset_at`（ISO 时间或 null），原 `budget_duration` 兼容字段不用于个人自然月设置。

`POST /user/bulk_update` 可独立提交两个字段中的任意一个，至少提供一项：

```json
{"users":[{"user_id":"example-user-id","budget_reset_day":15}]}
```

```json
{"all_users":true,"user_updates":{"max_budget":20,"budget_reset_day":1}}
```

`max_budget: null` 是取消个人固定上限，`budget_reset_day: 0` 是取消周期，二者不同。批量入口仍仅允许平台管理员，最多 1000 个用户。单用户日期更新沿用用户预算管理权限。

## 数据与运行规则

修改周期只安排下一次刷新，不立即清零消费；重复保存相同日期不推迟下一次刷新。取消周期保留已发生的周期起点，防止迟到账单重新占用当前个人预算。

后台维护、账户读取和请求准入会补执行到期刷新。停机多月直接推进到当前周期，不逐月累加额度。刷新、审计、配置和消费结算使用同一数据库写事务锁；并发重试仅记录一次 `user.budget_reset` 审计。上一周期开始、下一周期才结束的请求仍保留历史账单，但不占下一周期个人消费；其他预算层级仍各自计费。

升级时自动补列，旧用户默认不刷新且已有消费保留。Redis 待落库费用按个人周期过滤，数据损坏时不会凭空放宽额度。日期字段不接受客户端直接设置消费或下一次刷新时间。

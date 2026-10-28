# HTTP 处理器源码索引

[功能实现](implementation.md) · [权限](permissions.md) · [协议与运行边界](runtime.md)

## 认证、授权与错误约定

管理操作通常要求当前用户会话和对应动作权限；推理可以用当前会话或有效虚拟密钥。主密钥仅接受明确 bootstrap/应急动作。公开接口不等于公开修改权限。处理器是最终授权来源，表格列出实际入口，不把相同前缀下的全部接口推断为同一权限。

JSON 请求字段由处理器解析；模型定义使用 model_name/litellm_params/model_info，模板创建使用 name/body，模板绑定使用 scope/scope_id/route_template_id。非法 JSON 或字段返回请求错误，失效凭据返回认证错误，越权返回 403 或隐藏存在性的 404。依赖故障保留 5xx。429 使用 Retry-After；入口错误体保持对应协议。

x-litellm-call-id 关联调用与日志，x-litellm-response-cost 表示这次本地计算的美元费用，不是数据库 commit 回执。SSE 发出后无法改变 HTTP 状态，需查看日志失败证据。

## internal/gateway

[目录契约](../../../internal/gateway/readme_cn.md)

| 方法与路径 | 处理器 | 注册源码 |
| --- | --- | --- |
| `GET /admin/archive/kodo` | `archiveAdmin` | [directory.go](../../../internal/gateway/directory.go) |
| `PUT /admin/archive/kodo` | `archiveAdmin` | [directory.go](../../../internal/gateway/directory.go) |
| `POST /admin/archive/kodo/test` | `archiveAdmin` | [directory.go](../../../internal/gateway/directory.go) |
| `POST /admin/archive/kodo/retry` | `archiveAdmin` | [directory.go](../../../internal/gateway/directory.go) |
| `GET /admin/enterprise/sso` | `enterpriseOIDCAdmin` | [directory.go](../../../internal/gateway/directory.go) |
| `POST /admin/enterprise/sso` | `enterpriseOIDCAdmin` | [directory.go](../../../internal/gateway/directory.go) |
| `GET /admin/enterprise/sso/{connection_id}` | `enterpriseOIDCAdmin` | [directory.go](../../../internal/gateway/directory.go) |
| `PUT /admin/enterprise/sso/{connection_id}` | `enterpriseOIDCAdmin` | [directory.go](../../../internal/gateway/directory.go) |
| `DELETE /admin/enterprise/sso/{connection_id}` | `enterpriseOIDCAdmin` | [directory.go](../../../internal/gateway/directory.go) |
| `GET /auth/enterprise/options` | `enterpriseOIDCOptions` | [directory.go](../../../internal/gateway/directory.go) |
| `GET /auth/enterprise/{connection_id}/start` | `enterpriseOIDCStart` | [directory.go](../../../internal/gateway/directory.go) |
| `GET /auth/enterprise/callback` | `enterpriseOIDCCallback` | [directory.go](../../../internal/gateway/directory.go) |
| `GET /admin/enterprise/sso/{connection_id}/metadata` | `enterpriseSAMLMetadata` | [directory.go](../../../internal/gateway/directory.go) |
| `GET /auth/enterprise/{connection_id}/saml/metadata` | `enterpriseSAMLMetadata` | [directory.go](../../../internal/gateway/directory.go) |
| `POST /auth/enterprise/saml/acs` | `enterpriseSAMLACS` | [directory.go](../../../internal/gateway/directory.go) |
| `GET /auth/enterprise/saml/finish` | `enterpriseSAMLFinish` | [directory.go](../../../internal/gateway/directory.go) |
| `GET /admin/directory/connections` | `directoryAdmin` | [directory.go](../../../internal/gateway/directory.go) |
| `POST /admin/directory/connections` | `directoryAdmin` | [directory.go](../../../internal/gateway/directory.go) |
| `PUT /admin/directory/connections/{connection_id}` | `directoryAdmin` | [directory.go](../../../internal/gateway/directory.go) |
| `DELETE /admin/directory/connections/{connection_id}` | `directoryAdmin` | [directory.go](../../../internal/gateway/directory.go) |
| `GET /admin/directory/connections/{connection_id}` | `directoryDetails` | [directory.go](../../../internal/gateway/directory.go) |
| `PUT /admin/directory/mappings` | `directoryMapping` | [directory.go](../../../internal/gateway/directory.go) |
| `PUT /admin/directory/users/{user_id}/assignment` | `directoryAssignment` | [directory.go](../../../internal/gateway/directory.go) |
| `POST /login` | `login` | [routes.go](../../../internal/gateway/routes.go) |
| `POST /v2/login` | `login` | [routes.go](../../../internal/gateway/routes.go) |
| `POST /v3/login` | `login` | [routes.go](../../../internal/gateway/routes.go) |
| `POST /logout` | `logout` | [routes.go](../../../internal/gateway/routes.go) |
| `POST /v2/logout` | `logout` | [routes.go](../../../internal/gateway/routes.go) |
| `POST /v3/logout` | `logout` | [routes.go](../../../internal/gateway/routes.go) |
| `GET /auth/me` | `me` | [routes.go](../../../internal/gateway/routes.go) |
| `POST /auth/logout` | `logout` | [routes.go](../../../internal/gateway/routes.go) |
| `POST /bootstrap` | `bootstrap` | [routes.go](../../../internal/gateway/routes.go) |
| `GET /bootstrap/status` | `bootstrapStatus` | [routes.go](../../../internal/gateway/routes.go) |
| `GET /authorize/flow` | `authorizeFlow` | [routes.go](../../../internal/gateway/routes.go) |
| `POST /authorize/complete` | `authorizeComplete` | [routes.go](../../../internal/gateway/routes.go) |
| `POST /v1/mcp/server/oauth/{server_id}/token` | `mcpOAuthToken` | [routes.go](../../../internal/gateway/routes.go) |
| `POST /v1/chat/completions` | `chat` | [routes.go](../../../internal/gateway/routes.go) |
| `POST /chat/completions` | `chat` | [routes.go](../../../internal/gateway/routes.go) |
| `POST /v1/embeddings` | `embeddings` | [routes.go](../../../internal/gateway/routes.go) |
| `POST /embeddings` | `embeddings` | [routes.go](../../../internal/gateway/routes.go) |
| `POST /v1/completions` | `completions` | [routes.go](../../../internal/gateway/routes.go) |
| `POST /completions` | `completions` | [routes.go](../../../internal/gateway/routes.go) |
| `POST /v1/messages` | `messages` | [routes.go](../../../internal/gateway/routes.go) |
| `POST /v1/audio/translations` | `audioTranslations` | [routes.go](../../../internal/gateway/routes.go) |
| `POST /audio/translations` | `audioTranslations` | [routes.go](../../../internal/gateway/routes.go) |
| `POST /route_template/preview` | `routePreview` | [routes.go](../../../internal/gateway/routes.go) |
| `POST /flushall` | `cacheManagement` | [routes.go](../../../internal/gateway/routes.go) |
| `GET /cache/settings` | `cacheManagement` | [routes.go](../../../internal/gateway/routes.go) |
| `POST /cache/settings` | `cacheManagement` | [routes.go](../../../internal/gateway/routes.go) |
| `POST /cache/settings/test` | `cacheManagement` | [routes.go](../../../internal/gateway/routes.go) |
| `POST /delete` | `cacheManagement` | [routes.go](../../../internal/gateway/routes.go) |
| `GET /get/ui_theme_settings` | `uiTheme` | [routes.go](../../../internal/gateway/routes.go) |
| `PATCH /update/ui_theme_settings` | `uiTheme` | [routes.go](../../../internal/gateway/routes.go) |
| `POST /upload/logo` | `uiTheme` | [routes.go](../../../internal/gateway/routes.go) |
| `POST /prompts/test` | `promptTest` | [routes.go](../../../internal/gateway/routes.go) |
| `POST /search_tools/test_connection` | `searchToolTest` | [routes.go](../../../internal/gateway/routes.go) |
| `GET /cache/ping` | `cacheManagement` | [routes.go](../../../internal/gateway/routes.go) |
| `GET /ping` | `cacheManagement` | [routes.go](../../../internal/gateway/routes.go) |
| `GET /customer/list` | `customerList` | [routes.go](../../../internal/gateway/routes.go) |
| `GET /end_user/list` | `customerList` | [routes.go](../../../internal/gateway/routes.go) |


## internal/gateway/family

[目录契约](../../../internal/gateway/family/readme_cn.md)

| 方法与路径 | 处理器 | 注册源码 |
| --- | --- | --- |
| `POST /v1/responses` | `Responses` | [mount.go](../../../internal/gateway/family/mount.go) |
| `POST /responses` | `Responses` | [mount.go](../../../internal/gateway/family/mount.go) |


## internal/gateway/guard

[目录契约](../../../internal/gateway/guard/readme_cn.md)

| 方法与路径 | 处理器 | 注册源码 |
| --- | --- | --- |
| `POST /apply_guardrail` | `Apply` | [mount.go](../../../internal/gateway/guard/mount.go) |
| `POST /guardrails/apply_guardrail` | `Apply` | [mount.go](../../../internal/gateway/guard/mount.go) |


## internal/gateway/identity

[目录契约](../../../internal/gateway/identity/readme_cn.md)

| 方法与路径 | 处理器 | 注册源码 |
| --- | --- | --- |
| `GET /budget/reservations` | `BudgetReservations` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `POST /budget/reservations/{reservation_id}/reconcile` | `BudgetReconcile` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `POST /user/new` | `UserNew` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `GET /user/list` | `UserList` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `GET /user/filter/ui` | `UserFilterUI` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `GET /user/available_users` | `AvailableUsers` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `GET /user/info` | `UserInfo` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `GET /v2/user/info` | `UserInfo` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `POST /user/update` | `UserUpdate` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `POST /user/bulk_update` | `UserBulkBudgetUpdate` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `POST /user/delete` | `UserDelete` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `POST /user/set_password` | `UserSetPassword` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `POST /user/{user_id}/password` | `UserSetPassword` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `POST /organization/new` | `OrgNew` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `GET /organization/list` | `OrgList` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `GET /organization/info` | `OrgInfo` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `PATCH /organization/update` | `OrgUpdate` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `DELETE /organization/delete` | `OrgDelete` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `POST /organization/member_add` | `OrgMemberAdd` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `POST /organization/member_delete` | `OrgMemberRemove` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `DELETE /organization/member_delete` | `OrgMemberRemove` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `POST /team/new` | `TeamNew` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `GET /team/list` | `TeamList` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `GET /v2/team/list` | `TeamListV2` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `GET /team/available` | `TeamAvailable` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `GET /team/info` | `TeamInfo` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `POST /team/update` | `TeamUpdate` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `POST /team/delete` | `TeamDelete` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `POST /team/move` | `TeamMove` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `GET /team/models` | `TeamModels` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `GET /team/member_list` | `TeamMembers` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `POST /team/member_add` | `TeamMemberAdd` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `POST /team/member_update` | `TeamMemberUpdate` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `POST /team/member_delete` | `TeamMemberRemove` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `POST /project/new` | `ProjectNew` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `GET /project/list` | `ProjectList` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `POST /project/list` | `ProjectList` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `GET /project/info` | `ProjectInfo` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `POST /project/info` | `ProjectInfo` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `POST /project/update` | `ProjectUpdate` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `POST /project/delete` | `ProjectDelete` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `GET /route_template/list` | `RouteTemplateList` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `POST /route_template/new` | `RouteTemplateCreate` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `GET /route_template/{template_id}` | `RouteTemplateGet` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `POST /route_template/{template_id}/update` | `RouteTemplateUpdate` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `POST /route_template/{template_id}/delete` | `RouteTemplateDelete` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `GET /route_template/{template_id}/usage` | `RouteTemplateUsage` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `GET /route_template/binding` | `RouteTemplateBinding` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `POST /route_template/binding` | `RouteTemplateBinding` | [mount.go](../../../internal/gateway/identity/mount.go) |
| `GET /audit/logs` | `AuditLog` | [mount.go](../../../internal/gateway/identity/mount.go) |


## internal/gateway/keys

[目录契约](../../../internal/gateway/keys/readme_cn.md)

| 方法与路径 | 处理器 | 注册源码 |
| --- | --- | --- |
| `POST /key/generate` | `Generate` | [mount.go](../../../internal/gateway/keys/mount.go) |
| `GET /key/list` | `List` | [mount.go](../../../internal/gateway/keys/mount.go) |
| `GET /key/info` | `Info` | [mount.go](../../../internal/gateway/keys/mount.go) |
| `POST /v2/key/info` | `Info` | [mount.go](../../../internal/gateway/keys/mount.go) |
| `POST /key/delete` | `Delete` | [mount.go](../../../internal/gateway/keys/mount.go) |
| `POST /key/block` | `Block` | [mount.go](../../../internal/gateway/keys/mount.go) |
| `POST /key/unblock` | `Unblock` | [mount.go](../../../internal/gateway/keys/mount.go) |
| `POST /key/update` | `Update` | [mount.go](../../../internal/gateway/keys/mount.go) |
| `POST /key/bulk_update` | `BulkUpdate` | [mount.go](../../../internal/gateway/keys/mount.go) |
| `POST /key/regenerate` | `Regenerate` | [mount.go](../../../internal/gateway/keys/mount.go) |
| `POST /key/{key}/regenerate` | `Regenerate` | [mount.go](../../../internal/gateway/keys/mount.go) |
| `POST /key/{key}/reset_spend` | `ResetSpend` | [mount.go](../../../internal/gateway/keys/mount.go) |
| `GET /key/aliases` | `Aliases` | [mount.go](../../../internal/gateway/keys/mount.go) |
| `POST /key/health` | `Health` | [mount.go](../../../internal/gateway/keys/mount.go) |


## internal/gateway/models

[目录契约](../../../internal/gateway/models/readme_cn.md)

| 方法与路径 | 处理器 | 注册源码 |
| --- | --- | --- |
| `GET /v1/models` | `List` | [mount.go](../../../internal/gateway/models/mount.go) |
| `GET /v1/models/{id}` | `Detail` | [mount.go](../../../internal/gateway/models/mount.go) |
| `GET /models` | `List` | [mount.go](../../../internal/gateway/models/mount.go) |
| `GET /model/groups` | `Groups` | [mount.go](../../../internal/gateway/models/mount.go) |
| `PUT /model/fallback` | `SetFallback` | [mount.go](../../../internal/gateway/models/mount.go) |
| `PUT /model/default` | `SetDefault` | [mount.go](../../../internal/gateway/models/mount.go) |
| `GET /model/available` | `Available` | [mount.go](../../../internal/gateway/models/mount.go) |
| `GET /v2/model/info` | `Info` | [mount.go](../../../internal/gateway/models/mount.go) |
| `GET /model/cost_map/source` | `CostMapSource` | [mount.go](../../../internal/gateway/models/mount.go) |
| `POST /reload/model_cost_map` | `ReloadCostMap` | [mount.go](../../../internal/gateway/models/mount.go) |
| `POST /schedule/model_cost_map_reload` | `ScheduleCostMapReload` | [mount.go](../../../internal/gateway/models/mount.go) |
| `DELETE /schedule/model_cost_map_reload` | `CancelCostMapReload` | [mount.go](../../../internal/gateway/models/mount.go) |
| `GET /schedule/model_cost_map_reload/status` | `CostMapReloadStatus` | [mount.go](../../../internal/gateway/models/mount.go) |
| `GET /price/catalog` | `PriceList` | [mount.go](../../../internal/gateway/models/mount.go) |
| `POST /price/model` | `UpsertPriceModel` | [mount.go](../../../internal/gateway/models/mount.go) |
| `POST /price/model/listing` | `SetPriceListing` | [mount.go](../../../internal/gateway/models/mount.go) |
| `POST /price/model/reset` | `ResetPriceModel` | [mount.go](../../../internal/gateway/models/mount.go) |
| `POST /price/provider` | `UpsertPriceProvider` | [mount.go](../../../internal/gateway/models/mount.go) |
| `DELETE /price/provider` | `DeletePriceProvider` | [mount.go](../../../internal/gateway/models/mount.go) |
| `POST /model/new` | `New` | [mount.go](../../../internal/gateway/models/mount.go) |
| `POST /model/builtin/refresh` | `RefreshBuiltin` | [mount.go](../../../internal/gateway/models/mount.go) |
| `POST /model/builtin/models` | `ListBuiltin` | [mount.go](../../../internal/gateway/models/mount.go) |
| `POST /model/builtin/add` | `AddBuiltinModels` | [mount.go](../../../internal/gateway/models/mount.go) |
| `POST /model/update` | `Update` | [mount.go](../../../internal/gateway/models/mount.go) |
| `PATCH /model/{model_id}/update` | `Update` | [mount.go](../../../internal/gateway/models/mount.go) |
| `POST /model/delete` | `Delete` | [mount.go](../../../internal/gateway/models/mount.go) |
| `POST /model/disable` | `Disable` | [mount.go](../../../internal/gateway/models/mount.go) |
| `POST /model/enable` | `Enable` | [mount.go](../../../internal/gateway/models/mount.go) |
| `GET /model_group/info` | `GroupInfo` | [mount.go](../../../internal/gateway/models/mount.go) |


## internal/gateway/prefs

[目录契约](../../../internal/gateway/prefs/readme_cn.md)

| 方法与路径 | 处理器 | 注册源码 |
| --- | --- | --- |
| `GET /get/config/callbacks` | `Callbacks` | [mount.go](../../../internal/gateway/prefs/mount.go) |
| `GET /config/list` | `List` | [mount.go](../../../internal/gateway/prefs/mount.go) |
| `POST /config/update` | `Update` | [mount.go](../../../internal/gateway/prefs/mount.go) |
| `POST /config/field/update` | `FieldUpdate` | [mount.go](../../../internal/gateway/prefs/mount.go) |
| `POST /config/field/delete` | `FieldDelete` | [mount.go](../../../internal/gateway/prefs/mount.go) |


## internal/gateway/usage

[目录契约](../../../internal/gateway/usage/readme_cn.md)

| 方法与路径 | 处理器 | 注册源码 |
| --- | --- | --- |
| `GET /models/monitor` | `ModelMonitor` | [mount.go](../../../internal/gateway/usage/mount.go) |
| `GET /global/spend/teams` | `SpendTeams` | [mount.go](../../../internal/gateway/usage/mount.go) |
| `GET /guardrails/usage/overview` | `GuardrailOverview` | [mount.go](../../../internal/gateway/usage/mount.go) |
| `GET /guardrails/usage/detail/{guardrail_id}` | `GuardrailDetail` | [mount.go](../../../internal/gateway/usage/mount.go) |
| `GET /guardrails/usage/logs` | `GuardrailLogs` | [mount.go](../../../internal/gateway/usage/mount.go) |
| `GET /spend/logs/v2` | `LogsV2` | [mount.go](../../../internal/gateway/usage/mount.go) |
| `GET /spend/logs/ui` | `LogsV2` | [mount.go](../../../internal/gateway/usage/mount.go) |
| `GET /spend/logs/ui/{request_id}` | `LogByID` | [mount.go](../../../internal/gateway/usage/mount.go) |
| `GET /spend/logs/session/ui` | `SessionLogs` | [mount.go](../../../internal/gateway/usage/mount.go) |
| `GET /global/spend/logs` | `SpendLogs` | [mount.go](../../../internal/gateway/usage/mount.go) |
| `GET /global/spend/keys` | `SpendKeys` | [mount.go](../../../internal/gateway/usage/mount.go) |
| `GET /global/spend/models` | `SpendModels` | [mount.go](../../../internal/gateway/usage/mount.go) |
| `GET /global/spend/provider` | `SpendProvider` | [mount.go](../../../internal/gateway/usage/mount.go) |
| `POST /global/spend/end_users` | `SpendEndUsers` | [mount.go](../../../internal/gateway/usage/mount.go) |
| `GET /user/daily/activity` | `UserDailyActivity` | [mount.go](../../../internal/gateway/usage/mount.go) |
| `GET /user/daily/activity/aggregated` | `UserDailyActivityAggregated` | [mount.go](../../../internal/gateway/usage/mount.go) |
| `GET /team/daily/activity` | `TeamDailyActivity` | [mount.go](../../../internal/gateway/usage/mount.go) |
| `GET /team/daily/activity/aggregated` | `TeamDailyActivityAggregated` | [mount.go](../../../internal/gateway/usage/mount.go) |
| `GET /team/spend/by_user` | `TeamSpendByUser` | [mount.go](../../../internal/gateway/usage/mount.go) |
| `GET /organization/daily/activity` | `OrganizationDailyActivity` | [mount.go](../../../internal/gateway/usage/mount.go) |
| `GET /tag/daily/activity` | `UnsupportedDailyActivity` | [mount.go](../../../internal/gateway/usage/mount.go) |
| `GET /customer/daily/activity` | `UnsupportedDailyActivity` | [mount.go](../../../internal/gateway/usage/mount.go) |
| `GET /gateway/daily/activity` | `GatewayDailyActivity` | [mount.go](../../../internal/gateway/usage/mount.go) |
| `GET /global/activity` | `Activity` | [mount.go](../../../internal/gateway/usage/mount.go) |
| `GET /global/activity/model` | `ActivityModel` | [mount.go](../../../internal/gateway/usage/mount.go) |
| `GET /global/activity/cache_hits` | `ActivityCacheHits` | [mount.go](../../../internal/gateway/usage/mount.go) |
| `POST /spend/calculate` | `Calculate` | [mount.go](../../../internal/gateway/usage/mount.go) |
| `GET /spend/keys` | `Keys` | [mount.go](../../../internal/gateway/usage/mount.go) |
| `GET /spend/users` | `Users` | [mount.go](../../../internal/gateway/usage/mount.go) |
| `GET /tag/list` | `TagList` | [mount.go](../../../internal/gateway/usage/mount.go) |
| `GET /spend/tags` | `Tags` | [mount.go](../../../internal/gateway/usage/mount.go) |
| `GET /global/spend/tags` | `SpendTags` | [mount.go](../../../internal/gateway/usage/mount.go) |
| `GET /global/spend/all_tag_names` | `SpendTagNames` | [mount.go](../../../internal/gateway/usage/mount.go) |
| `POST /health/test_connection` | `HealthTestConnection` | [mount.go](../../../internal/gateway/usage/mount.go) |
| `GET /health/services` | `HealthServices` | [mount.go](../../../internal/gateway/usage/mount.go) |
| `GET /test` | `HealthTest` | [mount.go](../../../internal/gateway/usage/mount.go) |


## 目录调度与官方传输

上表覆盖 Go 源码直接注册的字面量入口。catalog/routes.json 另有协议调度；provider 注册 transport 另有固定官方路径。它们应与 handler 和端点能力一起阅读：

- [catalog 路由目录](../../../internal/catalog/routes.json)：请求分类与入口；支持状态以路由 family、处理器及已注册 transport 为准。
- [接线测试基线](../../testdata/catalog.json)：测试登记表面积，不能作为供应商功能承诺。
- [供应商传输](../../../internal/provider/readme_cn.md)：官方 create/get/list 方法和路径映射。

## internal/gateway/compliance

| 方法与路径 | 处理器 | 注册源码 |
| --- | --- | --- |
| `POST /compliance/eu-ai-act` | `complianceEU` | [compliance.go](../../../internal/gateway/compliance/compliance.go) |
| `POST /compliance/gdpr` | `complianceGDPR` | [compliance.go](../../../internal/gateway/compliance/compliance.go) |

## internal/gateway/health

| 方法与路径 | 处理器 | 注册源码 |
| --- | --- | --- |
| `GET /health/liveliness` | `healthLive` | [health.go](../../../internal/gateway/health/health.go) |
| `GET /health/liveness` | `healthLive` | [health.go](../../../internal/gateway/health/health.go) |
| `GET /health/readiness` | `healthReady` | [health.go](../../../internal/gateway/health/health.go) |
| `GET /health/readiness/details` | `healthDetails` | [health.go](../../../internal/gateway/health/health.go) |
| `GET /health` | `healthReady` | [health.go](../../../internal/gateway/health/health.go) |
| `GET /.well-known/litellm-ui-config` | `uiConfig` | [health.go](../../../internal/gateway/health/health.go) |
| `GET /litellm/.well-known/litellm-ui-config` | `uiConfig` | [health.go](../../../internal/gateway/health/health.go) |

## internal/gateway/publichub

| 方法与路径 | 处理器 | 注册源码 |
| --- | --- | --- |
| `GET /public/v1/model_hub` | `publicModelHub` | [public_hub.go](../../../internal/gateway/publichub/public_hub.go) |
| `GET /public/v1/model_hub/{facet}` | `publicModelHubFacet` | [public_hub.go](../../../internal/gateway/publichub/public_hub.go) |
| `GET /public/model_hub` | `publicModelHub` | [public_hub.go](../../../internal/gateway/publichub/public_hub.go) |
| `GET /public/model_hub/info` | `publicModelHubInfo` | [public_hub.go](../../../internal/gateway/publichub/public_hub.go) |
| `GET /model_hub` | `publicModelHub` | [public_hub.go](../../../internal/gateway/publichub/public_hub.go) |
| `GET /model_hub/{facet}` | `publicModelHubFacet` | [public_hub.go](../../../internal/gateway/publichub/public_hub.go) |
| `POST /model_hub/update_useful_links` | `updateUsefulLinks` | [public_hub.go](../../../internal/gateway/publichub/public_hub.go) |
| `GET /public/endpoints` | `publicEndpoints` | [public_hub.go](../../../internal/gateway/publichub/public_hub.go) |

## internal/gateway/tokens

| 方法与路径 | 处理器 | 注册源码 |
| --- | --- | --- |
| `POST /utils/token_counter` | `tokenCounter` | [tokens.go](../../../internal/gateway/tokens/tokens.go) |
| `GET /utils/supported_openai_params` | `supportedOpenAIParams` | [tokens.go](../../../internal/gateway/tokens/tokens.go) |
| `POST /utils/transform_request` | `transformRequest` | [tokens.go](../../../internal/gateway/tokens/tokens.go) |

[SAML 配置与元数据注册](saml-login.md)

[未知费用的人工对账](budget-reconciliation.md)

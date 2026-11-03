# provider

[English](readme.md)

这里定义上游协议执行方式。已有协议通过页面添加模型供应商和部署；新协议在 `internal/provider/<supplier>/` 实现，并在 `all/all.go` 导入。具体步骤见[添加模型供应商与开发新协议](../../docs/development/zh-CN/provider-catalog.md)，包括 NewAPI 页面操作、provider 注册、执行层接入及验收。

## 如何由其他模块接入

这里的“对外”指仓库内其他 Go 包。Go 的 `internal` 规则限制了仓库外的直接导入；HTTP 客户端使用已挂载的网关接口。

本目录是协议体系导航，不提供根包构造函数。启动导入 all，入口由 registry.Match 获取 Hit，部署经 deployment 校验后由数据面执行；新协议需要同时登记入口、传输和计量。

| 入口或资源 | 调用方式与返回结果 |
| --- | --- |
| all | 启动登记全部供应商 |
| registry.Match / deployment.ResolveHit | 匹配入口并验证绑定 |
| measurement / contract | 事实提取与类型契约 |

### 接入 demo

在仓库根目录执行以下命令。

```sh
go test ./internal/provider/... -count=1
```

执行协议登记、匹配、能力与绑定测试。最小空白导入 demo 见 [all](all/readme_cn.md)，绑定 demo 见 [deployment](deployment/readme_cn.md)。

### 错误、状态与副作用

注册协议不创建账号或连接。配置显式选择 transport 和 endpoint_types；任务必须保存原部署与创建事实，不能随轮询换目标。

## 接入前先分类

- **NewAPI / 公司中转 / 其它 OpenAI 兼容服务**：创建 OpenAI 兼容命名凭据，填写实际地址和密钥；模型部署显式选择 `bypass_openai_chat` 等已登记执行方式。不必创建 provider 目录，也不必逐个登记普通型号。
- **相同协议，不同认证或固定路径**：新增供应商包并登记 `Transport`，必要时登记 `Supplier` 和 `Model`，最后加入 `all/all.go`。参考 `qiniu/seedance.go`；不要复制价格目录当成协议实现。
- **Suno 自有音乐任务协议 / 其它全新载荷协议**：当前没有 Suno 内置执行器。除了注册传输，还要增加公开端点、入口与任务处理、计量和 UI 支持；只添加目录、Logo、凭据字段或 `init()` 注册不足以完成接入。

## 最小代码阅读顺序

1. `contract/type.go`：`Transport`、`Action`、`AuthConfig`、`TaskBilling` 的真实字段。
2. `capability/endpoints.go`：公开协议目录与兼容规则；新媒体协议必须显式增加端点。
3. `registry/registry.go`、`registry/models.go`：登记执行器和目录型号，不创建用户连接。
4. `deployment/validate.go`、`deployment/catalog_binding.go`：检查传输、连接白名单、目录与固定型号。
5. `../dataplane/transport/bypass_url.go`、`../dataplane/execution/official.go`：路径拼接、原生请求、任务钉与结算。
6. `all/all.go`：编译时加载；新增后重建并重启网关，不支持上传 Go 插件热加载。

凭据类型、连接名称、`catalog_id`、上游模型 ID、对外模型名、`transport` 是不同字段。`custom_llm_provider: suno` 不会自动选择音乐协议；`model_info.transport` 必须已登记。标准客户接口和匹配上游协议的 Chat、Responses、Messages 对话透传入口由执行能力自动开放；其他 Bypass 入口仍需在 `endpoint_types` 显式启用。

| Directory | Responsibility |
|---|---|
| [contract](contract/readme_cn.md) | 共享数据契约 |
| [capability](capability/readme_cn.md) | 公开端点与协议匹配 |
| [registry](registry/readme_cn.md) | 内置注册与路径匹配 |
| [deployment](deployment/readme_cn.md) | 显式绑定、校验与候选筛选 |
| [measurement](measurement/readme_cn.md) | 响应/SSE 用量和任务计量事实 |
| [openai](openai/readme_cn.md) | OpenAI 原生协议注册 |
| [anthropic](anthropic/readme_cn.md) | Anthropic Messages 注册 |
| [google](google/readme_cn.md) | Gemini 和 Vertex 注册 |
| [qiniu](qiniu/readme_cn.md) | 七牛任务与 FAL 注册 |
| [volcengine](volcengine/readme_cn.md) | 方舟任务注册 |
| [all](all/readme_cn.md) | 启动时装配全部供应商 |

依赖方向：厂商 → registry/measurement → contract；deployment → registry/capability/llm/dialogue。dataplane 执行请求，gateway 负责鉴权、持久化和装配。

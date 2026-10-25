# 独立 API 参考

产品指南讲部署与使用，开发文档讲实现和测试；字段、请求和响应结构从同一份 OpenAPI 在独立 Swagger UI 中展示。网站顶部的“API 参考”进入 `/reference/zh/index.html`，英文入口为 `/reference/en/index.html`。本地预览支持 `/xhub/` 等部署前缀。

## 覆盖与边界

只读取 `contracts/{suppliers,users,keys,models,usage}/openapi.json`：供应商5、用户11、Key14、模型/端点9、日志/用量9，共48个操作。构建逐项核对迁移清单，缺失契约、未登记操作或无法解析的引用阻止构建。原始 JSON 可下载；展示语言只选择说明元数据，不改变结构、默认值或示例。

Bypass、流式推理、SSO/SCIM、组织团队生命周期及外围兼容目录不在本批契约范围内。已有协议指南保留，不将其当作已验收的 Swagger 操作。

## 阅读与调用

打开时为只读，不自动调用线上接口。手工填写有权访问的网关后才能启用调用，在 Swagger `Authorize` 输入网关凭据，再手工 `Try it out` / `Execute`。目标需允许跨域请求，HTTPS页面不能调用HTTP网关；不要通过关闭浏览器安全策略绕过。

地址及授权仅存在内存；刷新恢复只读，切换领域或目标重建 Swagger 并清授权。Swagger 标准控件保留原生英文，页面自有导航、状态与契约说明提供中英切换，不做 DOM 替换。调用仍遵守既有服务器鉴权、预算和参数规则。

## 构建与兼容

```bash
npm ci --prefix website
npm test --prefix website
npm run build --prefix website
npm run test:e2e --prefix website
```

Swagger UI固定版本随站点打包，无运行时CDN或外部验证服务。OpenAPI仍在版本库接受CI校验，不增加线上校验中间件。旧生成Markdown链接保留迁移页，导航与搜索不再重复列出字段表；产品与开发资料侧栏分离。

真实网关测试入口为 `swagger-reference-en.test.ts` 和 `swagger-reference-zh-CN.test.ts`，使用隔离PG验证浏览器401→授权→创建用户→读回、授权清除与清理。发布工作流先构建站点再执行这两条。商业上游、生产SSO和线上反向代理不在测试范围。

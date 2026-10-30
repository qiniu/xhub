# XHub 下行连接超时

`xhub-client-timeout.conf` 用于已有 Nginx/OpenResty 入口。它仅设置客户端写入空闲超时为 60 秒（Nginx 默认值），避免 XHub 继承过短的全局值。不是上游读取超时，也不是允许请求无限等待。

镜像更新不会修改外部 Nginx。上线时需要运维单独操作：

1. 备份实际使用的主配置、XHub vhost 和 `nginx -T` 输出（输出可能含敏感配置，留在服务器）。
2. 把片段复制到受管理的配置目录，在 **XHub 专用 server 或实际代理 location** 内 include。检查更深层 location 没有覆盖它；已有同层 `send_timeout` 应替换，不能重复声明。
3. 用当前运行实例的二进制和 `-c` 路径执行 `nginx -t`，再执行 reload；失败不得 reload。
4. 验证真实对话、流式续接以及慢客户端恢复。回退时恢复备份、再次 `nginx -t`，然后 reload。

示例（路径按实际环境调整）：

```nginx
server {
    # 保留原有 listen / TLS / upstream / 鉴权 / 正文上限等配置。
    include /etc/nginx/snippets/xhub-client-timeout.conf;
}
```

本仓库的 `python3 -m unittest scripts.test_nginx_slow_client -v` 使用本地 Docker Nginx 和 Node 客户端，在同一网络命名空间内暂停读取 3.5 秒：旧 2 秒配置必须发生真实写超时，应用此片段后必须完整接收 32 MiB。测试会清理容器与文件，不操作线上。

控制台 Next 的正文缓存为 65 MiB，网关准入上限仍为 64 MiB；额外余量让网关读取到超限探测字节后明确返回 413，避免 Next 先截断而让网关等待缺失正文。Next 会在内存缓存正文，大请求并发的内存占用需要按实际容量评估。入口 Nginx 若配置更小的正文上限会提前拒绝，不会由此片段改动。

取消日志中的 `cancellation_source: unknown` 表示服务端无法确认哪个客户端/代理触发取消。`error_origin: gateway` 仅表示错误事件由网关生成，不表示底层网络故障一定由网关造成。客户端已断开时，失败事件可能仅进入内部捕获日志，不能保证已送达客户端。只有供应商明确返回的错误才保留为供应商错误；不要仅依据 HTTP 499 或 `context canceled` 归责用户。

参考：[Nginx send_timeout](https://nginx.org/en/docs/http/ngx_http_core_module.html#send_timeout)、[Next 正文缓存行为](https://nextjs.org/docs/app/api-reference/config/next-config-js/proxyClientMaxBodySize)。

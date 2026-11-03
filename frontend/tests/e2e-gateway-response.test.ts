import { describe, expect, it } from "vitest";
import { isSuccessfulGatewayGet } from "../e2e/gateway-response";

const path = "/team/daily/activity/aggregated";

/** 验证真实响应观察器兼容直连与同源代理；纯数据输入，无外部依赖或清理。 */
describe("E2E gateway response matching", () => {
  /** 正常路径及查询串必须匹配，避免发布模式已恢复却持续等待；不创建资源。 */
  it.each([
    `http://127.0.0.1:4100${path}`,
    `http://127.0.0.1:4100${path}?start_date=2025-01-01`,
    `http://localhost:3100/gw${path}`,
    `https://console.example.test/gw${path}?page=1&end_date=2026-10-25`,
  ])("accepts the exact gateway route: %s", (url) => {
    expect(isSuccessfulGatewayGet({ url, method: "GET", status: 200 }, path)).toBe(true);
  });

  /** 失败响应、其他方法、相似路径及非法 URL 不能冒充成功恢复；无资源需清理。 */
  it.each([
    { url: `http://localhost:3100/gw${path}`, method: "GET", status: 500 },
    { url: `http://localhost:3100/gw${path}`, method: "GET", status: 401 },
    { url: `http://localhost:3100/gw${path}`, method: "GET", status: 204 },
    { url: `http://localhost:3100/gw${path}`, method: "POST", status: 200 },
    { url: `http://localhost:3100/gw${path}`, method: "OPTIONS", status: 200 },
    { url: `http://localhost:3100/other${path}`, method: "GET", status: 200 },
    { url: `http://localhost:3100/gw/gw${path}`, method: "GET", status: 200 },
    { url: `http://localhost:3100/gw${path}/extra`, method: "GET", status: 200 },
    { url: "http://localhost:3100/gw/team/daily/activity", method: "GET", status: 200 },
    { url: `http://localhost:3100/other?redirect=${path}`, method: "GET", status: 200 },
    { url: "invalid-url", method: "GET", status: 200 },
  ])("rejects unrelated or unsuccessful responses: $url $method $status", (response) => {
    expect(isSuccessfulGatewayGet(response, path)).toBe(false);
  });
});

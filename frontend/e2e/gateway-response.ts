/** 浏览器响应的最小只读信息；调用方从真实 Playwright 响应提取，不提供替代响应。 */
interface GatewayResponse {
  url: string;
  method: string;
  status: number;
}

/**
 * 判断 E2E 观察到的响应是否为指定网关 GET 的成功结果。
 * response 为真实响应摘要，path 为不带查询串的网关绝对路径；返回是否匹配。
 * 支持本地直连和发布控制台的同源 /gw 代理；只接受这两种完整路径，
 * 避免任意后缀、查询参数中的路径或其他方法/失败状态误满足恢复断言。
 * 非法 URL 返回 false，无网络调用或状态修改。
 */
export function isSuccessfulGatewayGet(response: GatewayResponse, path: string): boolean {
  if (response.method !== "GET" || response.status !== 200) return false;
  try {
    const pathname = new URL(response.url).pathname;
    return pathname === path || pathname === `/gw${path}`;
  } catch {
    return false;
  }
}

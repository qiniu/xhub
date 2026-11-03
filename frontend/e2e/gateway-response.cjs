/** 匹配 E2E 观察到的真实网关响应；responseURL/actualMethod 来自浏览器响应，endpoint/expectedMethod 指定业务操作。
 * 返回方法与路径是否精确匹配；仅支持直连路径及控制台 /gw 代理，不忽略其他前缀或资源 ID。
 * 供页面网络等待调用；URL 非法时返回 false，无请求、拦截或其他副作用。
 * @param {string} responseURL
 * @param {string} actualMethod
 * @param {string} endpoint
 * @param {string} expectedMethod
 * @returns {boolean}
 */
function matchesGatewayResponse(responseURL, actualMethod, endpoint, expectedMethod) {
  if (actualMethod !== expectedMethod) return false;
  try {
    const path = new URL(responseURL).pathname;
    return path === endpoint || path === `/gw${endpoint}`;
  } catch {
    return false;
  }
}
module.exports = { matchesGatewayResponse };

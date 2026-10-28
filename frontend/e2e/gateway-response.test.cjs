const assert = require('node:assert/strict');
const test = require('node:test');
const { matchesGatewayResponse } = require('./gateway-response.cjs');

/** 复现发布 CI 同源代理响应被漏掉；输入为真实 trace 的两种路径，预期均被精确匹配，无外部资源需清理。 */
test('直连与 /gw 代理的同一管理请求都能匹配', () => {
  for (const base of ['http://127.0.0.1:4100', 'http://xhub-http.test:3100/gw']) {
    assert.equal(matchesGatewayResponse(base + '/search_tools', 'POST', '/search_tools', 'POST'), true, base);
    assert.equal(matchesGatewayResponse(base + '/chat/completions', 'POST', '/chat/completions', 'POST'), true, base);
    assert.equal(matchesGatewayResponse(base + '/policies', 'POST', '/policies', 'POST'), true, base);
    assert.equal(matchesGatewayResponse(base + '/v1/chat/completions', 'POST', '/v1/chat/completions', 'POST'), true, base);
    assert.equal(matchesGatewayResponse(base + '/prompts/test', 'POST', '/prompts/test', 'POST'), true, base);
  }
});

/** 查询参数不属于路由身份，编码 ID 必须保持一致；使用固定字符串，验证正确匹配且无状态需清理。 */
test('保留编码资源 ID 并允许查询参数', () => {
  assert.equal(matchesGatewayResponse('http://localhost/gw/prompts/a%2Fb?view=full', 'PATCH', '/prompts/a%2Fb', 'PATCH'), true);
  assert.equal(matchesGatewayResponse('http://localhost/gw/prompts/a/b', 'PATCH', '/prompts/a%2Fb', 'PATCH'), false);
});

/** 错误方法、其他资源和任意前缀不能误满足等待；预期 false，防止用后缀弱匹配制造假成功，无资源需清理。 */
test('拒绝错误操作和相似路径', () => {
  for (const [url, method] of [
    ['http://localhost/gw/search_tools', 'GET'],
    ['http://localhost/other/search_tools', 'POST'],
    ['http://localhost/gw/search_tools/other', 'POST'],
    ['http://localhost/gw/search_tools/', 'POST'],
    ['http://localhost/gw/gw/search_tools', 'POST'],
    ['http://localhost/search_tools/test_connection', 'POST'],
  ]) assert.equal(matchesGatewayResponse(url, method, '/search_tools', 'POST'), false, url + ' ' + method);
});

/** 非法 URL 不应中断响应观察器；固定无效输入预期 false，不发网络请求或产生清理副作用。 */
test('无效 URL 返回不匹配', () => {
  for (const url of ['', 'not a URL', '/search_tools']) {
    assert.equal(matchesGatewayResponse(url, 'POST', '/search_tools', 'POST'), false);
  }
});

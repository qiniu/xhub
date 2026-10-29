const { matchesGatewayResponse } = require('./gateway-response.cjs');
/** 判断是否需要读取可选设置的失败正文；只读元信息，无网络或状态副作用。 */
function isOptionalSettingsRead({url,method,status}) {
 return status===501 && ['/get/ui_settings','/sso/get/ui_settings','/get/user_banner'].some(path => matchesGatewayResponse(url, method, path, 'GET'));
}
/** 校验登录时可选设置的明确不支持契约；输入为真实响应元信息和JSON，返回布尔值，无副作用。
 * 只接受三个GET、501、结构化类型及no-store；其余5xx仍让页面巡检失败，不能把失败计为功能成功。
 */
function isExpectedOptionalSettingsFailure({url, method, status, cacheControl, body}) {
  return status === 501 && cacheControl === 'no-store'
    && body?.error?.type === 'unsupported_settings_operation'
    && body?.error?.code === '501' && typeof body?.error?.message === 'string' && body.error.message.length > 0
    && isOptionalSettingsRead({url,method,status});
}
module.exports = { isExpectedOptionalSettingsFailure, isOptionalSettingsRead };

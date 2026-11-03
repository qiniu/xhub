/** 清理护栏测试独占资源；state 提供 ID、鉴权头和本地 fixture，api 为独立请求上下文。
 * 返回 Promise<void>；调用方的 fixture teardown 执行，不能依赖已超时/关闭的 page.request。
 * 所有操作均尝试一次，单次 HTTP 最多 10 秒；汇总任何失败，不将失败或异常当成功。
 * @param {{id:string,modelID:string,headers:Record<string,string>,fixture:{close:()=>Promise<void>}}} state
 * @param {import('@playwright/test').APIRequestContext} api
 * @param {string} gateway
 */
async function cleanupGuardResources(state, api, gateway) {
  const jobs = [
    async () => {
      if (!state.id) return;
      const r = await api.delete(gateway + '/guardrails/' + encodeURIComponent(state.id), {headers:state.headers, timeout:10000});
      if (r.status() !== 200) throw new Error('guardrail cleanup HTTP ' + r.status());
    },
    async () => {
      if (!state.modelID) return;
      const r = await api.post(gateway + '/model/delete', {headers:state.headers,data:{id:state.modelID},timeout:10000});
      if (r.status() !== 200) throw new Error('model cleanup HTTP ' + r.status());
    },
    () => state.fixture.close(),
  ];
  const results = await Promise.allSettled(jobs.map(job => Promise.resolve().then(job)));
  const errors = results.filter(result => result.status === 'rejected').map(result => result.reason);
  if (errors.length) throw new AggregateError(errors, 'Guard resource cleanup failed');
}
module.exports = {cleanupGuardResources};

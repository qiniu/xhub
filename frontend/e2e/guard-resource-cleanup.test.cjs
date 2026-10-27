const {test} = require('node:test');
const assert = require('node:assert/strict');
const {cleanupGuardResources} = require('./guard-resource-cleanup.cjs');

/** 构造确定性清理边界；故障按名称注入，记录所有调用，返回内存夹具；无外部资源。 */
function fixture(failure) {
  const calls=[];
  const api={
    delete:async(url,options)=>{calls.push({name:'guard',url,options});if(failure==='throw')throw Error('connection closed');return {status:()=>failure==='guard'?503:200};},
    post:async(url,options)=>{calls.push({name:'model',url,options});return {status:()=>failure==='model'?500:200};},
  };
  const state={id:'guard/id',modelID:'model-id',headers:{Authorization:'Bearer fixture-only'},fixture:{close:async()=>{calls.push({name:'listener'});if(failure==='listener')throw Error('close failed');}}};
  return {api,state,calls};
}
/** 成功路径必须清理全部资源且有界请求；只操作内存替身，不代表真实网关 E2E。 */
test('cleanup attempts all resources with bounded independent requests',async()=>{
  const {api,state,calls}=fixture();await cleanupGuardResources(state,api,'http://fixture');
  assert.deepEqual(calls.map(c=>c.name),['guard','model','listener']);
  assert.equal(calls[0].url,'http://fixture/guardrails/guard%2Fid');
  for(const c of calls.slice(0,2)){assert.equal(c.options.timeout,10000);assert.deepEqual(c.options.headers,state.headers);}
  assert.deepEqual(calls[1].options.data,{id:'model-id'});
});
/** 无 ID 表示创建尚未完成；仅关闭监听，保证空/边界状态无需猜测删除目标。 */
test('cleanup with no saved IDs still closes listener',async()=>{
  const {api,state,calls}=fixture();state.id='';state.modelID='';await cleanupGuardResources(state,api,'http://fixture');assert.deepEqual(calls.map(c=>c.name),['listener']);
});
/** 故障不能短路其他清理也不能伪装通过；遍历 HTTP、网络异常和监听关闭失败，内存自动回收。 */
for(const failure of ['guard','model','throw','listener']) {
  test('cleanup propagates '+failure+' and still attempts remaining resources',async()=>{
    const {api,state,calls}=fixture(failure);
    await assert.rejects(cleanupGuardResources(state,api,'http://fixture'),error=>error instanceof AggregateError && error.errors.length===1);
    assert.deepEqual(calls.map(c=>c.name),['guard','model','listener']);
  });
}

const {test} = require('node:test');
const assert = require('node:assert/strict');
const {createServer} = require('node:http');
const {spawn} = require('node:child_process');
const fs = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');

/** 启动真实 Playwright 子进程验证 fixture 生命周期；mode 为正常、抛错或超时。
 * 只启动本机清理端点，不使用浏览器/数据库或生产凭据；验证业务状态与独立 teardown 请求，finally 删临时文件并关闭监听。
 */
async function exerciseTeardown(mode) {
  const calls=[];
  const recover = ['lostResponse','decodeFailure','lookupFailure','emptyLookup'].includes(mode);
  const deletedUsers=[];
  const server=createServer((req,res)=>{
    const url=new URL(req.url,'http://localhost');calls.push(req.method+' '+url.pathname);
    let body='';req.on('data',chunk=>body+=chunk);
    req.on('end',()=>{
      if(url.pathname==='/user/delete')deletedUsers.push(JSON.parse(body).user_id);
      const failed=(mode==='cleanupFailure' && req.method==='DELETE') ||
        (mode==='listFailure' && url.pathname==='/v1/access_group') ||
        (mode==='userFailure' && req.method==='POST') ||
        (mode==='lookupFailure' && url.pathname==='/user/list');
      res.writeHead(failed?500:200,{'Content-Type':'application/json'});
      if(url.pathname==='/user/list'){
        assert.equal(url.searchParams.get('user_email'),'owned-policy@example.invalid');
        const page=Number(url.searchParams.get('page'));
        res.end(JSON.stringify({total_pages:mode==='emptyLookup'?0:2,users:mode==='emptyLookup'?[]:page===1?
          [{user_id:'untouched',user_email:'other-owned-policy@example.invalid'}]:
          [{user_id:'owned-user',user_email:'owned-policy@example.invalid'}]}));
      } else res.end(JSON.stringify(req.method==='GET'?[{id:'owned-policy',name:'owned-policy'},{id:'other',name:'untouched'}]:{}));
    });
  });
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  const dir=await fs.mkdtemp(path.join(os.tmpdir(),'xhub-access-fixture-'));
  try {
    const gateway='http://127.0.0.1:'+server.address().port;
    const fixturePath=path.join(__dirname,'access-group-config-service-scenario.ts');
    await fs.writeFile(path.join(dir,'flow.spec.ts'),`
import {test} from ${JSON.stringify(fixturePath)};
test('fixture lifecycle',async({accessResources:r})=>{
 test.setTimeout(300);
 r.name='owned-policy';r.userID=${recover?'undefined':"'owned-user'"};r.headers={Authorization:'Bearer test'};
 ${{timeout:'await new Promise(()=>{});',failure:'throw new Error("intentional test failure");',success:'',cleanupFailure:'',listFailure:'',userFailure:'',lostResponse:'await new Promise(()=>{});',decodeFailure:'JSON.parse("invalid response");',lookupFailure:'',emptyLookup:''}[mode]}
});
`);
    const report=path.join(dir,'result.json');
    await fs.writeFile(path.join(dir,'config.cjs'),`module.exports={testDir:__dirname,workers:1,retries:0,reporter:[['json',{outputFile:${JSON.stringify(report)}}]],outputDir:${JSON.stringify(path.join(dir,'results'))}};`);
    const result=await new Promise((resolve,reject)=>{
      const child=spawn(process.execPath,[require.resolve('@playwright/test/cli'),'test','--config',path.join(dir,'config.cjs')],{env:{...process.env,E2E_GATEWAY:gateway},stdio:['ignore','pipe','pipe']});
      let output='';child.stdout.on('data',chunk=>output+=chunk);child.stderr.on('data',chunk=>output+=chunk);
      const watchdog=setTimeout(()=>{child.kill('SIGKILL');reject(new Error('fixture subprocess did not finish: '+output));},20000);
      child.on('error',error=>{clearTimeout(watchdog);reject(error)});
      child.on('exit',code=>{clearTimeout(watchdog);resolve({code,output})});
    });
    assert.equal(result.code,['success','emptyLookup'].includes(mode)?0:1,result.output);
    const json=JSON.parse(await fs.readFile(report,'utf8'));
    const outcome=json.suites[0].specs[0].tests[0].results[0];
    assert.equal(outcome.status,{success:'passed',failure:'failed',timeout:'timedOut',cleanupFailure:'failed',listFailure:'failed',userFailure:'failed',lostResponse:'timedOut',decodeFailure:'failed',lookupFailure:'failed',emptyLookup:'passed'}[mode]);
    const expected=['GET /v1/access_group'];
    if(mode!=='listFailure')expected.push('DELETE /v1/access_group/owned-policy');
    if(recover)expected.push('GET /user/list');
    if(['lostResponse','decodeFailure'].includes(mode))expected.push('GET /user/list');
    const shouldDelete=!['lookupFailure','emptyLookup'].includes(mode);
    if(shouldDelete)expected.push('POST /user/delete');
    assert.deepEqual(calls.sort(),expected.sort());
    assert.deepEqual(deletedUsers,shouldDelete?['owned-user']:[]);

  } finally {
    server.closeAllConnections();await new Promise(resolve=>server.close(resolve));
    await fs.rm(dir,{recursive:true,force:true});
  }
}
/** 正常、异常和超时都要走独立清理；组列表/删除故障仍尝试删用户，任一清理500都必须失败；子进程使用真实 HTTP 调用，故意失败不等于业务 E2E 通过。 */
for(const mode of ['success','failure','timeout','cleanupFailure','listFailure','userFailure','lostResponse','decodeFailure','lookupFailure','emptyLookup']) test('access group fixture teardown after '+mode,()=>exerciseTeardown(mode));

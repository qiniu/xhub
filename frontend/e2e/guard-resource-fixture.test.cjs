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
  const server=createServer((req,res)=>{calls.push(req.method+' '+req.url);req.resume();res.writeHead(200,{'Content-Type':'application/json'});res.end('{}');});
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  const dir=await fs.mkdtemp(path.join(os.tmpdir(),'xhub-guard-fixture-'));
  try {
    const gateway='http://127.0.0.1:'+server.address().port;
    const fixturePath=path.join(__dirname,'guard-resource-fixture.ts');
    await fs.writeFile(path.join(dir,'flow.spec.ts'),`
import {test} from ${JSON.stringify(fixturePath)};
test('fixture lifecycle',async({guardResources:r})=>{
 test.setTimeout(300);
 r.id='owned-guard';r.modelID='owned-model';
 ${{timeout:'await new Promise(()=>{});',failure:'throw new Error("intentional test failure");',success:''}[mode]}
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
    assert.equal(result.code,mode==='success'?0:1,result.output);
    const json=JSON.parse(await fs.readFile(report,'utf8'));
    const outcome=json.suites[0].specs[0].tests[0].results[0];
    assert.equal(outcome.status,{success:'passed',failure:'failed',timeout:'timedOut'}[mode]);
    assert.deepEqual(calls.sort(),['DELETE /guardrails/owned-guard','POST /model/delete']);
  } finally {
    server.closeAllConnections();await new Promise(resolve=>server.close(resolve));
    await fs.rm(dir,{recursive:true,force:true});
  }
}
/** 正常、异常和超时都要走独立清理；子进程使用真实 HTTP 调用，故意失败不等于业务 E2E 通过。 */
for(const mode of ['success','failure','timeout']) test('guard fixture teardown after '+mode,()=>exerciseTeardown(mode));

import {test,expect} from "@playwright/test";
import {createServer} from "node:http";
import {once} from "node:events";
import {GATEWAY,loginAdmin,sessionBearer,stableGoto} from "../../helpers";

// 双语真实链路：管理员创建隔离模型，HTTP上游返回首帧/中途错误，客户端和日志抽屉保留同一诊断。
// 前置真实网关/PG/浏览器；验证 Chat 原生及 Responses 转换和错误后的成功请求；finally 删除模型并关闭本地服务，schema 由运行器清理。
for(const locale of ["en","zh-CN"] as const) test("upstream stream error details "+locale,async({page})=>{
 test.setTimeout(120000);
 let partial=false,fail=true,calls=0;
 const marker="Vendor capacity exhausted: 保留原文";
 // 本地HTTP供应商夹具：接收真实请求，返回协议流，便于验证首帧失败与部分输出后失败；资源在finally回收。
 const upstream=createServer((req,res)=>{
  calls++;
  req.resume();
  res.writeHead(200,{"Content-Type":"text/event-stream","X-Request-Id":"req-upstream-header"});
  if(partial||!fail)res.write('data: {"id":"r-stream","choices":[{"index":0,"delta":{"content":"Partial answer"}}]}\n\n');
  if(fail)res.end("data: "+JSON.stringify({type:"error",error:{code:"vendor_capacity",type:"overloaded_error",message:marker,details:{retryable:true}},request_id:"req-upstream-body"})+"\n\n");
  else res.end('data: {"id":"r-stream","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}\n\ndata: [DONE]\n\n');
 });
 upstream.listen(0,"127.0.0.1");await once(upstream,"listening");
 const port=(upstream.address() as {port:number}).port;
 let modelID="";
 await loginAdmin(page);
 const headers={Authorization:"Bearer "+await sessionBearer(page)};
 const model="stream-detail-"+locale+"-"+Date.now();
 try{
  await page.getByRole("button",{name:locale==="en"?"English":"中文",exact:true}).click();
  const created=await page.request.post(GATEWAY+"/model/new",{headers,data:{model_name:model,litellm_params:{model,custom_llm_provider:"openai",api_base:`http://127.0.0.1:${port}/v1`,api_key:"local-fixture",input_cost_per_token:0.000001,output_cost_per_token:0.000002},model_info:{transport:"bypass_openai_chat",pricing_source:"manual",endpoint_types:["chat","responses"]}}});
  expect(created.status(),await created.text()).toBe(200);modelID=(await created.json()).model_info.id;
  for(const path of ["/chat/completions","/responses"]){for(const midway of [false,true]){
   partial=midway;const before=calls;
   const data=path==="/responses"?{model,input:"Explain the failure.",stream:true}:{model,messages:[{role:"user",content:"Explain the failure."}],stream:true};
   const response=await page.request.post(GATEWAY+path,{headers,data});
   const text=await response.text();
   expect([200,502]).toContain(response.status());
   for(const value of [marker,"vendor_capacity","overloaded_error","req-upstream-body"])expect(text).toContain(value);
   expect(calls-before,"不能重放已失败的供应商请求").toBe(1);
   const call=response.headers()["x-litellm-call-id"];
   await expect.poll(async()=> (await page.request.get(GATEWAY+"/spend/logs/ui/"+call,{headers})).status()).toBe(200);
   const log=await (await page.request.get(GATEWAY+"/spend/logs/ui/"+call,{headers})).json();
   expect(log.status).toBe("error");expect(log.metadata.upstream_response.status_code).toBe(200);
   expect(log.metadata.upstream_response.error.error.message).toBe(marker);
   await stableGoto(page,"/logs?log_id="+call);
   const dialog=page.getByRole("dialog");await expect(dialog).toBeVisible();
   const details=dialog.getByRole("region",{name:/完整错误详情|全部错误详情|Full Error Details/});
   await expect(details).toContainText(marker);
   await expect(details).toContainText("req-upstream-body");
   await expect(details).toContainText("vendor_capacity");
   await expect(dialog.getByRole("alert")).toContainText("502");
   await page.keyboard.press("Escape");
  }}
  fail=false;
  const success=await page.request.post(GATEWAY+"/responses",{headers,data:{model,input:"Respond successfully.",stream:true}});
  expect(success.status()).toBe(200);expect(await success.text()).toContain("response.completed");
 }finally{
  if(modelID)expect((await page.request.post(GATEWAY+"/model/delete",{headers,data:{id:modelID}})).status()).toBe(200);
  upstream.closeAllConnections();await new Promise<void>((resolve,reject)=>upstream.close(err=>err?reject(err):resolve()));
 }
});

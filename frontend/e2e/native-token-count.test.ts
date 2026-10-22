import {expect, test} from "@playwright/test";
import {GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto} from "./helpers";

/** 真实浏览器验证标准与透传计数、零费用日志和日志详情；前置隔离后台及本地上游。
 * 不使用生产凭据；finally删除测试Key和部署，运行器回收日志schema。 */
test("native token count remains visible and unbilled", async ({page}) => {
  await loginAdmin(page);
  const headers = {Authorization: "Bearer " + await sessionBearer(page)};
  const model = "native-count-" + Date.now();
  let deployment = "", token = "";
  try {
    const created = await page.request.post(GATEWAY + "/model/new", {headers, data: {
      model_name:model, litellm_params:{model:"count-upstream",custom_llm_provider:"anthropic",api_base:UPSTREAM,api_key:"sk-fake",input_cost_per_token:0.001,output_cost_per_token:0.002},
      model_info:{transport:"bypass_anthropic_messages",pricing_source:"manual",endpoint_types:["messages","bypass:anthropic-messages"]}
    }});
    expect(created.status(),await created.text()).toBe(200);
    deployment=(await created.json()).model_info.id;
    const minted=await page.request.post(GATEWAY+"/key/generate",{headers,data:{key_alias:model,models:[model]}});
    expect(minted.status(),await minted.text()).toBe(200);
    const key=await minted.json();token=key.token_id;
    const discovered=await page.request.get(GATEWAY+"/v1/models/"+model,{headers:{"x-api-key":key.key,"anthropic-version":"2023-06-01"}});
    expect(discovered.status(),await discovered.text()).toBe(200);
    expect(await discovered.json()).toMatchObject({id:model,type:"model",display_name:model});
    expect((await page.request.get(GATEWAY+"/v1/models/missing",{headers:{"x-api-key":key.key}})).status()).toBe(404);
    const ids:string[]=[];
    for (const path of ["/v1/messages/count_tokens","/bypass/anthropic/v1/messages/count_tokens"]) {
      const counted=await page.request.post(GATEWAY+path,{headers:{"x-api-key":key.key,"anthropic-version":"2023-06-01"},data:{model,messages:[{role:"user",content:"Count this input."}]}});
      expect(counted.status(),await counted.text()).toBe(200);
      expect(await counted.json()).toEqual({input_tokens:123});
      const id=counted.headers()["x-litellm-call-id"];expect(id).toBeTruthy();ids.push(id);
      await expect.poll(async()=>(await page.request.get(GATEWAY+"/spend/logs/ui/"+id,{headers})).status()).toBe(200);
      const detail=await (await page.request.get(GATEWAY+"/spend/logs/ui/"+id,{headers})).json();
      expect(detail.spend).toBe(0);
      expect(detail.prompt_tokens).toBe(0);
      expect(detail.completion_tokens).toBe(0);
    }
    for (const id of ids) {
      // 日志默认按会话合并；通过真实详情深链接定位每次调用，不能把会话代表ID当作全部请求ID。
      await stableGoto(page,"/logs/?log_id="+encodeURIComponent(id));
      await expect(page.getByRole("dialog")).toContainText(id);
      await page.keyboard.press("Escape");
    }
  } finally {
    if(token) expect((await page.request.post(GATEWAY+"/key/delete",{headers,data:{keys:[token]}})).status()).toBe(200);
    if(deployment) expect((await page.request.post(GATEWAY+"/model/delete",{headers,data:{id:deployment}})).status()).toBe(200);
  }
});

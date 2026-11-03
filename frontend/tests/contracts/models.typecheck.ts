import type { components, paths } from "../../src/lib/http/contracts/models";
type S = components["schemas"];
// 编译正反例检查真实响应形状、探测业务失败和扩展参数；不执行任何请求或修改记录。
const created: S["Deployment"] = {model_name:"m",litellm_params:{model:"upstream",extension:{enabled:true}},model_info:{id:"m",disabled:false,db_model:true,transport:"openai_chat",endpoint_types:["chat"]}};
const patch: S["UpdateDeployment"] = {id:"m",litellm_params:{input_cost_per_token:0,extension:null}};
const failed: S["ConnectionResult"] = {status:"error",healthy:false,error:"upstream_http_error",message:"Failed",result:{error:"Failed"},model:"m",mode:"chat"};
const query: paths["/v2/model/info"]["get"]["parameters"]["query"] = {modelId:"m",page:"1"};
// @ts-expect-error 创建回复不是data信封。
const wrong: S["Deployment"] = {data:created};
// @ts-expect-error 健康为false不能作为成功。
const falseSuccess: S["ConnectionResult"] = {status:"success",healthy:false,error:null,model:"m",mode:"chat"};
// @ts-expect-error 删除成功仍须包含真实部署。
const deleted: S["DeletedDeployment"] = {deleted:true};
void [created,patch,failed,query,wrong,falseSuccess,deleted];

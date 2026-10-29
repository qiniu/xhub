const test = require('node:test');
const assert = require('node:assert/strict');
const { isExpectedOptionalSettingsFailure: matches, isOptionalSettingsRead } = require('./optional-settings-response.cjs');
const base = {url:'http://localhost/gw/get/ui_settings',method:'GET',status:501,cacheControl:'no-store',body:{error:{type:'unsupported_settings_operation',code:'501',message:'Unavailable'}}};
/** 仅精确读取和完整失败契约属于可选设置降级；纯函数，无资源需清理。 */
test('optional settings require an explicit unavailable contract', () => {
 for(const prefix of ['', '/gw']) for(const path of ['/get/ui_settings','/sso/get/ui_settings','/get/user_banner']) assert.equal(matches({...base,url:'http://localhost'+prefix+path}),true);
 for(const change of [{status:500},{status:503},{status:200},{method:'PATCH'},{cacheControl:'public'},{body:null},{body:{error:{type:'unavailable'}}},{url:'http://localhost/get/ui_theme_settings'},{url:'http://localhost/get/ui_settings/extra'},{url:'http://localhost/config/list'},{url:'http://localhost/model/available'},{url:'not-a-url'},{body:{error:{...base.body.error,message:''}}},{body:{error:{...base.body.error,code:'500'}}}]) assert.equal(matches({...base,...change}),false,JSON.stringify(change));
});
/** 所有页面响应巡检必须被等待，不能让异步校验逃逸；只读本地源码，无环境副作用。 */
test('every page guard assertion is awaited', () => {
 const fs=require('node:fs'), path=require('node:path');
 for(const file of fs.readdirSync(__dirname, {recursive:true}).filter(x=>x.endsWith('.ts'))) {
  const source=fs.readFileSync(path.join(__dirname,file),'utf8');
  for(const line of source.split('\n').filter(x=>x.includes('.assertOk('))) assert.match(line,/await \w+\.assertOk\(/,file+': '+line);
 }
});

/** 其他错误应立即失败，不等待无关响应正文；纯函数无清理。 */
test('only optional reads need asynchronous body checks', () => {
 assert.equal(isOptionalSettingsRead(base),true);
 for(const change of [{status:500},{method:'PATCH'},{url:'http://localhost/config/list'}]) assert.equal(isOptionalSettingsRead({...base,...change}),false);
});

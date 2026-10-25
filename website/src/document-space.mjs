/** 判断文档所属入口；source 为站点相对 Markdown 路径，返回 product/developer/reference。
 * 导航与搜索调用；生成字段表归独立参考，旧文档地址仍由构建器保留。 */
export function documentSpace(source) {
  if (/\/(supplier|user|key|model|usage)-api\.generated\.md$/.test(source)) return "reference";
  return /^(development|modules)\//.test(source) ? "developer" : "product";
}

/** 返回旧生成文档对应领域；非法或非生成文档返回 null，无副作用。 */
export function referenceDomain(source) {
  const name = source.match(/\/(supplier|user|key|model|usage)-api\.generated\.md$/)?.[1];
  return ({ supplier: "suppliers", user: "users", key: "keys", model: "models", usage: "usage" })[name] || null;
}

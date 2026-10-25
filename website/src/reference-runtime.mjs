/** 校验手工输入的调用地址；返回无尾斜线地址，无存储或网络副作用。
 * 页面协议用于拒绝混合内容；不接受凭据、查询或片段，非法输入抛出 TypeError。 */
export function normalizeGateway(input, pageProtocol = "http:") {
  const url = new URL(input.trim());
  if (!["http:", "https:"].includes(url.protocol) || url.username || url.password || url.search || url.hash || (pageProtocol === "https:" && url.protocol === "http:")) throw new TypeError("Invalid gateway");
  return url.href.replace(/\/$/, "");
}

/** 选择契约元数据的成对中英文说明；text 为描述，locale 为 zh/en，返回展示文本。
 * 支持“中文 / English；下一中文 / Next English”；不匹配的格式原样保留，避免猜测或截断协议内容。 */
export function localizedMetadata(text, locale) {
  const separator = text.indexOf(" / ");
  if (separator < 0) return text;
  const chinese = text.slice(0, separator), rest = text.slice(separator + 3);
  if (!/[\u4e00-\u9fff]/.test(chinese)) return text;
  const boundary = rest.search(/；(?=[^；]*[\u4e00-\u9fff])/);
  const english = boundary < 0 ? rest : rest.slice(0, boundary);
  if (/[\u4e00-\u9fff]/.test(english)) return text;
  const selected = locale === "zh" ? chinese : english;
  return boundary < 0 ? selected : selected + (locale === "zh" ? "；" : "; ") + localizedMetadata(rest.slice(boundary + 1), locale);
}

/** 对已知描述元数据选择语言，返回独立副本；仅处理 description/summary/title，
 * 保留字段名、示例、默认值和枚举，避免翻译用户实际输入；不改变原始契约。 */
export function localizedSpec(spec, locale) {
  if (!["zh", "en"].includes(locale)) throw new TypeError("Invalid locale");
  /** 递归复制契约结构；value 为节点，例子和字面值子树不翻译，无副作用。 */
  function visit(value) {
    if (Array.isArray(value)) return value.map(visit);
    if (!value || typeof value !== "object") return value;
    return Object.fromEntries(Object.entries(value).map(([key, child]) => {
      if (["example", "examples", "default", "enum", "const"].includes(key)) return [key, structuredClone(child)];
      if (["description", "summary", "title"].includes(key) && typeof child === "string") {
        return [key, localizedMetadata(child, locale)];
      }
      return [key, visit(child)];
    }));
  }
  return visit(spec);
}

/** 生成 Swagger 配置；spec 为展示副本，target 为显式校验后的地址或空值。
 * 禁用外部验证服务及授权持久化；未设置目标时禁止执行，地址只在内存覆盖，不回写契约。 */
export function swaggerOptions(spec, target) {
  return {
    dom_id: "#swagger-ui", spec: { ...spec, servers: target ? [{ url: target }] : [] },
    validatorUrl: null, persistAuthorization: false, queryConfigEnabled: false,
    supportedSubmitMethods: target ? ["get", "post", "put", "patch", "delete", "head", "options"] : [],
    deepLinking: true, docExpansion: "list", defaultModelsExpandDepth: -1,
    displayRequestDuration: true, withCredentials: false,
  };
}

/** 探测页面只区分已有的提示词和搜索工具，不为其他目录引入通用工作流。 */
export type ProbeKind = "prompts" | "search_tools";

/** 表单保留用户输入原文；id、contentMode 和 unsupported 仅反映已保存记录，密钥不进入结果区。 */
export interface ProbeForm {
  id: string;
  name: string;
  model: string;
  messages: string;
  contentMode: boolean;
  variables: string;
  provider: string;
  apiBase: string;
  apiKey: string;
  query: string;
  unsupported: boolean;
}

/** 生成独立的新建表单；无参数，返回英文示例和空凭据，不读写存储。 */
export function emptyProbeForm(): ProbeForm {
  return {
    id: "", name: "", model: "", contentMode: false,
    messages: JSON.stringify([{ role: "user", content: "Say hello to {{name}}." }], null, 2),
    variables: JSON.stringify({ name: "World" }, null, 2),
    provider: "tavily", apiBase: "", apiKey: "", query: "OpenAI API documentation", unsupported: false,
  };
}

/** 将未知响应判定为对象；参数可能为任意 JSON，返回安全对象或 null，不接受数组和空值。 */
export function probeObject(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : null;
}

/** 从详情创建编辑表单；kind 定位 ID 字段，record 为后端对象。格式错误抛翻译键；不回填任何明文或掩码密钥。 */
export function formFromRecord(kind: ProbeKind, record: unknown): ProbeForm {
  const obj = probeObject(record);
  const id = obj?.[kind === "prompts" ? "prompt_id" : "search_tool_id"] ?? obj?.id;
  if (!obj || typeof id !== "string" || !id) throw new Error("probes.invalidRecord");
  const params = probeObject(obj.litellm_params) ?? {};
  const form = emptyProbeForm();
  form.id = id;
  form.name = String(obj.name ?? obj.search_tool_name ?? id);
  form.model = String(obj.model ?? "");
  form.provider = String(params.search_provider ?? "tavily");
  form.apiBase = String(params.api_base ?? "");
  form.contentMode = typeof obj.content === "string" && obj.messages === undefined;
  form.messages = form.contentMode ? String(obj.content) : JSON.stringify(obj.messages ?? [], null, 2);
  form.unsupported = obj.dotprompt_content !== undefined;
  return form;
}

/** 解析 JSON 输入并转换成稳定的翻译键错误；text 保留用户内容，errorKey 由表单确定，无副作用。 */
function parseProbeJSON(text: string, errorKey: string): unknown {
  try { return JSON.parse(text); } catch { throw new Error(errorKey); }
}

/** 构造保存正文；kind/form 来自当前编辑器，saved 为原始详情以保留供应商扩展字段。校验失败抛翻译键。 */
export function probeSaveBody(kind: ProbeKind, form: ProbeForm, saved?: unknown): Record<string, unknown> {
  if (!form.name.trim()) throw new Error("probes.required");
  if (kind === "prompts") {
    if (form.unsupported) throw new Error("probes.unsupportedPrompt");
    if (!form.model.trim()) throw new Error("probes.required");
    const body: Record<string, unknown> = { name: form.name, model: form.model.trim() };
    if (!form.id) body.prompt_id = form.name;
    if (form.contentMode) {
      if (!form.messages.trim()) throw new Error("probes.required");
      body.content = form.messages;
    } else {
      const messages = parseProbeJSON(form.messages, "probes.invalidMessages");
      if (!Array.isArray(messages) || messages.length === 0 || messages.some((m) => !probeObject(m))) {
        throw new Error("probes.invalidMessages");
      }
      body.messages = messages;
    }
    return body;
  }
  if (!["tavily", "perplexity", "serper"].includes(form.provider)) throw new Error("probes.unsupportedProvider");
  if (!form.id && !form.apiKey.trim()) throw new Error("probes.required");
  const params = { ...(probeObject(probeObject(saved)?.litellm_params) ?? {}), search_provider: form.provider, api_base: form.apiBase.trim() };
  // 留空表示保留服务器已存密钥。掩码只是展示数据，不能当作新凭据回写。
  delete (params as Record<string, unknown>).api_key;
  if (form.apiKey.trim()) (params as Record<string, unknown>).api_key = form.apiKey.trim();
  return { name: form.name, search_tool_name: form.name, litellm_params: params };
}

/** 构造测试请求；配置必须已保存，测试参数不写入配置。非法变量 JSON 和无保存 ID 均在请求前拒绝。 */
export function probeTestBody(kind: ProbeKind, form: ProbeForm): Record<string, unknown> {
  if (!form.id) throw new Error("probes.dirty");
  if (kind === "search_tools") return { search_tool_id: form.id, query: form.query };
  if (form.unsupported) throw new Error("probes.unsupportedPrompt");
  const variables = parseProbeJSON(form.variables, "probes.invalidVariables");
  if (!probeObject(variables)) throw new Error("probes.invalidVariables");
  return { prompt_id: form.id, prompt_variables: variables };
}

/** 验证测试结果的最小真实结构；kind 指定契约，value 为服务器正文，成功返回对象，旧占位或无效结构抛错误。 */
export function validatedProbeResult(kind: ProbeKind, value: unknown): Record<string, unknown> {
  const obj = probeObject(value);
  if (!obj) throw new Error("probes.invalidResponse");
  if (kind === "search_tools") {
    const results = obj.results;
    if (obj.status !== "success" || !Array.isArray(results)) throw new Error("probes.invalidResponse");
    if (obj.results_count !== results.length) throw new Error("probes.invalidResponse");
    if (results.some((row) => {
      const item = probeObject(row);
      if (!item) return true;
      return typeof item.title !== "string" || typeof item.url !== "string" || typeof item.snippet !== "string";
    })) throw new Error("probes.invalidResponse");
    return obj;
  }
  const choices = obj.choices;
  const first = Array.isArray(choices) ? probeObject(choices[0]) : null;
  const message = probeObject(first?.message);
  if (!message || (typeof message.content !== "string" && !Array.isArray(message.tool_calls))) {
    throw new Error("probes.invalidResponse");
  }
  return obj;
}

/** 生成配置指纹以阻止测试未保存草稿；测试变量和查询独立于配置，不参与比较，密钥仅留在内存。 */
export function probeConfiguration(form: ProbeForm): string {
  const { variables: _variables, query: _query, ...configuration } = form;
  return JSON.stringify(configuration);
}

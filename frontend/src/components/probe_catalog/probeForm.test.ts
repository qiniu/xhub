import { describe, expect, it } from "vitest";
import { collectKeys, translate } from "@/i18n/translate";
import { probesEn } from "@/i18n/messages/probes.en";
import { probesZhCN } from "@/i18n/messages/probes.zh-CN";
import { emptyProbeForm, formFromRecord, probeConfiguration, probeObject, probeSaveBody, probeTestBody, validatedProbeResult } from "./probeForm";

describe("probe form contracts", () => {
  /** 验证新表单使用独立英文示例且翻译目录对齐；无外部依赖，无数据清理。 */
  it("keeps English examples independent and both locales complete", () => {
    const first = emptyProbeForm();
    first.name = "different";
    expect(emptyProbeForm().name).toBe("");
    expect(JSON.parse(first.messages)[0].content).toBe("Say hello to {{name}}.");
    expect(first.query).toBe("OpenAI API documentation");
    expect(collectKeys(probesEn).sort()).toEqual(collectKeys(probesZhCN).sort());
    expect(translate("zh-CN", "probes.resultsCount", { count: 3 })).toBe("搜索返回 3 条结果。");
    expect(translate("en", "probes.resultsCount", { count: 3 })).toBe("Search returned 3 results.");
  });

  /** 验证详情必须是有 ID 对象；正常对象被接受，数组/null/缺 ID 不成为可测试记录。 */
  it("validates record shapes and keeps stored content", () => {
    expect(probeObject({ name: "ok" })).toEqual({ name: "ok" });
    for (const value of [null, [], "x", undefined, {}]) {
      expect(() => formFromRecord("prompts", value)).toThrow("probes.invalidRecord");
    }
    const contentRecord = { prompt_id: "id", name: "原名", model: "m", content: "用户正文 {{name}}" };
    const form = formFromRecord("prompts", contentRecord);
    expect(form.contentMode).toBe(true);
    expect(probeSaveBody("prompts", form)).toEqual({ name: "原名", model: "m", content: "用户正文 {{name}}" });
    form.messages = " ";
    expect(() => probeSaveBody("prompts", form)).toThrow("probes.required");
  });

  /** 验证新增提示词提交完整模型和消息；缺字段、无效 JSON、非数组和空消息必须在发请求前失败。 */
  it("requires executable prompt configuration and preserves user text", () => {
    const form = emptyProbeForm();
    expect(() => probeSaveBody("prompts", form)).toThrow("probes.required");
    form.name = "中文名称";
    expect(() => probeSaveBody("prompts", form)).toThrow("probes.required");
    form.model = "model-id";
    form.messages = JSON.stringify([{ role: "user", content: "原文不翻译" }]);
    const expected = { prompt_id: "中文名称", name: "中文名称", model: "model-id", messages: [{ role: "user", content: "原文不翻译" }] };
    expect(probeSaveBody("prompts", form)).toEqual(expected);
    for (const invalid of ["{", "null", "{}", "[]", "[null]", "[1]"]) {
      form.messages = invalid;
      expect(() => probeSaveBody("prompts", form)).toThrow("probes.invalidMessages");
    }
  });

  /** 验证未实现的 Dotprompt 不会被编辑器转换成其他格式；保存和测试均失败，无持久化副作用。 */
  it("rejects unsupported stored Dotprompt on save and test", () => {
    const form = formFromRecord("prompts", { prompt_id: "p", model: "m", dotprompt_content: "---" });
    expect(form.unsupported).toBe(true);
    expect(() => probeSaveBody("prompts", form)).toThrow("probes.unsupportedPrompt");
    expect(() => probeTestBody("prompts", form)).toThrow("probes.unsupportedPrompt");
  });

  /** 验证搜索密钥只写不回填；编辑保留扩展参数、留空省略 key，新值替换，不把掩码当真实密钥。 */
  it("preserves search settings without displaying or resending saved keys", () => {
    const saved = { search_tool_id: "s", name: "搜索", litellm_params: { search_provider: "serper", api_key: "********", api_base: "https://example.test/search", locale: "en" } };
    const form = formFromRecord("search_tools", saved);
    expect(form.apiKey).toBe("");
    expect(form.provider).toBe("serper");
    expect(probeSaveBody("search_tools", form, saved)).toEqual({ name: "搜索", search_tool_name: "搜索", litellm_params: { search_provider: "serper", api_base: "https://example.test/search", locale: "en" } });
    form.apiKey = "replacement";
    expect((probeSaveBody("search_tools", form, saved).litellm_params as Record<string, unknown>).api_key).toBe("replacement");
    form.provider = "unknown";
    expect(() => probeSaveBody("search_tools", form, saved)).toThrow("probes.unsupportedProvider");
    const fresh = { ...emptyProbeForm(), name: "new" };
    expect(() => probeSaveBody("search_tools", fresh)).toThrow("probes.required");
  });

  /** 验证测试引用已保存 ID，变量必须对象；仅测试参数改动不使配置变脏，实际配置变动必须另存。 */
  it("separates saved configuration from one-off test input", () => {
    const form = emptyProbeForm();
    expect(() => probeTestBody("prompts", form)).toThrow("probes.dirty");
    form.id = "p";
    const before = probeConfiguration(form);
    form.variables = '{"name":"用户"}'; form.query = "用户查询";
    expect(probeConfiguration(form)).toBe(before);
    expect(probeTestBody("prompts", form)).toEqual({ prompt_id: "p", prompt_variables: { name: "用户" } });
    expect(probeTestBody("search_tools", form)).toEqual({ search_tool_id: "p", query: "用户查询" });
    form.model = "different";
    expect(probeConfiguration(form)).not.toBe(before);
    for (const invalid of ["{", "null", "[]", '"x"']) {
      form.variables = invalid;
      expect(() => probeTestBody("prompts", form)).toThrow("probes.invalidVariables");
    }
  });

  /** 验证提示词结果必须有实际 completion 结构；旧 succeeded 占位、缺消息和空 choices 不得成功。 */
  it("rejects placeholder prompt results while accepting completion and tool output", () => {
    for (const result of [undefined, { status: "succeeded", output: "" }, { choices: [] }, { choices: [{ message: {} }] }]) {
      expect(() => validatedProbeResult("prompts", result)).toThrow("probes.invalidResponse");
    }
    const response = { choices: [{ message: { content: "真实结果" } }], usage: { prompt_tokens: 1, completion_tokens: 2 } };
    expect(validatedProbeResult("prompts", response)).toBe(response);
    expect(validatedProbeResult("prompts", { choices: [{ message: { tool_calls: [{ id: "call" }] } }] })).toBeDefined();
  });

  /** 验证搜索空结果合法但结果数组/条目/数量必须一致；静态 status success 不构成成功，无外部请求。 */
  it("accepts validated empty searches and rejects malformed search results", () => {
    const response = { status: "success", results_count: 0, results: [] };
    expect(validatedProbeResult("search_tools", response)).toBe(response);
    expect(validatedProbeResult("search_tools", { status: "success", results_count: 1, results: [{ title: "Documentation", url: "https://example.test", snippet: "Example" }] })).toBeDefined();
    for (const result of [{ status: "success" }, { status: "error", results_count: 0, results: [] }, { status: "success", results_count: 2, results: [] }, { status: "success", results_count: 1, results: [{}] }]) {
      expect(() => validatedProbeResult("search_tools", result)).toThrow("probes.invalidResponse");
    }
  });
});

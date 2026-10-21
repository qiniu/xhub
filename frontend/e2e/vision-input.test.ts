import { expect, test, type Page } from "@playwright/test";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto } from "./helpers";
import { translate } from "../src/i18n/translate";

const PNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGNg+M8AAAICAQB7CYF4AAAAAElFTkSuQmCC";

/** 浏览器同源发起真实 API 请求；参数为页面、有限额Key、路径和正文，返回状态/正文/账单及预算头；读取完正文再返回，不模拟网络。 */
async function browserPost(page: Page, bearer: string, path: string, body: Record<string, unknown>) {
  return page.evaluate(
    async ({ bearer, path, body }) => {
      const reply = await fetch(path, {
        method: "POST",
        headers: { "Content-Type": "application/json", Authorization: "Bearer " + bearer },
        body: JSON.stringify(body),
      });
      return {
        status: reply.status,
        raw: await reply.text(),
        callID: reply.headers.get("x-litellm-call-id")!,
        headers: Object.fromEntries(reply.headers.entries()),
      };
    },
    { bearer, path, body },
  );
}

/** 验证图片请求同样使用软预算，不产生整窗预占；参数为真实响应头，失败定位策略错误，无副作用。 */
function expectSoftVisionBudget(headers: Record<string, string>) {
 expect(headers["x-xhub-budget-guarantee"]).toBe("soft");
 expect(headers["x-xhub-budget-reserved"]).toBeUndefined();
 expect(headers["x-xhub-budget-reservation-id"]).toBeUndefined();
}

for (const locale of ["zh-CN", "en"] as const)
  for (const stream of [false, true]) {
    /** 前置真实浏览器/Go 后台/PostgreSQL 和校验图片字节/cap的本地上游；中英文创建选择有限额Key、上传预览、三协议JSON/SSE、账单和零占用。
     * 原生Messages工具结果图片保留，跨协议与独立图片收费明确拒绝；提示不含答案，finally 删除Key/模型/连接，运行器回收账单和schema。 */
    test("Vision upload and protocol bridge " + locale + " " + stream, async ({ page }) => {
      test.setTimeout(120000);
      await loginAdmin(page);
      const t = (key: string) => translate(locale, key);
      if (locale === "en")
        await page.getByRole("button", { name: translate("zh-CN", "language.en"), exact: true }).click();
      const bearer = await sessionBearer(page),
        headers = { Authorization: "Bearer " + bearer };
      const suffix = locale + "-" + stream,
        credential = "vision-credential-" + suffix,
        names = ["vision-chat-", "vision-responses-", "vision-messages-"].map((name) => name + suffix);
      const ids: string[] = [],
        calls: string[] = [];
      let keyID = "";
      expect(
        (
          await page.request.post(GATEWAY + "/credentials", {
            headers,
            data: {
              credential_name: credential,
              credential_info: { custom_llm_provider: "openai" },
              credential_values: { api_base: UPSTREAM + "/v1", api_key: "sk-fake" },
            },
          })
        ).status(),
      ).toBe(200);
      try {
        for (let index = 0; index < 4; index++) {
          const saved = await page.request.post(GATEWAY + "/model/new", {
            headers,
            data: {
              model_name: index === 3 ? "vision-image-rate-" + suffix : names[index],
              litellm_params: {
                model: "e2e-vision-budget-" + index,
                custom_llm_provider: index === 2 ? "anthropic" : "openai",
                ...(index === 2 ? { api_base: UPSTREAM, api_key: "sk-fake" } : { litellm_credential_name: credential }),
                input_cost_per_token: 0.000001,
                output_cost_per_token: 0.000002,
                ...(index === 3 ? { input_cost_per_image: 0.01 } : {}),
              },
              model_info: {
                transport: [
                  "bypass_openai_chat",
                  "bypass_openai_responses",
                  "bypass_anthropic_messages",
                  "bypass_openai_responses",
                ][index],
                endpoint_types: ["chat", "responses", "messages"],
                pricing_source: "manual",
                max_input_tokens: 1024,
                max_output_tokens: 8192,
                supported_parameters: ["max_tokens"],
              },
            },
          });
          expect(saved.status(), await saved.text()).toBe(200);
          ids.push((await saved.json()).model_info.id);
        }
        await page.getByTestId("create-key-button").click();
        await page.getByLabel(t("Key Name")).fill("vision-budget-" + suffix);
        await page.getByLabel(t("Max Budget (USD)"), { exact: true }).fill("1");
        const minted = page.waitForResponse(
          (reply) => reply.url().includes("/key/generate") && reply.request().method() === "POST",
        );
        await page.getByRole("button", { name: t("pages.apiKeys.createSubmit"), exact: true }).click();
        const mintedReply = await minted;
        expect(mintedReply.status()).toBe(200);
        const key = await mintedReply.json();
        keyID = key.token_id;
        await page.keyboard.press("Escape");
        const initial = await (await page.request.get(GATEWAY + "/key/info?key=" + keyID, { headers })).json();
        expect(initial.info.max_budget).toBe(1);
        await page.evaluate((stream) => sessionStorage.setItem("streamingEnabled", JSON.stringify(stream)), stream);
        await stableGoto(page, "/playground");
        await page.getByText(t("myModels.connectionSettings"), { exact: true }).click();
        await page.getByRole("combobox", { name: t("Virtual Key Source"), exact: true }).click();
        await page.getByRole("option", { name: t("Virtual Key"), exact: true }).click();
        await page.getByPlaceholder(t("Enter custom Virtual Key"), { exact: true }).fill(key.key);
        for (const name of names)
          for (const path of ["/v1/responses", "/v1/chat/completions"]) {
            const clear = page.getByRole("button", { name: t("Clear Chat"), exact: true });
            if (await clear.isVisible()) await clear.click();
            await page.getByPlaceholder(t("Select a Model"), { exact: true }).click();
            await page.getByRole("option", { name, exact: true }).click();
            await page.getByPlaceholder(t("Select an endpoint"), { exact: true }).click();
            await page
              .getByRole("option")
              .filter({ hasText: new RegExp(path.replaceAll("/", "\\/") + "$") })
              .click();
            if (path.endsWith("responses")) {
              const session = page.getByRole("switch", { name: t("Use API session management") });
              if (await session.isChecked()) await session.click();
            }
            const chooser = page.waitForEvent("filechooser");
            await page.getByRole("button", { name: t("Attach image or PDF"), exact: true }).click();
            await (
              await chooser
            ).setFiles({ name: "vision.png", mimeType: "image/png", buffer: Buffer.from(PNG, "base64") });
            await expect(page.getByAltText(t("Upload preview"))).toBeVisible();
            await page
              .getByPlaceholder(t("Type your message... (Shift+Enter for new line)"), { exact: true })
              .fill("Describe the pixel color in this image.");
            const pending = page.waitForResponse(
              (r) => new URL(r.url()).pathname.endsWith(path.replace(/^\/v1/, "")) && r.request().method() === "POST",
            );
            await page.getByRole("button", { name: t("Send message"), exact: true }).click();
            const reply = await pending;
            // SSE已由页面读取；Chrome不保证保留可二次读取正文，实际输出和完整结算分别在页面及账单校验。
            expect(reply.status(), name + " " + path).toBe(200);
            expect(reply.headers()["content-type"]).toContain(stream ? "text/event-stream" : "application/json");
            expect(
              reply.request().headers().authorization === "Bearer " + key.key,
              "Playground必须使用刚创建的有限额Key",
            ).toBe(true);
            expectSoftVisionBudget(reply.headers());
            await expect(page.getByText("green-pixel", { exact: true }).last()).toBeVisible();
            calls.push(reply.headers()["x-litellm-call-id"]);
          }
        // Messages 通过同源浏览器代理到真实后台，必须转换成上游能消费的图片，不能只允许一个白名单字段。
        for (const name of names) {
          const result = await browserPost(page, key.key, "/gw/v1/messages", {
            model: name,
            max_tokens: 256,
            stream,
            messages: [
              {
                role: "user",
                content: [
                  { type: "text", text: "Describe this image." },
                  { type: "image", source: { type: "base64", media_type: "image/png", data: PNG } },
                ],
              },
            ],
          });
          expect(result.status, result.raw).toBe(200);
          expect(result.headers["content-type"]).toContain(stream ? "text/event-stream" : "application/json");
          expect(result.raw).toContain("green-pixel");
          expectSoftVisionBudget(result.headers);
          calls.push(result.callID);
        }
        const toolHistory = [
          { role: "user", content: "Read the image and describe its pixel color." },
          { role: "assistant", content: [{ type: "tool_use", id: "tool_image", name: "read_image", input: {} }] },
          {
            role: "user",
            content: [
              {
                type: "tool_result",
                tool_use_id: "tool_image",
                content: [
                  { type: "text", text: "Image bytes:" },
                  { type: "image", source: { type: "base64", media_type: "image/png", data: PNG } },
                ],
              },
            ],
          },
        ];
        const nativeTool = await browserPost(page, key.key, "/gw/v1/messages", {
          model: names[2],
          max_tokens: 256,
          stream,
          messages: toolHistory,
        });
        expect(nativeTool.status, nativeTool.raw).toBe(200);
        expect(nativeTool.headers["content-type"]).toContain(stream ? "text/event-stream" : "application/json");
        expect(nativeTool.raw).toContain("green-pixel");
        expectSoftVisionBudget(nativeTool.headers);
        calls.push(nativeTool.callID);
        // 工具结果图片只认证原生Messages；跨协议失败必须可见，不能悄悄把图片丢掉后返回文字。
        for (const model of names.slice(0, 2)) {
          const rejected = await browserPost(page, key.key, "/gw/v1/messages", {
            model,
            max_tokens: 256,
            stream,
            messages: toolHistory,
          });
          expect(rejected.status, rejected.raw).toBe(400);
          expect(rejected.raw).not.toContain("green-pixel");
        }
        const invalid = await browserPost(page, key.key, "/gw/v1/responses", {
          model: names[0],
          input: [{ role: "user", content: [{ type: "input_image", image_url: "file:///private/image.png" }] }],
        });
        expect(invalid.status).toBe(400);
        const separateImageRate = await browserPost(page, key.key, "/gw/v1/responses", {
          model: "vision-image-rate-" + suffix,
          stream,
          input: [{ role: "user", content: [{ type: "input_image", image_url: "data:image/png;base64," + PNG }] }],
        });
        expect(separateImageRate.status, separateImageRate.raw).toBe(200);
        // 独立图片价格不再阻断请求；当前仅按实测 token 计费，也必须纳入账单核对。
        calls.push(separateImageRate.callID);

        for (const id of calls)
          await expect
            .poll(async () => {
              const reply = await page.request.get(GATEWAY + "/spend/logs/ui/" + id, { headers });
              if (!reply.ok()) return null;
              const bill = await reply.json();
              return {
                status: bill.status,
                spend: Number(bill.spend),
                input: bill.prompt_tokens,
                output: bill.completion_tokens,
              };
            })
            .toEqual({ status: "success", spend: 0.000012, input: 8, output: 2 });
        await expect
          .poll(async () => {
            const info = (await (await page.request.get(GATEWAY + "/key/info?key=" + keyID, { headers })).json()).info;
            return {
              pending: info.budget_pending,
              holds: info.budget_reservations?.length ?? 0,
              spend: Number(info.spend).toFixed(6),
            };
          })
          .toEqual({ pending: 0, holds: 0, spend: (calls.length * 0.000012).toFixed(6) });
        await stableGoto(page, "/api-keys?key=" + keyID);
        await expect(
          page.getByText(translate(locale, "hardBudget.pending", { amount: 0 }), { exact: true }),
        ).toBeVisible();
        await stableGoto(page, "/logs");
        await expect(page.getByText(names[0], { exact: true }).first()).toBeVisible();
      } finally {
        if (keyID)
          expect(
            (await page.request.post(GATEWAY + "/key/delete", { headers, data: { keys: [keyID] } })).status(),
          ).toBe(200);
        await page.evaluate(() => {
          sessionStorage.removeItem("apiKey");
          sessionStorage.removeItem("apiKeySource");
        });
        for (const id of ids)
          expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id } })).status()).toBe(200);
        expect((await page.request.delete(GATEWAY + "/credentials/" + credential, { headers })).status()).toBe(200);
      }
    });
  }

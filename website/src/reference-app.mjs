import { referenceCopy, referenceDomains } from "./reference-copy.mjs";
import { normalizeGateway, localizedSpec, swaggerOptions } from "./reference-runtime.mjs";
const lang = document.body.dataset.locale;
const t = referenceCopy[lang];
const domain = document.querySelector("#domain");
const status = document.querySelector("#status");
const error = document.querySelector("#error");
const input = document.querySelector("#gateway");
const requested = new URL(location.href).searchParams.get("domain");
if (referenceDomains.includes(requested)) domain.value = requested;
let target = "", generation = 0;

/** 装载当前领域真实静态契约；不伪造成功数据，迟到结果不能替换新选择。
 * 切换领域/目标会重新创建 Swagger 并清授权；失败展示本地化错误，不保留旧操作。 */
async function render() {
  const current = ++generation;
  const selected = domain.value;
  error.hidden = true;
  status.textContent = t.loading;
  document.querySelector("#swagger-ui").replaceChildren();
  document.querySelector("#download").href = `../specs/${selected}.json`;
  document.querySelector("#language").href = `../${lang === "zh" ? "en" : "zh"}/index.html?domain=${selected}`;
  history.replaceState(null, "", `?domain=${selected}${location.hash}`);
  try {
    const response = await fetch(`../specs/${selected}.json`, { credentials: "omit" });
    if (!response.ok) throw new Error("Contract request failed");
    const spec = await response.json();
    if (current !== generation) return;
    if (!spec.paths || spec.openapi !== "3.0.3") throw new Error("Invalid contract");
    window.SwaggerUIBundle(swaggerOptions(localizedSpec(spec, lang), target));
    status.textContent = target ? t.connected.replace("{target}", target) : t.readOnly;
  } catch {
    if (current !== generation) return;
    status.textContent = "";
    error.textContent = t.loadError;
    error.hidden = false;
  }
}

// 不把上一领域的 operationId 深链接带入新领域；刷新当前领域仍可恢复其展开位置。
domain.addEventListener("change", () => {
  history.replaceState(null, "", `?domain=${domain.value}`);
  render();
});
document.querySelector("#target-form").addEventListener("submit", event => {
  event.preventDefault();
  try { target = normalizeGateway(input.value, location.protocol); render(); }
  catch { error.textContent = t.invalidTarget; error.hidden = false; }
});
document.querySelector("#disconnect").addEventListener("click", () => { target = ""; input.value = ""; render(); });
render();

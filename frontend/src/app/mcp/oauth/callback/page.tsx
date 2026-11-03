"use client";

import { Suspense, useEffect, useMemo } from "react";
import { useSearchParams } from "next/navigation";
import { getSecureItem, setSecureItem } from "@/utils/secureStorage";
import { useT } from "@/i18n";

// Written to sessionStorage so the admin hook (useMcpOAuthFlow), the user hook
// (useUserMcpOAuthFlow), and the tools re-auth hook (useToolsOAuthFlow) can each
// pick up the result.  Each hook reads its own namespace to avoid cross-flow collisions.
const ADMIN_RESULT_KEY = "litellm-mcp-oauth-result";
const USER_RESULT_KEY = "litellm-user-mcp-oauth-result";
const TOOLS_RESULT_KEY = "litellm-tools-mcp-oauth-result";
const RETURN_URL_STORAGE_KEY = "litellm-mcp-oauth-return-url";

const resolveDefaultRedirect = () => {
  if (typeof window === "undefined") {
    return "/ui";
  }

  const path = window.location.pathname || "";
  const uiIndex = path.indexOf("/ui");
  if (uiIndex >= 0) {
    const prefix = path.slice(0, uiIndex + 3);
    return prefix.endsWith("/") ? prefix : `${prefix}`;
  }

  return "/";
};

/**
 * 将 URL 中未验证的授权结果交给原页面处理；无参数，返回双语跳转提示。
 * 回调页不会验证授权或交换令牌，即使收到 code 或写入存储成功，也不能宣称连接成功。
 * 副作用为写入会话存储并跳回同源页面；存储失败时仍跳转，提示不承诺已保存。
 */
const McpOAuthCallbackContent = () => {
  const t = useT();
  const searchParams = useSearchParams();

  const payload = useMemo(() => {
    if (!searchParams) {
      return null;
    }
    return {
      type: "litellm-mcp-oauth",
      code: searchParams.get("code"),
      state: searchParams.get("state"),
      // Forward OAuth provider error params so the hook can surface the real
      // reason (e.g. "access_denied") instead of a generic "code missing" error.
      error: searchParams.get("error"),
      error_description: searchParams.get("error_description"),
    };
  }, [searchParams]);

  useEffect(() => {
    if (!payload || typeof window === "undefined") {
      return;
    }

    try {
      // Write to all namespace keys so whichever hook is active can consume
      // the result.  sessionStorage only — no localStorage.
      const serialized = JSON.stringify(payload);
      setSecureItem(ADMIN_RESULT_KEY, serialized);
      setSecureItem(USER_RESULT_KEY, serialized);
      setSecureItem(TOOLS_RESULT_KEY, serialized);
    } catch (err) {
      // Silently ignore storage errors
    }

    const returnUrl = getSecureItem(RETURN_URL_STORAGE_KEY);
    let destination = resolveDefaultRedirect();
    if (returnUrl) {
      try {
        const parsed = new URL(returnUrl, window.location.origin);
        if (parsed.origin === window.location.origin) {
          destination = parsed.href;
        }
      } catch {
        // invalid URL — fall through to default
      }
    }
    window.location.replace(destination);
  }, [payload]);

  return (
    <div className="min-h-screen flex items-center justify-center bg-muted p-6">
      <div className="max-w-lg w-full rounded-lg bg-card shadow-md p-8 text-center space-y-4">
        <h1 className="text-xl font-semibold text-foreground">{t("mcpOAuthCallback.title")}</h1>
        <p className="text-sm text-foreground">
          {t("mcpOAuthCallback.pending")}
        </p>
        <p className="text-xs text-muted-foreground">
          {t("mcpOAuthCallback.returnHelp")}
        </p>
      </div>
    </div>
  );
};

/** OAuth 回调页面入口；无参数，返回加载边界与结果转交提示，由 Next.js 挂载，授权副作用由子组件处理。 */
const McpOAuthCallbackPage = () => {
  const t = useT();
  return (
    <Suspense fallback={<div className="min-h-screen flex items-center justify-center">{t("mcpOAuthCallback.loading")}</div>}>
      <McpOAuthCallbackContent />
    </Suspense>
  );
};

export default McpOAuthCallbackPage;

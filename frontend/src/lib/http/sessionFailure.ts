/** 会话核验依赖；调用方提供当前 Cookie、API 地址和认证头，避免将自定义 Key 当作登录凭据。 */
export interface SessionFailureContext {
  getSessionToken: () => string | null;
  getSessionKey: (token: string) => string | null;
  getBaseUrl: () => string;
  getHeaderName: () => string;
  fetchImpl?: typeof fetch;
}

/** 创建可合并并发请求的会话失效核验器，返回异步检查函数，无主动退出副作用。
 * 参数 context 来自控制台网络模块；只有 /auth/me 明确 401 且 Cookie 未变化时返回 true。
 * 自定义 Key 报错不能证明会话失效；网络/5xx/403/无登录凭据均返回 false，调用方继续展示原请求错误。
 * 每个实例只保留正在进行的核验，结束即释放；直接 fetch 避免递归触发全局错误处理。
 */
export function createSessionFailureCheck(context: SessionFailureContext): () => Promise<boolean> {
  let pending: { token: string; base: string; result: Promise<boolean> } | null = null;
  return async () => {
    const token = context.getSessionToken();
    if (!token) return false;
    const key = context.getSessionKey(token);
    if (!key) return false;
    const base = context.getBaseUrl();
    if (pending?.token === token && pending.base === base) return pending.result;
    const result = (async () => {
      try {
        const response = await (context.fetchImpl ?? fetch)(`${base}/auth/me`, {
          headers: { [context.getHeaderName()]: `Bearer ${key}` },
          credentials: "omit",
          cache: "no-store",
        });
        // 核验期间重新登录或切换网关时，旧请求不得删除新的有效会话。
        return response.status === 401 && context.getSessionToken() === token && context.getBaseUrl() === base;
      } catch {
        return false;
      }
    })();
    pending = { token, base, result };
    try {
      return await result;
    } finally {
      if (pending?.result === result) pending = null;
    }
  };
}

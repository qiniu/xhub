export const cacheAdminEn = {
  title: "Response cache",
  scope: "Only complete, eligible non-streaming responses are cached in this gateway process. Responses API history and provider prompt caching are separate. Clearing does not affect other instances, Redis, sessions, budgets or usage records.",
  unsupported: "Runtime cache settings and connection testing are not supported. Previously saved settings do not take effect.",
  ready: "In-process memory cache is available",
  loading: "Checking response cache…",
  loadFailed: "Could not confirm cache status. Refresh to retry.",
  flush: "Clear this process cache",
  flushing: "Clearing…",
  flushed: "This process response cache has been cleared.",
  flushFailed: "Cache clearing failed. Success has not been confirmed; retry or check the service.",
  refresh: "Refresh status",
};

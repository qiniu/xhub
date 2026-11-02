import { requestLogStatus } from "../taskStatus";
import { useEffect, useMemo, useRef, useState } from "react";
import { Bot, Check, Copy, Sparkles, Wrench } from "lucide-react";
import { Sheet, SheetContent, SheetTitle } from "@/components/ui/sheet";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { LogEntry } from "../columns";
import { isRequestFailure } from "../logErrors";
import { AutoRouterIcon, useIsAutoRoutedModelGroup } from "@/components/shared/table_cells";
import { AGENT_CALL_TYPES, MCP_CALL_TYPES } from "../constants";
import { getEventDisplayName } from "../utils";
import { ClassifyTag } from "./ClassifyTag";
import { DrawerHeader } from "./DrawerHeader";
import { SidebarToggle } from "./SidebarToggle";
import { useKeyboardNavigation } from "./useKeyboardNavigation";
import { LogDetailContent, GuardrailJumpLink } from "./LogDetailContent";
import { sessionSpendLogsCall } from "../../networking";
import { useQuery } from "@tanstack/react-query";
import { formatLogCost } from "../formatLogCost";
import { normalizeGuardrailEntries, sortSessionLogs, SessionLogSortMode } from "./utils";
import { useLogDetails } from "@/app/(dashboard)/hooks/logDetails/useLogDetails";
import { useT } from "@/i18n";

export interface LogDetailsDrawerProps {
  open: boolean;
  onClose: () => void;
  logEntry: LogEntry | null;
  sessionId?: string | null;
  sessionApiKey?: string | null;
  sessionUserId?: string | null;
  sessionCallerResolved?: boolean;
  accessToken?: string | null;
  allLogs?: LogEntry[];
  onSelectLog?: (log: LogEntry) => void;
  startTime?: string;
}

const DRAWER_WIDTH_STORAGE_KEY = "xhub.logDetails.widthPercent";
const DEFAULT_DRAWER_WIDTH = 60;
const MIN_DRAWER_WIDTH = 40;

/** 限制详情抽屉宽度；参数 value 为相对视口百分比，返回有效的 40–100 数值。
 * 用户拖动、键盘及本地偏好读取时调用；非数字回退默认值，不修改请求数据。 */
function clampDrawerWidth(value: number): number {
  return Number.isFinite(value) ? Math.max(MIN_DRAWER_WIDTH, Math.min(100, value)) : DEFAULT_DRAWER_WIDTH;
}

// Session logs are fetched page-by-page from the paginated backend and
// accumulated so the drawer can show the whole session. page_size is the
// backend maximum (le=100); the page cap bounds the fetch and the
// (un-virtualized) sidebar list for pathological sessions, keeping the most
// recent logs since the endpoint returns newest-first.
const SESSION_PAGE_SIZE = 100;
const MAX_SESSION_PAGES = 50;

/* ------------------------------------------------------------------ */
/*  TraceEventRow — compact event row used in both session & non-     */
/*  session sidebar lists.  Extracted to avoid JSX duplication.       */
/* ------------------------------------------------------------------ */
interface TraceEventRowProps {
  row: LogEntry;
  isSelected: boolean;
  onClick: () => void;
}

const TRACE_EVENT_ICON_CLASS = "text-muted-foreground shrink-0";

function TraceEventIcon({ callType, isAutoRouted }: { callType: string; isAutoRouted: boolean }) {
  const t = useT();
  if (MCP_CALL_TYPES.includes(callType)) return <Wrench size={12} className={TRACE_EVENT_ICON_CLASS} />;
  if (AGENT_CALL_TYPES.includes(callType)) return <Bot size={12} className={TRACE_EVENT_ICON_CLASS} />;
  if (isAutoRouted) return <AutoRouterIcon size={12} className={TRACE_EVENT_ICON_CLASS} />;
  return <Sparkles size={12} className={TRACE_EVENT_ICON_CLASS} />;
}

/** 展示会话中的单次请求；参数为日志、选中态和选择回调，返回可访问按钮。
 * 抽屉侧栏调用；金额沿用原始请求精度，不改写日志，点击仅触发调用方回调。 */
function TraceEventRow({ row, isSelected, onClick }: TraceEventRowProps) {
  const t = useT();
  const isAutoRouted = useIsAutoRoutedModelGroup(row.model_group);
  const durationValue =
    row.request_duration_ms != null
      ? (row.request_duration_ms / 1000).toFixed(3)
      : row.startTime && row.endTime
        ? ((Date.parse(row.endTime) - Date.parse(row.startTime)) / 1000).toFixed(3)
        : "-";

  return (
    <button
      type="button"
      className={`w-full text-left pl-8 pr-2 py-1 transition-colors ${isSelected ? "bg-info/10" : "hover:bg-accent"}`}
      onClick={onClick}
    >
      <div className="flex items-center gap-1">
        <TraceEventIcon callType={row.call_type} isAutoRouted={isAutoRouted} />
        <span className="text-xs font-medium text-foreground truncate">
          {getEventDisplayName(row.call_type, row.model)}
        </span>
        <ClassifyTag origin={row.metadata?.internal_call_origin} className="ml-auto" />
      </div>
      <div className="text-[11px] text-muted-foreground mt-1 flex flex-wrap items-center gap-x-1.5 gap-y-0.5 font-mono">
        <span>{durationValue}s</span>
        {row.spend != null ? (
          <>
            <span>·</span>
            <span>{formatLogCost(row.spend, row)}</span>
          </>
        ) : null}
        {row.total_tokens ? (
          <>
            <span>·</span>
            <span>{row.total_tokens} tok</span>
          </>
        ) : null}
      </div>
    </button>
  );
}

/** 展示请求及会话日志抽屉；参数包括选中日志、授权令牌和关闭/选择回调，返回可导航详情。
 * 日志页调用；金额使用同一展示格式，零合计含未定价请求时保持未知；查询遵循后台授权，不重新计费或修改历史记录。 */
export function LogDetailsDrawer({
  open,
  onClose,
  logEntry,
  sessionId,
  sessionApiKey,
  sessionUserId,
  sessionCallerResolved = true,
  accessToken,
  allLogs = [],
  onSelectLog,
  startTime,
}: LogDetailsDrawerProps) {
  const t = useT();
  const isSessionMode = Boolean(sessionId);
  const [selectedSessionRequestId, setSelectedSessionRequestId] = useState<string | null>(null);
  const [sessionSortMode, setSessionSortMode] = useState<SessionLogSortMode>("duration");
  const [isSidebarCollapsed, setIsSidebarCollapsed] = useState(false);
  const [copiedLeftPanelId, setCopiedLeftPanelId] = useState(false);
  const [drawerWidth, setDrawerWidth] = useState(DEFAULT_DRAWER_WIDTH);
  const [isExpanded, setIsExpanded] = useState(false);
  const [isCompact, setIsCompact] = useState(false);
  const [viewportWidth, setViewportWidth] = useState(0);
  const dragging = useRef(false);

  useEffect(() => {
    // 本地偏好不可读或损坏时仅使用默认宽度，不影响日志查询。
    try {
      const saved = window.localStorage.getItem(DRAWER_WIDTH_STORAGE_KEY);
      if (saved !== null && saved.trim() !== "") setDrawerWidth(clampDrawerWidth(Number(saved)));
    } catch { /* 浏览器可能禁用本地存储。 */ }
    const updateViewport = () => {
      const compact = window.innerWidth < 640;
      setViewportWidth(window.innerWidth);
      setIsCompact(compact);
      if (compact) setIsSidebarCollapsed(true);
    };
    updateViewport();
    window.addEventListener("resize", updateViewport);
    return () => window.removeEventListener("resize", updateViewport);
  }, []);

  /** 保存拖拽或键盘选择的抽屉百分比；参数 value 为待设置宽度，无返回值。
   * 拖动控件调用；同时退出全屏，本地存储不可用不阻塞操作。 */
  const updateDrawerWidth = (value: number) => {
    const minimum = window.innerWidth > 0 ? Math.min(100, Math.max(MIN_DRAWER_WIDTH, 640 / window.innerWidth * 100)) : MIN_DRAWER_WIDTH;
    const next = Math.max(minimum, clampDrawerWidth(value));
    setDrawerWidth(next);
    setIsExpanded(false);
    try { window.localStorage.setItem(DRAWER_WIDTH_STORAGE_KEY, String(next)); } catch { /* 使用内存状态。 */ }
  };

  const { data: sessionData } = useQuery({
    queryKey: ["sessionLogs", sessionId, sessionApiKey, sessionUserId],
    queryFn: async () => {
      if (!sessionId || !accessToken) return { logs: [] as LogEntry[], total: 0 };

      // Fetch the first page, then page through the rest so sessions with more
      // than one page of logs are shown in full (capped for safety).
      const caller = { apiKey: sessionApiKey || undefined, userId: sessionUserId || undefined };
      const firstPage = await sessionSpendLogsCall(accessToken, sessionId, 1, SESSION_PAGE_SIZE, caller);
      let rows: LogEntry[] = firstPage.data || firstPage || [];
      const pagesToFetch = Math.min(firstPage.total_pages ?? 1, MAX_SESSION_PAGES);

      if (pagesToFetch > 1) {
        const BATCH = 5;
        const remaining: Awaited<ReturnType<typeof sessionSpendLogsCall>>[] = [];
        for (let start = 2; start <= pagesToFetch; start += BATCH) {
          const end = Math.min(start + BATCH - 1, pagesToFetch);
          const batch = await Promise.all(
            Array.from({ length: end - start + 1 }, (_, i) =>
              sessionSpendLogsCall(accessToken, sessionId, start + i, SESSION_PAGE_SIZE, caller),
            ),
          );
          remaining.push(...batch);
        }
        for (const page of remaining) {
          rows = rows.concat(page.data || []);
        }
      }

      // Fall back to the accumulated row count (not just the first page) when the
      // backend omits total, so the truncation note reflects what was fetched.
      const total: number = firstPage.total ?? rows.length;

      const logs = rows.map((row) => ({
        ...row,
        request_duration_ms: row.request_duration_ms ?? Date.parse(row.endTime) - Date.parse(row.startTime),
      }));

      return { logs, total };
    },
    enabled: Boolean(open && isSessionMode && sessionId && accessToken && sessionCallerResolved),
  });

  const sessionLogs: LogEntry[] = useMemo(
    () => sortSessionLogs(sessionData?.logs ?? [], sessionSortMode),
    [sessionData, sessionSortMode],
  );
  // total reported by the backend; when the page cap truncates the fetch this
  // exceeds sessionLogs.length, which drives the "showing most recent" note.
  const sessionTotalCount = sessionData?.total ?? sessionLogs.length;
  const sessionTruncated = sessionTotalCount > sessionLogs.length;

  // Default selection for a freshly opened session: the most recent log (latest
  // startTime). The list is ordered by the selected sort mode, so the latest
  // log by time is not necessarily sessionLogs[0]; compute it explicitly.
  // A clicked/remembered log still wins over this default.
  const mostRecentLog = useMemo<LogEntry | null>(
    () =>
      sessionLogs.reduce<LogEntry | null>(
        (latest, row) =>
          !latest || new Date(row.startTime).getTime() > new Date(latest.startTime).getTime() ? row : latest,
        null,
      ),
    [sessionLogs],
  );

  const currentLog = useMemo(() => {
    if (!isSessionMode) return logEntry;
    // The clicked row stays visible while the rest of the session loads, and
    // if that fetch fails. An empty session list used to close the drawer.
    if (!sessionLogs.length) return logEntry;
    const fallbackLog = mostRecentLog ?? sessionLogs[0];
    if (selectedSessionRequestId) {
      return sessionLogs.find((row) => row.request_id === selectedSessionRequestId) || fallbackLog;
    }
    if (logEntry?.request_id) {
      const clickedLog = sessionLogs.find((row) => row.request_id === logEntry.request_id);
      return clickedLog || fallbackLog;
    }
    return fallbackLog;
  }, [isSessionMode, logEntry, selectedSessionRequestId, sessionLogs, mostRecentLog]);

  useEffect(() => {
    if (!isSessionMode || !sessionLogs.length) return;
    if (!selectedSessionRequestId || !sessionLogs.some((row) => row.request_id === selectedSessionRequestId)) {
      const fallbackRequestId =
        logEntry?.request_id && sessionLogs.some((row) => row.request_id === logEntry.request_id)
          ? logEntry.request_id
          : (mostRecentLog ?? sessionLogs[0]).request_id;
      setSelectedSessionRequestId(fallbackRequestId);
    }
  }, [isSessionMode, logEntry, selectedSessionRequestId, sessionLogs, mostRecentLog]);

  // 打开时依据当前屏宽收起侧栏；关闭时清理临时选择，避免窄屏文字被挤压。
  useEffect(() => {
    if (open) {
      setIsSidebarCollapsed(window.innerWidth < 640);
    } else {
      if (isSessionMode) setSelectedSessionRequestId(null);
      setSessionSortMode("duration");
      setCopiedLeftPanelId(false);
    }
  }, [open, isSessionMode]);

  // Keyboard navigation
  const { selectNextLog, selectPreviousLog } = useKeyboardNavigation({
    isOpen: open,
    currentLog,
    allLogs: isSessionMode ? sessionLogs : allLogs,
    onClose,
    onSelectLog: (selected) => {
      if (isSessionMode) {
        setSelectedSessionRequestId(selected.request_id);
      }
      onSelectLog?.(selected);
    },
  });

  // Lazy-load log details (messages/response) only when drawer is open.
  // This fetches data for a single log on-demand instead of prefetching all 50.
  const logDetails = useLogDetails(currentLog?.request_id, startTime, open && !!currentLog?.request_id);
  const detailsData = logDetails.data as any;
  const isLoadingDetails = logDetails.isLoading;

  // Build an enriched log entry that merges lazy-loaded details.
  // The list endpoint may already include messages/response when store_prompts_in_spend_logs is enabled,
  // while the detail endpoint fetches from custom loggers (S3, GCS, etc.) or DB fallback.
  const enrichedLog = useMemo(() => {
    if (!currentLog) return null;
    return {
      ...currentLog,
      status: detailsData?.status || currentLog.status,
      messages: detailsData?.messages || currentLog.messages,
      response: detailsData?.response || currentLog.response,
      error: detailsData?.error ?? currentLog.error,
      metadata: { ...currentLog.metadata, ...detailsData?.metadata },
      proxy_server_request: detailsData?.proxy_server_request || currentLog.proxy_server_request,
    };
  }, [currentLog, detailsData]);

  const metadata = currentLog?.metadata || {};

  // Status display values
  const hasError = enrichedLog ? isRequestFailure(enrichedLog) : false;
  const statusLabel = t(requestLogStatus(enrichedLog || { status: "" }).label);
  const statusColor = hasError ? ("error" as const) : ("success" as const);
  const environment = metadata?.user_api_key_team_alias || t("logsUX.teamUnknown");
  const minimumWidth = viewportWidth > 0 ? Math.min(100, Math.max(MIN_DRAWER_WIDTH, 640 / viewportWidth * 100)) : MIN_DRAWER_WIDTH;
  const displayedWidth = Math.max(minimumWidth, drawerWidth);

  const totalSessionCost = sessionLogs.reduce((sum, row) => sum + (row.spend || 0), 0);
  // 零合计不能证明会话免费；含未定价且非响应缓存命中的请求时，沿用该请求的未知状态。
  const unpricedSessionLog = sessionLogs.find(
    (row) => row.metadata?.cost_breakdown?.pricing_status === "unpriced" && String(row.cache_hit).toLowerCase() !== "true",
  );
  const sessionStart =
    sessionLogs.length > 0 ? new Date(Math.min(...sessionLogs.map((r) => new Date(r.startTime).getTime()))) : null;
  const sessionEnd =
    sessionLogs.length > 0 ? new Date(Math.max(...sessionLogs.map((r) => new Date(r.endTime).getTime()))) : null;
  const sessionDurationSeconds =
    sessionStart && sessionEnd ? ((sessionEnd.getTime() - sessionStart.getTime()) / 1000).toFixed(2) : "0.00";
  const llmCount = sessionLogs.filter(
    (row) => !MCP_CALL_TYPES.includes(row.call_type) && !AGENT_CALL_TYPES.includes(row.call_type),
  ).length;
  const agentCount = sessionLogs.filter((row) => AGENT_CALL_TYPES.includes(row.call_type)).length;
  const mcpCount = sessionLogs.filter((row) => MCP_CALL_TYPES.includes(row.call_type)).length;
  const cacheHitCount = sessionLogs.filter((row) => String(row.cache_hit ?? "").toLowerCase() === "true").length;
  const logsForList = isSessionMode ? sessionLogs : currentLog ? [currentLog] : [];
  const leftPanelId = isSessionMode ? sessionId || "" : currentLog?.request_id || "";
  const leftPanelDisplayId = leftPanelId.length > 14 ? `${leftPanelId.slice(0, 11)}...` : leftPanelId;
  // 缓存键或提示词只能说明请求被推断归组，不能将其当作有上下文传递的真实会话。
  const sessionLabel = currentLog?.session_source === "cache_key" ? t("logsUX.relatedCache")
    : currentLog?.session_source === "prompt" ? t("logsUX.relatedPrompt") : t("Session");

  const handleCopyLeftPanelId = async () => {
    if (!leftPanelId) return;
    try {
      await navigator.clipboard.writeText(leftPanelId);
      setCopiedLeftPanelId(true);
      setTimeout(() => setCopiedLeftPanelId(false), 1200);
    } catch {
      /* clipboard unavailable in non-secure contexts */
    }
  };

  if (!currentLog || !enrichedLog) return null;

  return (
    <Sheet
      open={open}
      onOpenChange={(nextOpen) => {
        if (!nextOpen) onClose();
      }}
    >
      <SheetContent
        side="right"
        showCloseButton={false}
        className="gap-0 overflow-hidden p-0 data-[side=right]:sm:max-w-none"
        style={{ width: isCompact || isExpanded ? "100%" : `${displayedWidth}%` }}
      >
        <SheetTitle className="sr-only">
          {logEntry?.request_id ? t("Request {value0} details", { value0: (logEntry.request_id) }) : t("Request details")}
        </SheetTitle>
        {!isCompact && <button type="button" role="separator" aria-orientation="vertical" aria-label={t("logsUX.resizeDrawer")}
          aria-valuemin={Math.ceil(minimumWidth)} aria-valuemax={100} aria-valuenow={isExpanded ? 100 : Math.max(Math.ceil(minimumWidth), Math.round(displayedWidth))}
          className="absolute inset-y-0 left-0 z-raised flex w-2 cursor-ew-resize touch-none items-center justify-center hover:bg-primary/20 focus-visible:ring-2 focus-visible:ring-ring"
          onPointerDown={(event) => { dragging.current = true; event.currentTarget.setPointerCapture?.(event.pointerId); }}
          onPointerMove={(event) => { if (dragging.current && window.innerWidth > 0) updateDrawerWidth((window.innerWidth - event.clientX) / window.innerWidth * 100); }}
          onPointerUp={() => { dragging.current = false; }}
          onPointerCancel={() => { dragging.current = false; }}
          onLostPointerCapture={() => { dragging.current = false; }}
          onKeyDown={(event) => { if (event.key === "ArrowLeft" || event.key === "ArrowRight") { event.preventDefault(); updateDrawerWidth((isExpanded ? 100 : displayedWidth) + (event.key === "ArrowLeft" ? 5 : -5)); } }}
        ><span aria-hidden="true" className="h-12 w-1 rounded-full bg-border" /></button>}
        <div style={{ height: "100%" }} className="flex relative min-w-0">
          {/* 窄屏侧栏悬浮于详情头部之上，折叠按钮再高一层，保证触屏始终可收起。 */}
          {!isSidebarCollapsed && (
            <SidebarToggle
              isCollapsed={false}
              onToggle={() => setIsSidebarCollapsed(true)}
              className="absolute top-2 left-2 z-raised max-sm:z-floating"
            />
          )}
          {!isSidebarCollapsed && (
            <div className="border-r border-border bg-muted flex shrink-0 flex-col w-[clamp(160px,24%,256px)] max-sm:absolute max-sm:inset-y-0 max-sm:left-0 max-sm:z-sticky max-sm:w-[min(80vw,256px)]">
              <div className="pl-12 pr-3 py-2 border-b border-border bg-card">
                <div className="flex items-start justify-between gap-2">
                  <div className="min-w-0">
                    <div className="text-[10px] uppercase tracking-wide text-muted-foreground">
                      {isSessionMode ? sessionLabel : t("Trace")}
                    </div>
                    <div className="font-mono text-[12px] text-foreground leading-tight flex items-center gap-1">
                      <span className="truncate">{leftPanelDisplayId}</span>
                      <button
                        type="button"
                        onClick={handleCopyLeftPanelId}
                        className="text-muted-foreground hover:text-foreground"
                        aria-label={t("Copy trace id")}
                      >
                        {copiedLeftPanelId ? <Check className="size-3" /> : <Copy className="size-3" />}
                      </button>
                    </div>
                  </div>
                </div>
                <div className="mt-1 text-[11px] text-muted-foreground font-mono">
                  {logsForList.length} {t("req")}
                  {[
                    isSessionMode
                      ? llmCount
                      : logsForList.filter(
                          (row) => !MCP_CALL_TYPES.includes(row.call_type) && !AGENT_CALL_TYPES.includes(row.call_type),
                        ).length,
                    isSessionMode
                      ? agentCount
                      : logsForList.filter((row) => AGENT_CALL_TYPES.includes(row.call_type)).length,
                    isSessionMode
                      ? mcpCount
                      : logsForList.filter((row) => MCP_CALL_TYPES.includes(row.call_type)).length,
                  ].map((count, i) => {
                    const label = [" LLM", " Agent", " MCP"][i];
                    return count > 0 ? (
                      <span key={label}>
                        <span className="mx-1.5">·</span>
                        {count}
                        {label}
                      </span>
                    ) : null;
                  })}
                  <span className="mx-1.5">·</span>
                  {isSessionMode
                    ? formatLogCost(totalSessionCost, unpricedSessionLog)
                    : formatLogCost(enrichedLog.spend, enrichedLog)}
                  {isSessionMode && (
                    <>
                      <span className="mx-1.5">·</span>
                      {sessionDurationSeconds}s
                    </>
                  )}
                </div>
                {isSessionMode && (
                  <div className="text-[11px] text-muted-foreground font-mono break-words">
                    {t("Response cache hits")}: {cacheHitCount}/{logsForList.length}
                  </div>
                )}
                {isSessionMode && sessionTruncated && (
                  <div className="mt-1 text-[11px] text-warning font-mono">
                    {t("Showing most recent {value0} of {sessionTotalCount}", { value0: (logsForList.length), sessionTotalCount })}
                  </div>
                )}
                {isSessionMode && (
                  <Tabs
                    className="mt-1.5"
                    value={sessionSortMode}
                    onValueChange={(value) => setSessionSortMode(value as SessionLogSortMode)}
                  >
                    <TabsList className="w-full">
                      <TabsTrigger value="duration" className="text-[11px]">
                        {t("Duration")}
                      </TabsTrigger>
                      <TabsTrigger value="start_time" className="text-[11px]">
                        {t("Start time")}
                      </TabsTrigger>
                    </TabsList>
                  </Tabs>
                )}
              </div>

              <div className="flex-1 overflow-y-auto">
                {normalizeGuardrailEntries(metadata?.guardrail_information).length > 0 && (
                  <div className="px-3 pt-2">
                    <GuardrailJumpLink guardrailEntries={normalizeGuardrailEntries(metadata?.guardrail_information)} />
                  </div>
                )}
                {isSessionMode ? (
                  <div className="py-1">
                    {/* Child events — vertical tree line with horizontal connectors */}
                    <div className="relative pl-2">
                      <div className="absolute left-4 top-1 bottom-1 border-l border-border" />
                      {logsForList.map((row, idx) => {
                        const isLast = idx === logsForList.length - 1;
                        return (
                          <div key={row.request_id} className="relative">
                            <div className="absolute left-4 top-3 w-3 border-t border-border" />
                            {isLast && <div className="absolute left-4 top-3 bottom-0 w-px bg-muted" />}
                            <TraceEventRow
                              row={row}
                              isSelected={row.request_id === currentLog.request_id}
                              onClick={() => {
                                setSelectedSessionRequestId(row.request_id);
                                onSelectLog?.(row);
                              }}
                            />
                          </div>
                        );
                      })}
                    </div>
                  </div>
                ) : (
                  <div className="py-1">
                    {logsForList.map((row) => (
                      <TraceEventRow
                        key={row.request_id}
                        row={row}
                        isSelected={row.request_id === currentLog.request_id}
                        onClick={() => onSelectLog?.(row)}
                      />
                    ))}
                  </div>
                )}
              </div>
            </div>
          )}

          <div className="min-w-0 flex-1 flex flex-col overflow-hidden">
            <DrawerHeader
              log={currentLog}
              onClose={onClose}
              isSidebarCollapsed={isSidebarCollapsed}
              onToggleSidebar={() => setIsSidebarCollapsed((collapsed) => !collapsed)}
              isExpanded={isExpanded}
              onToggleExpanded={() => setIsExpanded((value) => !value)}
              onPrevious={selectPreviousLog}
              onNext={selectNextLog}
              statusLabel={statusLabel}
              statusColor={statusColor}
              environment={environment}
            />
            <div className="flex-1 overflow-y-auto">
              {logDetails.isError && <div role="alert" className="m-4 rounded-md border border-destructive/30 p-3 text-sm text-destructive">
                {t("logsUX.detailFailed")}
                <button type="button" className="ml-2 underline" onClick={() => void logDetails.refetch()}>{t("Refresh")}</button>
              </div>}
              <LogDetailContent
                logEntry={enrichedLog}
                isLoadingDetails={isLoadingDetails}
                isDetailsError={logDetails.isError}
                accessToken={accessToken ?? null}
              />
            </div>
          </div>
        </div>
      </SheetContent>
    </Sheet>
  );
}

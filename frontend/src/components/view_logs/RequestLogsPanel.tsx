"use client";

import { useDebouncedValue } from "@tanstack/react-pacer/debouncer";
import { useQuery } from "@tanstack/react-query";
import type { ColumnFiltersState, OnChangeFn, PaginationState, SortingState } from "@tanstack/react-table";
import moment from "moment";
import { useCallback, useEffect, useMemo, useState } from "react";

import { DEFAULT_PAGE_SIZE_OPTIONS } from "@/components/shared/DataTable";
import { AutoRouterModelGroupsProvider } from "@/components/shared/table_cells";
import { DEBOUNCE_WAIT_MS } from "@/utils/debounceConstants";
import { logDetailsQueryOptions } from "@/app/(dashboard)/hooks/logDetails/useLogDetails";
import type { LogEntry } from "./columns";
import { useT } from "@/i18n";
import {
  DEFAULT_LOGS_SORTING,
  DEFAULT_LOGS_INTERVAL,
  formatLogsWindow,
  getLogsWindowEndBound,
  LOG_FILTER_IDS,
  useLogFilterLogic,
} from "./log_filter_logic";
import { useLogDetailRouting } from "./logDetailRouting";
import { LogDetailsDrawer } from "./LogDetailsDrawer";
import { LogsTableToolbar } from "./LogsTableToolbar";
import { RequestLogsTable } from "./RequestLogsTable";

const PAGE_SIZE = DEFAULT_PAGE_SIZE_OPTIONS[0];
const matchesLogId = (log: LogEntry, logId: string) => log.request_id === logId || log.litellm_call_id === logId;
const findLogById = (logs: readonly LogEntry[], logId: string): LogEntry | null =>
  logs.find((log) => log.request_id === logId) ?? logs.find((log) => log.litellm_call_id === logId) ?? null;

interface RequestLogsPanelProps {
  accessToken: string;
  token: string;
  userRole: string;
  userID: string;
  isActive: boolean;
  errorsOnly?: boolean;
}

/** 查询并展示请求日志；普通面板排除失败，errorsOnly 固定失败筛选并逐条列出请求，避免会话聚合隐藏错误。
 * 参数为登录凭据和活跃状态，返回日志面板，供日志页使用；后台负责权限和分页。
 * 密钥 Hash 仅展示，行点击打开日志详情；面板不再查询或切换到密钥详情，避免历史 Hash 触发 404。 */
export default function RequestLogsPanel({ accessToken, token, userRole, userID, isActive, errorsOnly = false }: RequestLogsPanelProps) {
  const t = useT();
  const [pagination, setPagination] = useState<PaginationState>({ pageIndex: 0, pageSize: PAGE_SIZE });
  const [sorting, setSorting] = useState<SortingState>(DEFAULT_LOGS_SORTING);
  const [columnFilters, setColumnFilters] = useState<ColumnFiltersState>([]);
  const [sessionCursors, setSessionCursors] = useState<Record<number, string>>({});

  const [startTime, setStartTime] = useState<string>(moment().subtract(24, "hours").format("YYYY-MM-DDTHH:mm"));
  const [endTime, setEndTime] = useState<string>(moment().format("YYYY-MM-DDTHH:mm"));
  const [isCustomDate, setIsCustomDate] = useState(false);
  const [selectedTimeInterval, setSelectedTimeInterval] = useState<{ value: number; unit: string }>(DEFAULT_LOGS_INTERVAL);

  const [selectedLog, setSelectedLog] = useState<LogEntry | null>(null);

  const {
    logId: urlLogId,
    sessionId: urlSessionId,
    openLog,
    openSession,
    selectLog,
    close: closeUrlLog,
  } = useLogDetailRouting();

  const [isLiveTail, setIsLiveTail] = useState<boolean>(() => {
    const storedValue = sessionStorage.getItem("isLiveTail");
    return storedValue !== "false";
  });

  useEffect(() => {
    sessionStorage.setItem("isLiveTail", JSON.stringify(isLiveTail));
  }, [isLiveTail]);

  const searchTerm = useMemo(() => {
    const entry = columnFilters.find((filter) => filter.id === LOG_FILTER_IDS.SEARCH);
    return typeof entry?.value === "string" ? entry.value : "";
  }, [columnFilters]);
  const [debouncedSearch] = useDebouncedValue(searchTerm, { wait: DEBOUNCE_WAIT_MS });
  const queryColumnFilters = useMemo<ColumnFiltersState>(() => {
    const others = columnFilters.filter((filter) => filter.id !== LOG_FILTER_IDS.SEARCH);
    return debouncedSearch === "" ? others : [...others, { id: LOG_FILTER_IDS.SEARCH, value: debouncedSearch }];
  }, [columnFilters, debouncedSearch]);

  const { logsQuery, filteredLogs, allTeams, usesSessionCursor } = useLogFilterLogic({
    accessToken,
    token,
    userRole,
    userID,
    columnFilters: queryColumnFilters,
    activeTab: isActive ? "request logs" : "inactive",
    isLiveTail,
    startTime,
    endTime,
    pagination,
    isCustomDate,
    presetInterval: selectedTimeInterval,
    sorting,
    sessionCursors,
    errorsOnly,
  });

  // 用户选项查询沿用日志窗口，自动刷新时同步移动边界。
  const windowEndBound = getLogsWindowEndBound(logsQuery.dataUpdatedAt || Date.parse(endTime));
  const logsWindow = useMemo(
    () => formatLogsWindow(startTime, endTime, isCustomDate, windowEndBound, selectedTimeInterval),
    [startTime, endTime, isCustomDate, windowEndBound, selectedTimeInterval],
  );

  // 深链接和抽屉使用相同缓存键及有效期，已下载的完整正文只读一次。
  const urlLogQueryOptions = {
    ...logDetailsQueryOptions(accessToken, urlLogId ?? undefined),
    enabled: !!accessToken && urlLogId !== null && !(selectedLog !== null && matchesLogId(selectedLog, urlLogId)),
  };

  const { data: urlLog, isError: detailError } = useQuery(urlLogQueryOptions);

  const displayLog = useMemo<LogEntry | null>(() => {
    if (urlLogId === null) return null;
    if (selectedLog !== null && matchesLogId(selectedLog, urlLogId)) return selectedLog;
    return findLogById(filteredLogs.data, urlLogId) ?? urlLog ?? null;
  }, [urlLogId, selectedLog, filteredLogs.data, urlLog]);

  const displaySessionId = useMemo<string | null>(() => {
    if (urlSessionId !== null) return urlSessionId;
    if (displayLog?.session_id !== undefined && (displayLog.session_total_count || 1) > 1) {
      return displayLog.session_id;
    }
    return null;
  }, [urlSessionId, displayLog]);

  const isDrawerOpen = displayLog !== null || displaySessionId !== null;

  const rows: LogEntry[] = filteredLogs.data;
  const rowsThroughThisPage = pagination.pageIndex * pagination.pageSize + rows.length;
  const isLastPage =
    filteredLogs.has_more === false || (filteredLogs.has_more === undefined && rows.length < pagination.pageSize);
  const rowCount = !usesSessionCursor ? filteredLogs.total : isLastPage ? rowsThroughThisPage : Math.max(filteredLogs.total, rowsThroughThisPage);

  const handleSearchChange = useCallback((value: string) => {
    setColumnFilters((previous) => {
      const others = previous.filter((filter) => filter.id !== LOG_FILTER_IDS.SEARCH);
      return value === "" ? others : [...others, { id: LOG_FILTER_IDS.SEARCH, value }];
    });
    setSessionCursors({});
    setPagination((previous) => ({ ...previous, pageIndex: 0 }));
  }, []);

  const handleSortingChange = useCallback<OnChangeFn<SortingState>>((updaterOrValue) => {
    setSorting(updaterOrValue);
    setSessionCursors({});
    setPagination((previous) => ({ ...previous, pageIndex: 0 }));
  }, []);

  const handleColumnFiltersChange = useCallback<OnChangeFn<ColumnFiltersState>>((updaterOrValue) => {
    setColumnFilters(updaterOrValue);
    setSessionCursors({});
    setPagination((previous) => ({ ...previous, pageIndex: 0 }));
  }, []);

  const resetToFirstPage = useCallback(() => {
    setSessionCursors({});
    setPagination((previous) => ({ ...previous, pageIndex: 0 }));
  }, []);

  /** 接收表格页码更新并返回 void；页码接口直接翻页，游标接口缓存下一页游标，加载占位数据期间拒绝导航。 */
  const handlePaginationChange = useCallback<OnChangeFn<PaginationState>>(
    (updaterOrValue) => {
      const requested = typeof updaterOrValue === "function" ? updaterOrValue(pagination) : updaterOrValue;
      if (logsQuery.isPlaceholderData) return;
      if (!usesSessionCursor) {
        setPagination(requested);
        return;
      }
      if (requested.pageSize !== pagination.pageSize) {
        setSessionCursors({});
        setPagination({ ...requested, pageIndex: 0 });
        return;
      }
      if (requested.pageIndex !== pagination.pageIndex + 1) {
        setPagination(requested);
        return;
      }
      const nextCursor = filteredLogs.next_session_cursor;
      if (!nextCursor || logsQuery.isPlaceholderData) return;
      setSessionCursors((previous) => ({ ...previous, [requested.pageIndex]: nextCursor }));
      setPagination(requested);
    },
    [usesSessionCursor, pagination, filteredLogs.next_session_cursor, logsQuery.isPlaceholderData],
  );

  const handleResetFilters = useCallback(() => {
    setColumnFilters([]);
    setStartTime(moment().subtract(24, "hours").format("YYYY-MM-DDTHH:mm"));
    setEndTime(moment().format("YYYY-MM-DDTHH:mm"));
    setIsCustomDate(false);
    setSelectedTimeInterval(DEFAULT_LOGS_INTERVAL);
    resetToFirstPage();
  }, [resetToFirstPage]);

  const handleRowClick = useCallback(
    (log: LogEntry) => {
      setSelectedLog(log);
      if (log.session_id && (log.session_total_count || 1) > 1) {
        openSession(log.session_id, log.request_id);
      } else {
        openLog(log.request_id);
      }
    },
    [openLog, openSession],
  );

  const handleSessionClick = useCallback(
    (log: LogEntry) => {
      if (!log.session_id) return;
      setSelectedLog(log);
      openSession(log.session_id, log.request_id);
    },
    [openSession],
  );

  const handleSelectLog = useCallback(
    (log: LogEntry) => {
      setSelectedLog(log);
      selectLog(log.request_id, displaySessionId);
    },
    [selectLog, displaySessionId],
  );

  return (
    <AutoRouterModelGroupsProvider>
      <div className="flex items-center justify-between mb-4">
        <h1 className="text-xl font-semibold">{errorsOnly ? t("Error Logs") : t("pages.logs.title")}</h1>
      </div>

      {logsQuery.isError && <p role="alert" className="mb-3 text-destructive">{t("logsUX.loadFailed")}</p>}
      {detailError && <p role="alert" className="mb-3 text-destructive">{t("logsUX.detailFailed")}</p>}

      <RequestLogsTable
        data={rows}
        hasLoadError={logsQuery.isError}
        rowCount={rowCount}
        isLoading={logsQuery.isLoading || logsQuery.isPlaceholderData}
        isRefreshing={logsQuery.isFetching}
        pagination={pagination}
        onPaginationChange={handlePaginationChange}
        sorting={sorting}
        onSortingChange={handleSortingChange}
        columnFilters={columnFilters}
        onColumnFiltersChange={handleColumnFiltersChange}
        searchValue={searchTerm}
        onSearchChange={handleSearchChange}
        onRefresh={() => void logsQuery.refetch()}
        onRowClick={handleRowClick}
        onSessionClick={handleSessionClick}
        teams={allTeams ?? []}
        logsWindow={logsWindow}
        errorsOnly={errorsOnly}
        toolbarChildren={
          <LogsTableToolbar
            startTime={startTime}
            onStartTimeChange={setStartTime}
            endTime={endTime}
            onEndTimeChange={setEndTime}
            isCustomDate={isCustomDate}
            onIsCustomDateChange={setIsCustomDate}
            selectedTimeInterval={selectedTimeInterval}
            onSelectedTimeIntervalChange={setSelectedTimeInterval}
            isLiveTail={isLiveTail}
            onIsLiveTailChange={setIsLiveTail}
            onResetToFirstPage={resetToFirstPage}
            onResetFilters={handleResetFilters}
          />
        }
      />

      <LogDetailsDrawer
        open={isDrawerOpen}
        onClose={closeUrlLog}
        logEntry={displayLog}
        sessionId={displaySessionId}
        sessionApiKey={displayLog?.api_key}
        sessionUserId={displayLog?.user}
        sessionCallerResolved={displayLog !== null}
        accessToken={accessToken}
        allLogs={rows}
        onSelectLog={handleSelectLog}
        startTime={undefined}
      />
    </AutoRouterModelGroupsProvider>
  );
}

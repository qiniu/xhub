"use client";

import type { ColumnFiltersState, OnChangeFn, PaginationState, SortingState } from "@tanstack/react-table";
import { ScrollText } from "lucide-react";
import { useMemo, useState, type ReactNode } from "react";

import { DataTable, DataTableFilterDrawer, DataTableToolbar } from "@/components/shared/DataTable";

import type { Team } from "../key_team_helpers/key_list";
import type { LogEntry } from "./columns";
import { LOG_FILTER_LABELS, type LogsWindow } from "./log_filter_logic";
import { RequestLogsFilters } from "./RequestLogsFilters";
import { getRequestLogsTableColumns } from "./RequestLogsTableColumns";
import { useT } from "@/i18n";
import { logColumnVisibility } from "./logPresentation";

interface RequestLogsTableProps {
  data: LogEntry[];
  rowCount: number;
  isLoading: boolean;
  isRefreshing: boolean;
  hasLoadError?: boolean;
  pagination: PaginationState;
  onPaginationChange: OnChangeFn<PaginationState>;
  sorting: SortingState;
  onSortingChange: OnChangeFn<SortingState>;
  columnFilters: ColumnFiltersState;
  onColumnFiltersChange: OnChangeFn<ColumnFiltersState>;
  searchValue: string;
  onSearchChange: (value: string) => void;
  onRefresh: () => void;
  onRowClick: (log: LogEntry) => void;
  onSessionClick: (log: LogEntry) => void;
  teams: Team[];
  logsWindow: LogsWindow;
  toolbarChildren?: ReactNode;
  errorsOnly?: boolean;
}

/** 列表无数据提示；根据是否存在筛选显示调整建议，不将空结果表述为系统从未收到请求。 */
function RequestLogsEmptyState({ filtered }: { filtered: boolean }) {
  const t = useT();
  return (
    <div className="flex flex-col items-center gap-1 py-6">
      <div className="mb-1 flex size-10 items-center justify-center rounded-lg bg-muted">
        <ScrollText className="size-5 text-muted-foreground" />
      </div>
      <div className="text-sm font-medium text-foreground">
        {filtered ? t("No matching requests") : t("logsUX.empty")}
      </div>
      <div className="max-w-xs text-center text-sm text-muted-foreground">
        {filtered ? t("No requests match your filters for this time range.") : t("logsUX.emptyHint")}
      </div>
    </div>
  );
}

/** 渲染后台分页日志及筛选抽屉；参数为日志、分页、筛选与行/会话回调，返回表格。
 * 供 RequestLogsPanel 使用；errorsOnly 隐藏冲突状态筛选，密钥 Hash 仅展示，行与会话点击交给面板处理。 */
export function RequestLogsTable({
  data,
  rowCount,
  isLoading,
  isRefreshing,
  hasLoadError = false,
  pagination,
  onPaginationChange,
  sorting,
  onSortingChange,
  columnFilters,
  onColumnFiltersChange,
  searchValue,
  onSearchChange,
  onRefresh,
  onRowClick,
  onSessionClick,
  teams,
  logsWindow,
  toolbarChildren,
  errorsOnly = false,
}: RequestLogsTableProps) {
  const t = useT();
  const [filtersOpen, setFiltersOpen] = useState(false);

  const columns = useMemo(() => {
    const deps = { onSessionClick };
    const columns = getRequestLogsTableColumns(deps);
    const order = [
      "startTime",
      "model",
      "status",
      "diagnosis",
      "user",
      "request_duration_ms",
      "total_tokens",
      "spend",
      "session_id",
      "request_id",
    ];
    return columns.sort(
      (a, b) =>
        (order.includes(a.id!) ? order.indexOf(a.id!) : 99) - (order.includes(b.id!) ? order.indexOf(b.id!) : 99),
    );
  }, [onSessionClick, t]);

  const isFiltered = columnFilters.length > 0 || searchValue !== "";

  return (
    <DataTable
      data={data}
      columns={columns}
      getRowId={(row) => row.request_id}
      defaultColumnVisibility={logColumnVisibility(errorsOnly)}
      fillHeight
      sortingMode="server"
      sorting={sorting}
      onSortingChange={onSortingChange}
      paginationMode="server"
      pagination={pagination}
      onPaginationChange={onPaginationChange}
      rowCount={rowCount}
      filterMode="server"
      columnFilters={columnFilters}
      onColumnFiltersChange={onColumnFiltersChange}
      isLoading={isLoading}
      loadingMessage={t("Loading request logs…")}
      noDataMessage={hasLoadError ? t("logsUX.loadFailed") : <RequestLogsEmptyState filtered={isFiltered} />}
      size="compact"
      onRowClick={onRowClick}
      toolbar={(table) => (
        <>
          <DataTableToolbar
            table={table}
            searchValue={searchValue}
            onSearchChange={onSearchChange}
            searchPlaceholder={t("Search logs by ID…")}
            onRefresh={onRefresh}
            isRefreshing={isRefreshing}
            onOpenFilters={() => setFiltersOpen(true)}
            filterLabels={Object.fromEntries(Object.entries(LOG_FILTER_LABELS).map(([id, key]) => [id, t(key)]))}
            showViewOptions={true}
          >
            {toolbarChildren}
          </DataTableToolbar>
          <DataTableFilterDrawer
            table={table}
            open={filtersOpen}
            onOpenChange={setFiltersOpen}
            title={t("Filters")}
            applyLabel={t("logsUX.apply")}
            resetLabel={t("Reset")}
            description={t("Narrow down request logs")}
          >
            {({ get, set }) => (
              <RequestLogsFilters get={get} set={set} teams={teams} logsWindow={logsWindow} errorsOnly={errorsOnly} />
            )}
          </DataTableFilterDrawer>
        </>
      )}
    />
  );
}

"use client";

import { useMemo, useState } from "react";

import { useInfiniteSpendLogUsers } from "@/app/(dashboard)/hooks/spendLogs/useSpendLogUsers";
import { DataTableFilterField } from "@/components/shared/DataTable";
import { PaginatedSearchSelect } from "@/components/shared/PaginatedSearchSelect";
import { SearchSelect, type SearchSelectOption } from "@/components/shared/SearchSelect";
import {
  Combobox,
  ComboboxContent,
  ComboboxEmpty,
  ComboboxInput,
  ComboboxItem,
  ComboboxList,
} from "@/components/ui/combobox";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

import useIsOrgAdmin from "@/app/(dashboard)/hooks/useIsOrgAdmin";
import {
  useIsPlatformAdmin,
  useIsTeamAdminForAnyTeam,
} from "@/app/(dashboard)/hooks/sessionIdentity/useSessionIdentity";
import type { Team } from "../key_team_helpers/key_list";
import { ERROR_CODE_OPTIONS } from "./constants";
import { LOG_FILTER_IDS, LOG_FILTER_LABELS, type LogsWindow } from "./log_filter_logic";
import { getActiveLocale, useT } from "@/i18n";
import { translate } from "@/i18n/translate";

const ALL_VALUE = "all";

const CACHE_FILTER_KEYS = [
  { value: ALL_VALUE, label: "All Requests" },
  { value: "hit", label: "Cache Hit" },
  { value: "miss", label: "Cache Miss" },
] as const;
const PAGE_SIZE = 50;

const SEARCH_INPUT_REASONS: ReadonlySet<string> = new Set(["input-change", "input-clear", "clear-press"]);

const asString = (value: unknown): string => (typeof value === "string" ? value : "");
const emptyToUndefined = (value: string): string | undefined => (value === "" ? undefined : value);

/** 按当前可见团队生成选项；接收团队列表与筛选回调，只更新查询草稿，不扩大后台权限范围。 */
function TeamFilterField({
  value,
  onChange,
  teams,
}: {
  value: string;
  onChange: (value: string | undefined) => void;
  teams: Team[];
}) {
  const t = useT();
  const options = useMemo<SearchSelectOption[]>(
    () =>
      teams.map((team) => ({
        label: team.team_alias || team.team_id,
        value: team.team_id,
        sublabel: team.team_id,
      })),
    [teams],
  );

  return (
    <DataTableFilterField label={t("Team ID")}>
      <SearchSelect
        options={options}
        value={value}
        onValueChange={(next) => onChange(next ?? undefined)}
        placeholder={t("Search or select a team")}
        emptyText={t("No teams found")}
      />
    </DataTableFilterField>
  );
}

/** 模型筛选接受公开名称，包括停用或删除的历史型号；返回文本控件，仅收窄查询，不依赖当前可用模型目录。 */
function ModelFilterField({ value, onChange }: { value: string; onChange: (value: string | undefined) => void }) {
  const t = useT();
  return (
    <DataTableFilterField label={t("Model")}>
      <Input
        aria-label={t("Model")}
        value={value}
        onChange={(e) => onChange(emptyToUndefined(e.target.value))}
        placeholder={t("logsUX.modelHint")}
      />
    </DataTableFilterField>
  );
}

/** 用户日志筛选器接收当前值、变更回调和日志时间窗，返回分页选择控件。
 * 调用方为日志筛选表单；空值与重复用户被过滤，仅更新筛选，不修改用户内容或后台数据。
 */
function UserIdFilterField({
  value,
  onChange,
  logsWindow,
}: {
  value: string;
  onChange: (value: string | undefined) => void;
  logsWindow: LogsWindow;
}) {
  const t = useT();
  const [search, setSearch] = useState("");
  const { data, fetchNextPage, hasNextPage, isFetchingNextPage, isLoading, isError } = useInfiniteSpendLogUsers(
    logsWindow,
    PAGE_SIZE,
    emptyToUndefined(search),
  );

  const options = useMemo<SearchSelectOption[]>(() => {
    const seen = new Set<string>();
    return (data?.pages ?? []).flatMap((page) =>
      page.data.flatMap((userId: string) => {
        if (!userId || seen.has(userId)) return [];
        seen.add(userId);
        return [{ label: userId, value: userId }];
      }),
    );
  }, [data]);

  return (
    <DataTableFilterField label={t("User ID")}>
      <PaginatedSearchSelect
        options={options}
        value={value}
        onValueChange={(next) => onChange(next ?? undefined)}
        onSearchChange={setSearch}
        onLoadMore={() => void fetchNextPage()}
        hasNextPage={hasNextPage}
        isLoading={isLoading}
        isFetchingNextPage={isFetchingNextPage}
        placeholder={t("Search an internal user")}
        emptyText={isError ? t("logsUX.lookupFailed") : t("No users found")}
      />
    </DataTableFilterField>
  );
}

/** 错误码筛选：接收当前 HTTP 码及变更回调，按页面语言显示已知码且支持自定义码；仅变更查询，不写入后台。 */
function ErrorCodeFilterField({ value, onChange }: { value: string; onChange: (value: string | undefined) => void }) {
  const t = useT();
  const [query, setQuery] = useState("");
  const locale = getActiveLocale();
  const errorCodeOptions = useMemo(
    () => ERROR_CODE_OPTIONS.map((option) => ({ ...option, label: translate(locale, option.label) })),
    [locale],
  );

  const options = useMemo<SearchSelectOption[]>(() => {
    const trimmed = query.trim();
    const lowered = trimmed.toLowerCase();
    const matches = errorCodeOptions.filter((option) => option.label.toLowerCase().includes(lowered));
    const isKnownCode = errorCodeOptions.some(
      (option) => option.value === trimmed || option.label.toLowerCase() === lowered,
    );
    if (trimmed === "" || isKnownCode || !/^[1-5]\d{2}$/.test(trimmed)) return matches;
    return [...matches, { label: t("Use custom code: {trimmed}", { trimmed }), value: trimmed }];
  }, [query, errorCodeOptions, t]);

  const selected = useMemo<SearchSelectOption | null>(() => {
    if (value === "") return null;
    return errorCodeOptions.find((option) => option.value === value) ?? { label: value, value };
  }, [value, errorCodeOptions]);

  const items = useMemo<SearchSelectOption[]>(() => {
    if (selected === null) return options;
    if (options.some((option) => option.value === selected.value)) return options;
    return [selected, ...options];
  }, [options, selected]);

  return (
    <DataTableFilterField label={t("Error Code")}>
      <Combobox
        items={items}
        value={selected}
        onValueChange={(item: SearchSelectOption | null) => onChange(emptyToUndefined(item?.value ?? ""))}
        onInputValueChange={(next, eventDetails) => setQuery(SEARCH_INPUT_REASONS.has(eventDetails.reason) ? next : "")}
        onOpenChange={(nextOpen) => {
          if (!nextOpen) setQuery("");
        }}
        isItemEqualToValue={(a: SearchSelectOption, b: SearchSelectOption) => a.value === b.value}
        itemToStringLabel={(item: SearchSelectOption) => item.label}
        filter={null}
      >
        <ComboboxInput
          onFocus={(event) => event.currentTarget.select()}
          placeholder={t("Select or type an error code")}
          showClear={value !== ""}
          className="w-full"
        />
        <ComboboxContent>
          <ComboboxEmpty>{t("No error codes found")}</ComboboxEmpty>
          <ComboboxList data-testid="error-code-filter-list">
            {(item: SearchSelectOption) => (
              <ComboboxItem key={item.value} value={item}>
                {item.label}
              </ComboboxItem>
            )}
          </ComboboxList>
        </ComboboxContent>
      </Combobox>
    </DataTableFilterField>
  );
}

interface RequestLogsFiltersProps {
  get: (columnId: string) => unknown;
  set: (columnId: string, value: unknown) => void;
  teams: Team[];
  logsWindow: LogsWindow;
  errorsOnly?: boolean;
}

/** 渲染日志筛选项；接收表格读写回调和授权范围，普通视图不提供失败选项，错误视图隐藏状态选择；返回筛选表单，无数据写入副作用。 */
export function RequestLogsFilters({ get, set, teams, logsWindow, errorsOnly = false }: RequestLogsFiltersProps) {
  const t = useT();
  // 标签随当前页面语言翻译，不能在模块加载时固定为默认语言。
  const CACHE_FILTER_ITEMS = CACHE_FILTER_KEYS.map((item) => ({ ...item, label: t(item.label) }));
  const valueOf = (id: string): string => asString(get(id));
  const setter = (id: string) => (next: string | undefined) => set(id, next);
  // 三种权限 Hook 必须每次都执行，避免角色变化导致 Hook 调用顺序改变。
  const platformAdmin = useIsPlatformAdmin();
  const orgAdmin = useIsOrgAdmin();
  const teamAdmin = useIsTeamAdminForAnyTeam();
  const canChooseScope = platformAdmin || orgAdmin || teamAdmin;

  return (
    <>
      <p className="text-sm text-muted-foreground">{t("logsUX.filterHint")}</p>
      <ModelFilterField value={valueOf(LOG_FILTER_IDS.MODEL_ID)} onChange={setter(LOG_FILTER_IDS.MODEL_ID)} />
      {canChooseScope && (
        <>
          <UserIdFilterField
            value={valueOf(LOG_FILTER_IDS.USER_ID)}
            onChange={setter(LOG_FILTER_IDS.USER_ID)}
            logsWindow={logsWindow}
          />
          <TeamFilterField
            value={valueOf(LOG_FILTER_IDS.TEAM_ID)}
            onChange={setter(LOG_FILTER_IDS.TEAM_ID)}
            teams={teams}
          />
        </>
      )}
      {errorsOnly && (
        <>
          <ErrorCodeFilterField
            value={valueOf(LOG_FILTER_IDS.ERROR_CODE)}
            onChange={setter(LOG_FILTER_IDS.ERROR_CODE)}
          />
          <DataTableFilterField label={t("Error Message")}>
            <Input
              aria-label={t("Error Message")}
              value={valueOf(LOG_FILTER_IDS.ERROR_MESSAGE)}
              onChange={(e) => set(LOG_FILTER_IDS.ERROR_MESSAGE, emptyToUndefined(e.target.value))}
              placeholder={t("logsUX.keywordHint")}
            />
          </DataTableFilterField>
        </>
      )}
      <details className="rounded-md border p-3">
        <summary className="cursor-pointer text-sm font-medium">{t("logsUX.advanced")}</summary>
        <div className="mt-3 space-y-4">
          {[
            LOG_FILTER_IDS.KEY_ALIAS,
            LOG_FILTER_IDS.SESSION_ID,
            LOG_FILTER_IDS.KEY_HASH,
            LOG_FILTER_IDS.SUPPLIER,
            LOG_FILTER_IDS.ENDPOINT,
          ].map((id) => (
            <DataTableFilterField key={id} label={t(LOG_FILTER_LABELS[id])}>
              <Input
                aria-label={t(LOG_FILTER_LABELS[id])}
                value={valueOf(id)}
                onChange={(e) => set(id, emptyToUndefined(e.target.value))}
              />
            </DataTableFilterField>
          ))}
          {!errorsOnly && (
            <DataTableFilterField label={t("logsUX.responseCache")}>
              <Select
                items={CACHE_FILTER_ITEMS}
                value={valueOf(LOG_FILTER_IDS.CACHE_STATUS) || ALL_VALUE}
                onValueChange={(next) => set(LOG_FILTER_IDS.CACHE_STATUS, next === ALL_VALUE ? undefined : next)}
              >
                <SelectTrigger aria-label={t("logsUX.responseCache")}>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {CACHE_FILTER_ITEMS.map((item) => (
                    <SelectItem key={item.value} value={item.value}>
                      {item.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </DataTableFilterField>
          )}
        </div>
      </details>
    </>
  );
}

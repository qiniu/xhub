import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, it, expect, vi } from "vitest";
import { I18nProvider } from "@/i18n/I18nProvider";
import { translate } from "@/i18n/translate";
import { LogsTableToolbar } from "./LogsTableToolbar";

/** 验证时间边界输入在中英文中可访问，更新原值并重置分页；纯组件无外部资源，自动卸载。 */
describe("日志时间输入",()=>{
 it.each(["en","zh-CN"] as const)("%s 时间输入保留原值", async(locale)=>{
  const onStart=vi.fn(),onEnd=vi.fn(),reset=vi.fn();
  render(<I18nProvider initialLocale={locale}><LogsTableToolbar
    startTime="2026-10-30T08:00" endTime="2026-10-30T09:00" isCustomDate={true}
    onStartTimeChange={onStart} onEndTimeChange={onEnd} onIsCustomDateChange={vi.fn()}
    selectedTimeInterval={{value:24,unit:"hours"}} onSelectedTimeIntervalChange={vi.fn()}
    isLiveTail={false} onIsLiveTailChange={vi.fn()} excludeInternalHealthChecks={false}
    onExcludeInternalHealthChecksChange={vi.fn()} onResetToFirstPage={reset} onResetFilters={vi.fn()}
  /></I18nProvider>);
  const start=screen.getByLabelText(translate(locale,"logDiagnostics.startTime"));
  expect(start).toHaveValue("2026-10-30T08:00");
  expect(screen.getByLabelText(translate(locale,"logDiagnostics.endTime"))).toHaveValue("2026-10-30T09:00");
  await userEvent.setup().clear(start);
  expect(onStart).toHaveBeenCalledWith("");expect(reset).toHaveBeenCalled();
 });
});

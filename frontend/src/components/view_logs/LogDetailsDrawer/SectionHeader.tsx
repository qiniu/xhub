/**
 * SectionHeader - Datadog-style header with icon, label, metrics, and copy
 */

import { ChevronDown, ChevronUp, Copy, MessageSquare } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/cva.config";
import { useT } from "@/i18n";
import { formatLogCost } from "../formatLogCost";

interface SectionHeaderProps {
  type: "input" | "output";
  tokens?: number;
  cost?: number;
  onCopy: () => void;
  isCollapsed?: boolean;
  onToggleCollapse?: () => void;
  turnCount?: number;
}

const SUMMARY_CLASSES = "flex flex-1 items-center gap-4";

/** 格式化输入/输出摘要的完整 Token 数；参数 tokens 为数量，返回分组数字，组件展示调用，无状态副作用。 */
function formatHeaderTokens(tokens: number): string {
  return tokens.toLocaleString("en-US");
}

/** 展示日志输入/输出摘要及复制/折叠操作；参数为类型、Token、费用和回调，返回可访问标题组件。
 * 日志详情调用；金额仅格式化既有事实，不修改计费，折叠与复制仅通过传入回调通知调用方。 */
export function SectionHeader({
  type,
  tokens,
  cost,
  onCopy,
  isCollapsed,
  onToggleCollapse,
  turnCount,
}: SectionHeaderProps) {
  const t = useT();
  const summary = (
    <>
      {onToggleCollapse &&
        (isCollapsed ? (
          <ChevronDown className="size-2.5 text-muted-foreground" />
        ) : (
          <ChevronUp className="size-2.5 text-muted-foreground" />
        ))}

      <div className="flex items-center gap-2">
        {type === "input" ? (
          <MessageSquare className="size-3.5 text-muted-foreground" />
        ) : (
          <span className="text-sm opacity-60 grayscale">✨</span>
        )}
        <span className="text-sm font-medium">{type === "input" ? t("Input") : t("Output")}</span>
      </div>

      {tokens !== undefined && tokens > 0 && (
        <span className="text-xs text-muted-foreground">{t("logs.tokenCount", { count: formatHeaderTokens(tokens) })}</span>
      )}

      {cost !== undefined && cost > 0 && (
        <span className="text-xs text-muted-foreground">{t("logs.costValue", { cost: formatLogCost(cost) })}</span>
      )}

      {turnCount !== undefined && turnCount > 0 && (
        <span className="text-xs text-muted-foreground">{t("logs.turnCount", { count: turnCount })}</span>
      )}
    </>
  );

  return (
    <div
      className={cn(
        "flex items-center justify-between bg-muted px-4 py-2.5 transition-colors",
        isCollapsed ? "border-b-0" : "border-b border-border",
      )}
    >
      {onToggleCollapse ? (
        <button
          type="button"
          onClick={onToggleCollapse}
          aria-expanded={!isCollapsed}
          className={cn(SUMMARY_CLASSES, "-mx-2 cursor-pointer rounded-md px-2 py-1 text-left hover:bg-accent")}
        >
          {summary}
        </button>
      ) : (
        <div className={SUMMARY_CLASSES}>{summary}</div>
      )}

      <Tooltip>
        <TooltipTrigger
          render={
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label={type === "input" ? t("Copy input") : t("Copy output")}
              onClick={(e) => {
                e.stopPropagation();
                onCopy();
              }}
            />
          }
        >
          <Copy />
        </TooltipTrigger>
        <TooltipContent>{t("Copy")}</TooltipContent>
      </Tooltip>
    </div>
  );
}

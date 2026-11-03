import { useState } from "react";
import { Check, ChevronDown, ChevronUp, Copy, Maximize2, Minimize2, X } from "lucide-react";
import moment from "moment";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";
import { LogEntry } from "../columns";
import { AutoRouterTag } from "@/components/shared/table_cells";
import { ClassifyTag } from "./ClassifyTag";
import { SidebarToggle } from "./SidebarToggle";
import { getProviderLogoAndName } from "../../provider_info_helpers";
import {
  DRAWER_HEADER_PADDING,
  COLOR_BORDER,
  COLOR_BACKGROUND,
  SPACING_MEDIUM,
  FONT_SIZE_HEADER,
  FONT_SIZE_MEDIUM,
  FONT_FAMILY_MONO,
} from "./constants";
import { useT } from "@/i18n";

interface DrawerHeaderProps {
  log: LogEntry;
  onClose: () => void;
  onPrevious: () => void;
  onNext: () => void;
  statusLabel: string;
  statusColor: "error" | "success";
  environment: string;
  isSidebarCollapsed: boolean;
  onToggleSidebar: () => void;
  isExpanded?: boolean;
  onToggleExpanded?: () => void;
}

/** 显示日志抽屉标题与导航；参数为当前日志、团队状态和布局回调，返回可交互标题。
 * 抽屉调用；展开仅改变本地视图宽度，不修改日志和请求。 */
export function DrawerHeader({
  log,
  onClose,
  onPrevious,
  onNext,
  statusLabel,
  statusColor,
  environment,
  isSidebarCollapsed,
  onToggleSidebar,
  isExpanded = false,
  onToggleExpanded,
}: DrawerHeaderProps) {
  const t = useT();
  const provider = log.custom_llm_provider || "";
  const providerInfo = provider ? getProviderLogoAndName(provider) : null;
  const showToggleWithProvider = isSidebarCollapsed && Boolean(providerInfo || log.model);
  const showToggleWithRequestId = isSidebarCollapsed && !showToggleWithProvider;

  return (
    <div
      className="z-chrome"
      style={{
        padding: DRAWER_HEADER_PADDING,
        borderBottom: `1px solid ${COLOR_BORDER}`,
        backgroundColor: COLOR_BACKGROUND,
        position: "sticky",
        top: 0,
      }}
    >
      {/* 模型和供应商可换行，长名称保留原文。 */}
      <div className="flex items-center gap-2" style={{ marginBottom: SPACING_MEDIUM }}>
        {showToggleWithProvider && <SidebarToggle isCollapsed onToggle={onToggleSidebar} />}
        <ModelProviderSection
          model={log.model}
          modelGroup={log.model_group}
          internalCallOrigin={log.metadata?.internal_call_origin}
          providerLogo={providerInfo?.logo}
          providerName={providerInfo?.displayName}
        />
      </div>

      {/* 请求 ID 缩略显示，导航和复制操作始终保持可访问。 */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          gap: 4,
          marginBottom: SPACING_MEDIUM,
        }}
      >
        {showToggleWithRequestId && <SidebarToggle isCollapsed onToggle={onToggleSidebar} />}
        <RequestIdSection requestId={log.request_id} />
        <NavigationSection onPrevious={onPrevious} onNext={onNext} onClose={onClose} isExpanded={isExpanded} onToggleExpanded={onToggleExpanded} />
      </div>

      {/* 状态、团队和时间按可用宽度换行。 */}
      <StatusBar log={log} statusLabel={statusLabel} statusColor={statusColor} environment={environment} />
    </div>
  );
}

/** 展示模型、供应商及分类；参数为原始名称和标记，返回可换行的标题内容。
 * 抽屉标题调用；保留输入原文，供应商图标加载失败时隐藏图标。 */
function ModelProviderSection({
  model,
  modelGroup,
  internalCallOrigin,
  providerLogo,
  providerName,
}: {
  model: string;
  modelGroup?: string;
  internalCallOrigin?: string | null;
  providerLogo?: string;
  providerName?: string;
}) {
  const t = useT();
  return (
    <div className="flex min-w-0 items-center gap-2">
      {providerLogo && (
        <img
          src={providerLogo}
          alt={providerName || t("Provider")}
          style={{ width: 24, height: 24 }}
          onError={(e) => {
            const target = e.target as HTMLImageElement;
            target.style.display = "none";
          }}
        />
      )}
      <div className="flex min-w-0 flex-wrap items-center gap-2">
        <span className="font-semibold break-all" style={{ fontSize: 14 }}>
          {model}
        </span>
        {providerName && (
          <span className="text-muted-foreground" style={{ fontSize: 12 }}>
            {providerName}
          </span>
        )}
        <AutoRouterTag modelGroup={modelGroup} />
        <ClassifyTag origin={internalCallOrigin} />
      </div>
    </div>
  );
}

/** 展示并复制请求 ID；参数 requestId 为原始标识，返回可访问控件。
 * 抽屉标题调用；复制失败不改变原日志，长 ID 仅在视觉上省略。 */
function RequestIdSection({ requestId }: { requestId: string }) {
  const t = useT();
  const [copied, setCopied] = useState(false);

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(requestId);
      setCopied(true);
      setTimeout(() => setCopied(false), 1200);
    } catch {
      /* clipboard unavailable in non-secure contexts */
    }
  };

  return (
    <div className="flex min-w-0 flex-1 items-center gap-1">
      <TooltipProvider>
        <Tooltip>
          <TooltipTrigger
            render={
              <span
                className="font-semibold"
                style={{
                  fontSize: FONT_SIZE_HEADER,
                  fontFamily: FONT_FAMILY_MONO,
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                  whiteSpace: "nowrap",
                  display: "block",
                }}
              />
            }
          >
            {requestId}
          </TooltipTrigger>
          <TooltipContent>{requestId}</TooltipContent>
        </Tooltip>
      </TooltipProvider>
      <button
        type="button"
        aria-label={copied ? t("Copied!") : t("Copy Request ID")}
        onClick={handleCopy}
        className="shrink-0 text-muted-foreground hover:text-foreground"
      >
        {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
      </button>
    </div>
  );
}

/** 显示展开、前后切换及关闭按钮；参数为对应回调及展开状态，返回导航控件。
 * 抽屉标题调用；按钮只触发调用方回调，不改变日志。 */
function NavigationSection({
  onPrevious,
  onNext,
  onClose,
  isExpanded,
  onToggleExpanded,
}: {
  onPrevious: () => void;
  onNext: () => void;
  onClose: () => void;
  isExpanded: boolean;
  onToggleExpanded?: () => void;
}) {
  const t = useT();
  const keyboardShortcutStyle = {
    border: "1px solid var(--color-border)",
    borderRadius: 4,
    padding: "0 4px",
    fontSize: 12,
    fontFamily: "monospace",
    marginLeft: 4,
    background: "var(--color-muted)",
  };
  const splitStyle = { width: 1, height: 20, background: COLOR_BORDER };

  return (
    <div className="flex shrink-0 items-center gap-1">
      {onToggleExpanded && <Button variant="ghost" size="icon-sm" onClick={onToggleExpanded} aria-label={t(isExpanded ? "logsUX.restoreDrawer" : "logsUX.expandDrawer")}>
        {isExpanded ? <Minimize2 className="size-4" /> : <Maximize2 className="size-4" />}
      </Button>}
      <Button variant="ghost" size="sm" onClick={onPrevious}>
        <ChevronUp className="size-4" />
        <span style={keyboardShortcutStyle}>K</span>
      </Button>
      <div style={splitStyle} />
      <Button variant="ghost" size="sm" onClick={onNext}>
        <ChevronDown className="size-4" />
        <span style={keyboardShortcutStyle}>J</span>
      </Button>
      <div style={splitStyle} />
      <TooltipProvider>
        <Tooltip>
          <TooltipTrigger render={<Button variant="ghost" size="icon-sm" onClick={onClose} />}>
            <X className="size-4" />
          </TooltipTrigger>
          <TooltipContent>{t("ESC to close")}</TooltipContent>
        </Tooltip>
      </TooltipProvider>
    </div>
  );
}

/** 展示状态、团队和请求时间；参数为当前日志与格式化状态，返回可换行状态栏。
 * 抽屉标题调用；团队名称由数据决定，缺失时由调用方提供明确占位。 */
function StatusBar({
  log,
  statusLabel,
  statusColor,
  environment,
}: {
  log: LogEntry;
  statusLabel: string;
  statusColor: "error" | "success";
  environment: string;
}) {
  const t = useT();
  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
      <Badge variant={statusColor === "error" ? "destructive" : "secondary"}>{statusLabel}</Badge>
      <Badge variant="outline" className="max-w-full whitespace-normal break-all">{t("logsUX.team")}: {environment}</Badge>
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
        <span className="text-muted-foreground" style={{ fontSize: FONT_SIZE_MEDIUM }}>
          {moment(log.startTime).format(t("MMM D, YYYY h:mm:ss A"))}
        </span>
        <span className="text-muted-foreground" style={{ fontSize: FONT_SIZE_MEDIUM }}>
          ({moment(log.startTime).fromNow()})
        </span>
      </div>
    </div>
  );
}

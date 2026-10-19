import React, { useState } from "react";
import { CircleHelp } from "lucide-react";

import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
import { Switch } from "@/components/ui/switch";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";
import { useT } from "@/i18n";

const PREDEFINED_INTERVALS = ["7d", "30d", "90d", "180d", "365d"] as const;

interface KeyLifecycleSettingsProps {
  value?: string;
  onChange?: (value: string) => void;
  /** 宿主是否实现自动轮换；不支持时禁用并解释原因，默认兼容已有调用方。 */
  rotationSupported?: boolean;
  autoRotationEnabled: boolean;
  onAutoRotationChange: (enabled: boolean) => void;
  rotationInterval: string;
  onRotationIntervalChange: (interval: string) => void;
  isCreateMode?: boolean;
  neverExpire?: boolean;
  onNeverExpireChange?: (checked: boolean) => void;
  id?: string;
}

/** 为期限和轮换说明渲染提示图标；参数为已翻译说明，返回可访问提示，无写入副作用。 */
const hintIcon = (hint: string): React.ReactNode => (
  <Tooltip>
    <TooltipTrigger
      render={<CircleHelp className="size-3.5 shrink-0 cursor-help text-muted-foreground" />}
      aria-label={hint}
    />
    <TooltipContent>{hint}</TooltipContent>
  </Tooltip>
);

/** 渲染共享的密钥生命周期控件；参数及回调控制期限、永不过期与轮换，返回表单片段；编辑留空保留期限，宿主支持且启用轮换时显示间隔，否则禁用并说明原因。 */
const KeyLifecycleSettings: React.FC<KeyLifecycleSettingsProps> = ({
  value,
  onChange,
  rotationSupported = true,
  autoRotationEnabled,
  onAutoRotationChange,
  rotationInterval,
  onRotationIntervalChange,
  isCreateMode = false,
  neverExpire = false,
  onNeverExpireChange,
  id,
}) => {
  const t = useT();
  const isCustomInterval = Boolean(rotationInterval) && !PREDEFINED_INTERVALS.includes(rotationInterval as never);

  const [showCustomInput, setShowCustomInput] = useState(isCustomInterval);
  const [customInterval, setCustomInterval] = useState(isCustomInterval ? rotationInterval : "");

  /** 根据当前语言格式化间隔；输入协议值，返回可见标签，未知自定义值原样保留。 */
  const intervalLabel = (interval: string) => {
    if (interval === "custom") return t("keyEditor.custom");
    if (PREDEFINED_INTERVALS.includes(interval as never))
      return t("keyEditor.days", { count: Number(interval.slice(0, -1)) });
    return interval;
  };

  const durationId = id ?? "key-lifecycle-duration";

  /** 处理预设或自定义间隔；输入选项值，返回无，自定义选项只展开输入而不提前提交。 */
  const handleIntervalChange = (next: string) => {
    if (next === "custom") {
      setShowCustomInput(true);
      return;
    }
    setShowCustomInput(false);
    setCustomInterval("");
    onRotationIntervalChange(next);
  };

  /** 将自定义间隔输入同步给调用方；参数为输入事件，返回无，不改写用户输入内容。 */
  const handleCustomIntervalChange = (event: React.ChangeEvent<HTMLInputElement>) => {
    setCustomInterval(event.target.value);
    onRotationIntervalChange(event.target.value);
  };

  /** 同步永不过期选项；参数为选中状态，返回无，勾选时清除相对期限输入。 */
  const handleNeverExpireChange = (checked: boolean) => {
    onNeverExpireChange?.(checked);
    if (checked) {
      onChange?.("");
    }
  };

  return (
    <TooltipProvider>
      <div className="space-y-6">
        <div className="space-y-4">
          <span className="text-sm font-medium text-foreground">{t("keyEditor.expiryTitle")}</span>

          <div className="space-y-2">
            <div className="flex items-center space-x-1 text-sm font-medium text-foreground">
              <label htmlFor={durationId}>{t("keyEditor.expire")}</label>
              {hintIcon(t(isCreateMode ? "keyEditor.createExpiryHint" : "keyEditor.expiryHint"))}
              {!isCreateMode && onNeverExpireChange && (
                <span className="ml-2 flex items-center gap-2 text-sm font-normal text-muted-foreground">
                  <Checkbox
                    id={`${durationId}-never-expire`}
                    checked={neverExpire}
                    onCheckedChange={handleNeverExpireChange}
                  />
                  <label htmlFor={`${durationId}-never-expire`} className="cursor-pointer">
                    {t("keyEditor.neverExpire")}
                  </label>
                </span>
              )}
            </div>
            <Input
              id={durationId}
              value={value ?? ""}
              onChange={(event) => onChange?.(event.target.value)}
              placeholder={isCreateMode ? t("keyEditor.createPlaceholder") : t("keyEditor.editPlaceholder")}
              className="w-full"
              disabled={!isCreateMode && neverExpire}
            />
          </div>
        </div>

        <Separator />

        <div className="space-y-4">
          <span className="text-sm font-medium text-foreground">{t("keyEditor.rotationTitle")}</span>

          <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
            <div className="space-y-2">
              <label className="flex items-center space-x-1 text-sm font-medium text-foreground">
                <span>{t("keyEditor.enableRotation")}</span>
                {hintIcon(t("keyEditor.rotationHint"))}
              </label>
              <Switch
                aria-label={t("keyEditor.enableRotation")}
                disabled={!rotationSupported}
                aria-describedby={!rotationSupported ? `${durationId}-rotation-unavailable` : undefined}
                checked={rotationSupported && autoRotationEnabled}
                onCheckedChange={onAutoRotationChange}
              />
            </div>

            {rotationSupported && autoRotationEnabled && (
              <div className="space-y-2">
                <label className="flex items-center space-x-1 text-sm font-medium text-foreground">
                  <span>{t("keyEditor.interval")}</span>
                  {hintIcon(t("keyEditor.intervalHint"))}
                </label>
                <div className="space-y-2">
                  <Select
                    value={showCustomInput ? "custom" : rotationInterval || null}
                    onValueChange={(next: string | null) => next !== null && handleIntervalChange(next)}
                  >
                    <SelectTrigger className="w-full" aria-label={t("keyEditor.interval")}>
                      <SelectValue placeholder={t("keyEditor.selectInterval")}>
                        {(selected: string | null) =>
                          selected === null ? (
                            t("keyEditor.selectInterval")
                          ) : (
                            <span title={intervalLabel(selected)}>{intervalLabel(selected)}</span>
                          )
                        }
                      </SelectValue>
                    </SelectTrigger>
                    <SelectContent>
                      {PREDEFINED_INTERVALS.map((interval) => (
                        <SelectItem key={interval} value={interval} title={intervalLabel(interval)}>
                          {intervalLabel(interval)}
                        </SelectItem>
                      ))}
                      <SelectItem value="custom" title={intervalLabel("custom")}>
                        {intervalLabel("custom")}
                      </SelectItem>
                    </SelectContent>
                  </Select>

                  {showCustomInput && (
                    <div className="space-y-1">
                      <Input
                        value={customInterval}
                        onChange={handleCustomIntervalChange}
                        aria-label={t("keyEditor.custom")}
                        placeholder={t("keyEditor.customPlaceholder")}
                      />
                      <div className="text-xs text-muted-foreground">{t("keyEditor.formats")}</div>
                    </div>
                  )}
                </div>
              </div>
            )}
          </div>

          {!rotationSupported && (
            <p id={`${durationId}-rotation-unavailable`} className="text-sm text-muted-foreground">
              {t("keyEditor.rotationUnavailable")}
            </p>
          )}
          {rotationSupported && autoRotationEnabled && (
            <div className="rounded-md bg-info/10 p-3 text-sm text-info">{t("keyEditor.rotationNotice")}</div>
          )}
        </div>
      </div>
    </TooltipProvider>
  );
};

export default KeyLifecycleSettings;

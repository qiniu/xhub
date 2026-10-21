import React, { useState } from "react";
import { CopyToClipboard } from "react-copy-to-clipboard";
import { Button } from "@/components/ui/button";
import { toast } from "@/lib/toast";
import { useT } from "@/i18n";

interface CreatedKeyDisplayProps {
  apiKey: string;
}

/** 展示新建密钥及一次性保存提示；apiKey 为后台原始密钥，不参与翻译。供密钥创建流程使用，复制后短暂显示成功状态。 */
const CreatedKeyDisplay: React.FC<CreatedKeyDisplayProps> = ({ apiKey }) => {
  const t = useT();
  const [copied, setCopied] = useState(false);

  /** 复制完成后更新按钮和通知；无参数或返回值，2 秒后恢复初始状态。 */
  const handleCopy = () => {
    setCopied(true);
    toast.success(t("Key copied to clipboard"));
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <div>
      <p className="mb-2">
        {t("createdKey.warning")}
      </p>

      <p className="text-sm text-muted-foreground mt-3 mb-1">{t("Virtual Key:")}</p>
      <div className="bg-muted rounded-md p-2.5 mb-2.5">
        <pre className="m-0 whitespace-normal break-words text-foreground">{apiKey}</pre>
      </div>

      <CopyToClipboard text={apiKey} onCopy={handleCopy}>
        <Button className="mt-3">{copied ? t("Copied!") : t("Copy Virtual Key")}</Button>
      </CopyToClipboard>
    </div>
  );
};

export default CreatedKeyDisplay;

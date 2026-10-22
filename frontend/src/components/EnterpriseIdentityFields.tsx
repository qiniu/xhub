"use client";

import { useEffect, useState, useId } from "react";
import { useT } from "@/i18n";
import { listSSO, type SSOConfiguration } from "@/lib/http/enterprise";
import { Field, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

/** 企业身份选择控件的状态：加载真实 SSO 连接列表，错误时明确提示但不阻止普通用户创建。 */
export function useEnterpriseConnections(): { connections: SSOConfiguration[]; error: boolean; loading: boolean } {
  const [connections, setConnections] = useState<SSOConfiguration[]>([]);
  const [error, setError] = useState(false);
  const [loading, setLoading] = useState(true);
  useEffect(() => {
    let alive = true;
    listSSO().then((rows) => { if (alive) setConnections(rows); }).catch(() => { if (alive) setError(true); }).finally(() => { if (alive) setLoading(false); });
    return () => { alive = false; };
  }, []);
  return { connections, error, loading };
}

/** 显示平台管理员管理的 SSO 连接和外部账号 ID；变更仅由父表单提交，组件本身无写入副作用。
 * connections 为真实连接列表，managed 时只读；两项值与 onChange 由新建/编辑表单持有。
 */
export function EnterpriseIdentityFields({
  connections, connectionID, externalID, onConnectionChange, onExternalChange, managed = false, loading = false, error = false,
}: {
  connections: SSOConfiguration[];
  connectionID: string;
  externalID: string;
  onConnectionChange: (value: string) => void;
  onExternalChange: (value: string) => void;
  managed?: boolean;
  loading?: boolean;
  error?: boolean;
}) {
  const t = useT();
  const id = useId();
  return <section aria-label={t("enterpriseUserIdentity.title")} className="space-y-3 rounded-md border p-4">
    <div>
      <h3 className="font-medium">{t("enterpriseUserIdentity.title")}</h3>
      <p className="text-sm text-muted-foreground">{t(managed ? "enterpriseUserIdentity.managed" : "enterpriseUserIdentity.help")}</p>
      {error && <p role="alert" className="text-sm text-destructive">{t("enterpriseUserIdentity.loadError")}</p>}
    </div>
    <Field>
      <FieldLabel htmlFor={id + "-connection"}>{t("enterpriseUserIdentity.connection")}</FieldLabel>
      <Select value={connectionID || "none"} onValueChange={(value) => onConnectionChange(value === "none" ? "" : value ?? "")} disabled={managed || loading || error}>
        <SelectTrigger id={id + "-connection"} aria-label={t("enterpriseUserIdentity.connection")}>
          <SelectValue placeholder={t(loading ? "enterpriseUserIdentity.loading" : "enterpriseUserIdentity.unbound")} />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="none">{t("enterpriseUserIdentity.unbound")}</SelectItem>
          {connections.map((connection) => <SelectItem key={connection.connection_id} value={connection.connection_id}>{connection.enabled ? connection.name : t("enterpriseUserIdentity.disabledConnection", { name: connection.name })}</SelectItem>)}
          {connectionID && !connections.some((connection) => connection.connection_id === connectionID) && <SelectItem value={connectionID}>{connectionID}</SelectItem>}
        </SelectContent>
      </Select>
    </Field>
    <Field>
      <FieldLabel htmlFor={id + "-external"}>{t("enterpriseUserIdentity.externalID")}</FieldLabel>
      <Input id={id + "-external"} value={externalID} onChange={(event) => onExternalChange(event.target.value)} disabled={managed || !connectionID || loading || error} autoComplete="off" />
    </Field>
  </section>;
}

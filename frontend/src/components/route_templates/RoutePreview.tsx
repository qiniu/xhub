"use client";

import React, { useEffect, useRef, useState } from "react";
import { useT } from "@/i18n";
import { apiClient } from "../networking";
import { Button } from "@/components/ui/button";

/** 预览安全投影只接收部署和选路事实，不接收密钥或上游地址。 */
export type RoutePreviewResult = {
  source: string;
  rule_source: string;
  routing_strategy: string;
  data: {
    deployment_id: string;
    connection: string;
    supplier: string;
    upstream_model: string;
    protocol: string;
    excluded_reason?: string;
    weight?: number;
    target_share?: number;
  }[];
};

/**
 * RoutePreview 将当前草稿交给后端真实规则解析和候选筛选。
 * 参数为令牌、公开模型、草稿及模板身份；返回只读预览 UI。
 * 模板编辑页调用；不保存草稿、不调用供应商；草稿或入口变化后清除过期结果。
 */
export default function RoutePreview({
  accessToken,
  models,
  body,
  templateId,
  organizationId,
  teamId,
}: {
  accessToken: string | null;
  models: string[];
  body: Record<string, unknown> | null;
  templateId?: string;
  organizationId?: string;
  teamId?: string;
}) {
  const t = useT();
  const lastRevision = useRef(0);
  const [entries, setEntries] = useState<{ id: string; label: string }[]>([]);
  const [model, setModel] = useState("");
  const [endpoint, setEndpoint] = useState("chat");
  const [result, setResult] = useState<RoutePreviewResult | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [revision, setRevision] = useState(0);
  const serialized = JSON.stringify(body);
  useEffect(() => {
    if (!accessToken) return;
    let canceled = false;
    apiClient
      .get<{ endpoint_types?: { id: string; label: string }[] }>("/public/endpoints")
      .then((data) => {
        if (!canceled) setEntries(data.endpoint_types ?? []);
      })
      .catch(() => {
        if (!canceled) setError("endpointsError");
      });
    return () => {
      canceled = true;
    };
  }, [accessToken]);
  useEffect(() => {
    setBusy(false);
    setResult(null);
    setError("");
  }, [serialized, model, endpoint, templateId, organizationId, teamId, accessToken]);
  useEffect(() => {
    if (lastRevision.current === revision) return;
    lastRevision.current = revision;
    if (!accessToken || !body || !models.includes(model)) return;
    let canceled = false;
    setBusy(true);
    setError("");
    apiClient
      .post<RoutePreviewResult>("/route_template/preview", {
        accessToken,
        body: {
          model_name: model,
          endpoint_id: endpoint,
          body,
          template_id: templateId,
          organization_id: organizationId,
          team_id: teamId,
        },
      })
      .then((data) => {
        if (!canceled) setResult(data);
      })
      .catch(() => {
        if (!canceled) setError("requestError");
      })
      .finally(() => {
        if (!canceled) setBusy(false);
      });
    return () => {
      canceled = true;
    };
    // 预览由按钮触发；变化会取消旧请求，只有再次点击才发送新草稿。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [revision, serialized, model, endpoint, templateId, organizationId, teamId, accessToken]);
  return (
    <section aria-label={t("pages.routeTemplates.preview.region")} className="space-y-3 rounded-md border p-3">
      <p className="text-sm font-medium">{t("pages.routeTemplates.preview.title")}</p>
      <p className="text-xs text-muted-foreground">{t("pages.routeTemplates.preview.hint")}</p>
      <div className="grid min-w-0 gap-2">
        <label className="grid min-w-0 gap-1">
          {t("pages.routeTemplates.preview.model")}
          <select
            className="min-w-0 w-full rounded-md border bg-background p-2"
            aria-label={t("pages.routeTemplates.preview.model")}
            value={model}
            onChange={(e) => setModel(e.target.value)}
          >
            <option value="">{t("pages.routeTemplates.preview.selectModel")}</option>
            {models.map((name) => (
              <option key={name} value={name}>
                {name}
              </option>
            ))}
          </select>
        </label>
        <label className="grid min-w-0 gap-1">
          {t("pages.routeTemplates.preview.endpoint")}
          <select
            className="min-w-0 w-full rounded-md border bg-background p-2"
            aria-label={t("pages.routeTemplates.preview.endpoint")}
            value={endpoint}
            onChange={(e) => setEndpoint(e.target.value)}
          >
            {entries.map((entry) => (
              <option key={entry.id} value={entry.id}>
                {entry.label}
              </option>
            ))}
          </select>
        </label>
        <Button
          type="button"
          variant="outline"
          disabled={
            busy || !body || !accessToken || !models.includes(model) || !entries.some((entry) => entry.id === endpoint)
          }
          onClick={() => setRevision((value) => value + 1)}
        >
          {t(busy ? "pages.routeTemplates.preview.busy" : "pages.routeTemplates.preview.run")}
        </Button>
      </div>
      {error && <p role="alert">{t(`pages.routeTemplates.preview.${error}`)}</p>}
      {result && (
        <div className="break-all">
          <p>
            {t("pages.routeTemplates.preview.summary", {
              source: result.source,
              rule: result.rule_source,
              strategy: result.routing_strategy,
            })}
          </p>
          {result.data.length === 0 && <p>{t("pages.routeTemplates.preview.empty")}</p>}
          <ul>
            {result.data.map((row) => (
              <li key={row.deployment_id} className="border-t py-2 text-sm">
                <p>
                  {row.connection || row.supplier} · {row.upstream_model} · {row.protocol} · {row.deployment_id}
                </p>
                {row.excluded_reason ? (
                  <p>{t("pages.routeTemplates.preview.excluded", { reason: row.excluded_reason })}</p>
                ) : (
                  <p>
                    {t("pages.routeTemplates.preview.candidate")}
                    {row.weight !== undefined && (
                      <span> · {t("pages.routeTemplates.preview.weight", { value: row.weight })}</span>
                    )}
                    {row.target_share !== undefined && (
                      <span>
                        {" "}
                        · {t("pages.routeTemplates.preview.share", { value: (row.target_share * 100).toFixed(1) })}
                      </span>
                    )}
                  </p>
                )}
              </li>
            ))}
          </ul>
        </div>
      )}
    </section>
  );
}

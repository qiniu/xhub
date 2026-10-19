import { z } from "zod/v4";
import { t } from "@/i18n";

const isBlank = (value: string): boolean => value.trim() === "";

const isJsonObject = (value: string): boolean => {
  try {
    const parsed: unknown = JSON.parse(value);
    return typeof parsed === "object" && parsed !== null && !Array.isArray(parsed);
  } catch {
    return false;
  }
};

const wholeNumberOrEmpty = z
  .string()
  .refine((value) => isBlank(value) || (/^\d+$/.test(value.trim()) && Number(value) <= 2147483647), { error: () => t("Must be a non-negative whole number") });

const amountOrEmpty = z
  .string()
  .refine(
    (value) => isBlank(value) || (Number.isFinite(Number(value)) && Number(value) >= 0),
    { error: () => t("Must be a non-negative number") },
  );

const orgSettingsShape = {
  organization_alias: z.string().min(1, { error: () => t("Please input an organization name") }),
  models: z.array(z.string()),
  max_budget: amountOrEmpty,
  budget_duration: z.string().refine(value => ["", "24h", "7d", "30d"].includes(value), { error: () => t("organizationBudget.invalid") }),
  tpm_limit: wholeNumberOrEmpty,
  rpm_limit: wholeNumberOrEmpty,
  metadata: z.string().refine((value) => isBlank(value) || isJsonObject(value), { error: () => t("Metadata must be a valid JSON object") }),
  route_template_id: z.string(),
};

export const orgSettingsSchema = z.object(orgSettingsShape);

export type OrgSettingsFormValues = z.output<typeof orgSettingsSchema>;

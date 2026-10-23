#!/usr/bin/env node
/**
 * 统计 ESLint JSON 报告中的 error，并限制其不得超过已知基线。
 * 参数依次为报告路径和最大错误数；退出码 1 表示新增了错误，调用方用于阻断 PR。
 * 本脚本只读取报告，不修改源码，也不产生需要清理的状态。
 */
import { readFileSync } from "node:fs";

const [reportPath, maximumText] = process.argv.slice(2);
const maximum = Number.parseInt(maximumText, 10);
if (!reportPath || !Number.isInteger(maximum) || maximum < 0) {
  console.error("usage: check-eslint-error-budget.mjs REPORT MAXIMUM");
  process.exit(2);
}

const report = JSON.parse(readFileSync(reportPath, "utf8"));
const errors = report.reduce(
  (total, file) => total + file.messages.filter((message) => message.severity === 2).length,
  0,
);

console.log(`ESLint errors: ${errors} | baseline maximum: ${maximum}`);
if (errors > maximum) {
  console.error(`::error::ESLint error budget exceeded (${errors} > ${maximum}). Fix new errors; do not raise the baseline.`);
  process.exit(1);
}

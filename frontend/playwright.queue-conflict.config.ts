import { defineConfig } from "@playwright/test";
import base from "./playwright.config";

// 单独运行需要独占 Redis 的浏览器回归；沿用真实网关、UI 和隔离数据库夹具。
export default defineConfig({ ...base, testDir: "./e2e-diagnosis", testMatch: "queue-conflict.test.ts" });

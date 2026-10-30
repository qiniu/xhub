import path from "path";
import { fileURLToPath } from "url";

/** @type {import('next').NextConfig} */
const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

const nextConfig = {
  output: "standalone",
  outputFileTracingRoot: path.resolve(__dirname, ".."),
  outputFileTracingIncludes: { "/docs/[[...slug]]": ["../docs/api/**/*.md"] },
  distDir: process.env.E2E_BUILD_DIR || ".next",
  typescript: { tsconfigPath: process.env.E2E_BUILD_DIR ? "tsconfig.e2e.json" : "tsconfig.json" },
  experimental: {
    useTypeScriptCli: false,
    // 推理请求可能长时间没有首字节；覆盖框架默认的 30 秒代理超时，网关和调用方仍各自执行自己的截止时间。
    proxyTimeout: 600_000,
    // 网关允许64MiB正文；Next默认10MiB只截断不拒绝，会使Content-Length不符而挂起。
    // 留1MiB余量覆盖框架丢弃越界整块的行为，让网关先读到第64MiB+1字节并返回413。
    // 这是代理缓存上限，不提高网关准入限制；固定长度和分块上传由真实E2E验证。
    proxyClientMaxBodySize: 65 * 1024 * 1024,
  },
  compiler: {
    removeConsole: process.env.NODE_ENV === "production" ? { exclude: ["error", "warn"] } : false,
  },
  images: {
    unoptimized: true,
  },
  basePath: "",
  assetPrefix: "",
  trailingSlash: false,
  allowedDevOrigins: ["127.0.0.1", "localhost"],
  // 企业认证回调与元数据使用注册在 IdP 的固定公开路径；与 /gw 共用服务端网关源，保留 POST 正文及 Cookie。
  // 参数无；返回 Next 代理规则，不把后台源暴露给浏览器，避免测试代理可用而正式控制台回调 404。
  async rewrites() {
    const gateway = process.env.XHUB_GATEWAY_ORIGIN || "http://127.0.0.1:4000";
    return [
      { source: "/gw/:path*", destination: `${gateway}/:path*` },
      { source: "/auth/enterprise/:path*", destination: `${gateway}/auth/enterprise/:path*` },
    ];
  },
  turbopack: {
    root: __dirname,
  },
};

export default nextConfig;

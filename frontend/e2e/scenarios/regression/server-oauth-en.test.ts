/** 旧server授权真实浏览器回归；使用隔离夹具并在结束后清理。 */
import { registerServerOAuthBoundary } from "../../server-oauth-scenario";
registerServerOAuthBoundary("en");

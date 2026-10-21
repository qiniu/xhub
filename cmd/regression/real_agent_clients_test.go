package regression

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/sunqirui1987/xhub/internal/gateway/affinity"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestRealAgentClients 使用实际 Codex/Claude Code 二进制完成一次本地文件工具往返。
// 参数 t 为回归上下文；显式 XHUB_TEST_CODEX_BIN/XHUB_TEST_CLAUDE_BIN 才运行，未安装时明确跳过。
// 私有数据库和本地协议夹具隔离供应商波动；验证真实 CLI 请求/工具结果、日志及费用，不代表真实模型质量或上游兼容认证。
// CLI 收到流终态即可退出，账单可能随后提交；有界等待真实账单完成，不把异步落库时序误判为丢账。
// Claude→Responses 首轮返回密文，续轮须原样携带并复用客户端原生会话，测试不注入 X-Session-Id。
// XHUB_AGENT_LIVE=1 并提供 LIVE_KEY/BASE 时显式改用真实上游，会产生费用；默认不访问外部服务。
// CLI 禁用用户配置/外部 MCP/持久会话；普通用例关闭网页搜索，专项用例显式开启 Codex 托管搜索声明并验证显式降级。
// 仅执行固定测试目录读取命令，二进制由调用方提供，目录/schema/密钥由测试清理；不认证服务端检索工具。
func TestRealAgentClients(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("real CLI fixture process-group cleanup is validated on POSIX only")
	}
	for _, variant := range []struct {
		client, transport string
		search            bool
		standardTools     bool
	}{{"codex", "responses", false, false}, {"codex", "chat", false, false}, {"claude", "responses", false, false}, {"claude", "chat", false, false}, {"codex", "chat", true, false}, {"codex", "chat", true, true}} {
		client, transport := variant.client, variant.transport
		name := client + "/" + transport
		if variant.search {
			name += "-optional-search"
		}
		if variant.standardTools {
			name += "-standard-tools"
		}
		t.Run(name, func(t *testing.T) {
			variable := "XHUB_TEST_CODEX_BIN"
			if client == "claude" {
				variable = "XHUB_TEST_CLAUDE_BIN"
			}
			binary := os.Getenv(variable)
			if binary == "" {
				t.Skip(variable + " not configured; real CLI not exercised")
			}
			live := os.Getenv("XHUB_AGENT_LIVE") == "1"
			liveKey, liveBase := os.Getenv("XHUB_AGENT_LIVE_KEY"), strings.TrimRight(os.Getenv("XHUB_AGENT_LIVE_BASE"), "/")
			if live && (liveKey == "" || liveBase == "") {
				t.Fatal("真实供应商验收必须显式配置 XHUB_AGENT_LIVE_KEY/BASE")
			}
			directory := t.TempDir()
			sentinel := "xhub-real-agent-file-content-9317"
			file := filepath.Join(directory, "probe.txt")
			if err := os.WriteFile(file, []byte(sentinel), 0600); err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			requests := 0
			toolReturned := false
			sawSearch := false
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					w.WriteHeader(400)
					return
				}
				mu.Lock()
				defer mu.Unlock()
				requests++
				capField := "max_output_tokens"
				if transport == "chat" {
					capField = "max_completion_tokens"
				}
				capValue, _ := body[capField].(float64)
				// 软预算不注入输出上限；仅校验客户端实际提交的限制仍是合法正数。
				if _, explicit := body[capField]; explicit && (capValue <= 0 || capValue > 131072) {
					t.Errorf("真实CLI显式输出上限无效: %s=%v", capField, body[capField])
				}
				limit := 3
				if live {
					limit = 8
				}
				if requests > limit {
					t.Errorf("%s 未在 %d 次请求内完成文件工具往返", client, limit)
					w.WriteHeader(400)
					writeJSON(w, map[string]any{"error": map[string]any{"message": "agent fixture tool round trip exceeded limit"}})
					return
				}
				historyField := "input"
				if transport == "chat" {
					historyField = "messages"
				}
				raw, _ := json.Marshal(body[historyField])
				done := (strings.Contains(string(raw), "function_call_output") || strings.Contains(string(raw), "custom_tool_call_output") || strings.Contains(string(raw), `"role":"tool"`)) && strings.Contains(string(raw), sentinel)
				if done {
					toolReturned = true
					if client == "claude" && transport == "responses" && !strings.Contains(string(raw), `"encrypted_content":"test-cipher"`) {
						t.Error("实际 Claude Code 续轮丢失推理密文")
					}
				}
				if live {
					encoded, _ := json.Marshal(body)
					req, err := http.NewRequestWithContext(r.Context(), "POST", liveBase+map[string]string{"responses": "/responses", "chat": "/chat/completions"}[transport], bytes.NewReader(encoded))
					if err != nil {
						t.Error("live request construction failed")
						w.WriteHeader(502)
						return
					}
					req.Header.Set("Content-Type", "application/json")
					req.Header.Set("Authorization", "Bearer "+liveKey)
					httpClient := &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
					response, err := httpClient.Do(req)
					if err != nil {
						t.Errorf("live upstream request failed: %T", err)
						w.WriteHeader(502)
						return
					}
					defer response.Body.Close()
					w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
					w.WriteHeader(response.StatusCode)
					buffer := make([]byte, 8192)
					for {
						n, e := response.Body.Read(buffer)
						if n > 0 {
							if _, err := w.Write(buffer[:n]); err != nil {
								return
							}
							if f, ok := w.(http.Flusher); ok {
								f.Flush()
							}
						}
						if e != nil {
							if e != io.EOF {
								t.Errorf("live stream read failed: %T", e)
							}
							break
						}
					}
					return
				}
				if done {
					toolReturned = true
					if client == "claude" && (!strings.Contains(string(raw), "Inspect the file before answering.") || !strings.Contains(string(raw), map[string]string{"responses": `"type":"reasoning"`, "chat": `"reasoning_content"`}[transport])) {
						t.Error("实际 Claude Code 续轮未还原 thinking 推理项")
					}
					if transport == "chat" {
						writeChatAgentResponse(w, fmt.Sprintf("chat_cli_%d", requests), "AGENT_OK "+sentinel, "", "", body["stream"] == true)
					} else {
						writeAgentResponse(w, publicAgentResponse(fmt.Sprintf("resp_cli_%d", requests), "AGENT_OK "+sentinel, nil), body["stream"] == true)
					}
					return
				}
				toolName := ""
				argument := ""
				tools, _ := body["tools"].([]any)
				for _, value := range tools {
					tool, _ := value.(map[string]any)
					if transport == "chat" {
						tool, _ = tool["function"].(map[string]any)
					}
					name, _ := tool["name"].(string)
					if client == "codex" && transport == "chat" && (name == "functions__exec" || name == "exec") {
						toolName = name
						args, _ := json.Marshal(map[string]any{"cmd": "cat " + file, "workdir": directory, "max_output_tokens": 1000})
						// 搜索专项复现七牛的 input 包装，防止实际 CLI 回归只用理想 content 夹具蒙混通过。
						field := "content"
						if variant.search {
							field = "input"
						}
						wrapped, _ := json.Marshal(map[string]any{field: "text(await tools.exec_command(" + string(args) + "));"})
						argument = string(wrapped)
						break
					}
					if client == "codex" && (name == "exec_command" || name == "shell_command" || name == "shell") {
						toolName = name
						args := map[string]any{"cmd": "cat " + file, "workdir": directory, "max_output_tokens": 1000}
						if name == "shell_command" {
							args = map[string]any{"command": "cat " + file, "workdir": directory}
						}
						if name == "shell" {
							args = map[string]any{"command": []string{"cat", file}, "workdir": directory}
						}
						encoded, _ := json.Marshal(args)
						argument = string(encoded)
						break
					}
					if client == "claude" && name == "Read" {
						toolName = name
						encoded, _ := json.Marshal(map[string]any{"file_path": file})
						argument = string(encoded)
						break
					}
				}
				// 桌面版 Codex 将代码执行工具放在 additional_tools/namespace 中，必须保留原生自定义工具语义。
				custom := false
				if client == "codex" && toolName == "" {
					inputs, _ := body["input"].([]any)
					for _, value := range inputs {
						item, _ := value.(map[string]any)
						if item["type"] != "additional_tools" {
							continue
						}
						groups, _ := item["tools"].([]any)
						for _, value := range groups {
							group, _ := value.(map[string]any)
							if group["name"] != "functions" {
								continue
							}
							definitions, _ := group["tools"].([]any)
							for _, value := range definitions {
								tool, _ := value.(map[string]any)
								if tool["name"] == "exec" && tool["type"] == "custom" {
									toolName = "exec"
									custom = true
									args, _ := json.Marshal(map[string]any{"cmd": "cat " + file, "workdir": directory, "max_output_tokens": 1000})
									argument = "text(await tools.exec_command(" + string(args) + "));"
								}
							}
						}
					}
				}
				if toolName == "" {
					if report := os.Getenv("XHUB_AGENT_REPORT_DIR"); report != "" {
						raw, _ := json.Marshal(map[string]any{"tools": body["tools"], "input": body["input"]})
						_ = os.WriteFile(filepath.Join(report, client+"-request.json"), raw, 0600)
					}
					t.Errorf("实际 %s 请求没有预期文件工具；收到 %d 个工具", client, len(tools))
					w.WriteHeader(400)
					writeJSON(w, map[string]any{"error": map[string]any{"message": "expected file tool missing"}})
					return
				}
				call := map[string]any{"id": "fc_cli", "type": "function_call", "status": "completed", "call_id": "call_cli", "name": toolName, "arguments": argument}
				if custom {
					call = map[string]any{"id": "ctc_cli", "type": "custom_tool_call", "status": "completed", "call_id": "call_cli", "name": "exec", "namespace": "functions", "input": argument}
				}
				if transport == "chat" {
					writeChatAgentResponse(w, fmt.Sprintf("chat_cli_%d", requests), "", toolName, argument, body["stream"] == true)
				} else {
					response := publicAgentResponse(fmt.Sprintf("resp_cli_%d", requests), "", call)
					if client == "claude" {
						// 密文首轮复现 codex-auto-review 的真实行为，要求 CLI 自带会话信号足以续轮。
						response["output"].([]any)[0].(map[string]any)["encrypted_content"] = "test-cipher"
					}
					writeAgentResponse(w, response, body["stream"] == true)
				}
			}))
			t.Cleanup(upstream.Close)
			dep := chatDeployment("gpt-5.6-sol")
			dep.LiteLLMParams["api_base"] = upstream.URL + "/v1"
			dep.ModelInfo["transport"] = "bypass_openai_" + transport
			dep.ModelInfo["endpoint_types"] = []string{"chat", "responses", "messages"}
			dep.ModelInfo["id"] = "real-cli-deployment"
			// 本地供应商夹具提供完整费率；真实 CLI 经软预算准入，不额外注入默认输出上限。
			dep.ModelInfo["max_input_tokens"] = 1048576
			dep.ModelInfo["max_output_tokens"] = 131072
			dep.ModelInfo["supported_parameters"] = []string{"max_completion_tokens", "max_tokens"}
			dep.LiteLLMParams["cache_creation_input_token_cost"] = testInputRate * 1.25
			dep.LiteLLMParams["cache_read_input_token_cost"] = testInputRate * 0.1
			if variant.search {
				dep.ModelInfo["web_search_support"] = "unsupported"
				dep.ModelInfo["web_search_fallback"] = "drop_optional"
			}
			h := newHarness(t, dep)
			baseHandler := h.server.Config.Handler
			var routeMu sync.Mutex
			h.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// 串行真实CLI请求，每轮前丢弃临时状态，验证客户端自带会话信号与PG可恢复历史。
				routeMu.Lock()
				defer routeMu.Unlock()
				h.gw.State = *affinity.NewDurable(nil, h.store)
				if r.URL.Path == "/v1/messages" || (variant.search && r.URL.Path == "/v1/responses") {
					raw, _ := io.ReadAll(r.Body)
					r.Body = io.NopCloser(bytes.NewReader(raw))
					var body map[string]any
					_ = json.Unmarshal(raw, &body)
					if variant.search {
						// 记录真实 CLI 自带的搜索声明，避免测试在客户端没有发送搜索时误报降级成功。
						if report := os.Getenv("XHUB_AGENT_REPORT_DIR"); report != "" {
							_ = os.MkdirAll(report, 0700)
							_ = os.WriteFile(filepath.Join(report, "codex-search-request.json"), mustJSON(body), 0600)
						}
						mu.Lock()
						sawSearch = sawSearch || strings.Contains(string(mustJSON(body)), `"type":"web_search"`) || strings.Contains(string(mustJSON(body)), `"type":"web_search_preview"`)
						mu.Unlock()
					}
					t.Logf("Claude 桥接能力参数 context_management=%s thinking=%s output_config=%s", mustJSON(body["context_management"]), mustJSON(body["thinking"]), mustJSON(body["output_config"]))
				}
				baseHandler.ServeHTTP(w, r)
			})
			admin := h.adminSession()
			owner := h.openScope(t, admin, "real-"+client)
			h.setKeyBudget(t, admin, owner.key, 50)
			ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
			defer cancel()
			prompt := "Read " + file + " using your file or shell tool, then reply with AGENT_OK and its contents. Do not modify any files."
			var args, env []string
			if client == "codex" {
				// 离线夹具只下发固定 cat 命令，协议验收不能依赖 CI 宿主用户命名空间；真实供应商仍使用只读沙箱。
				sandbox := "danger-full-access"
				if live {
					sandbox = "read-only"
				}
				args = []string{"exec", "--ignore-user-config", "--ignore-rules", "--ephemeral", "--skip-git-repo-check", "-C", directory, "-s", sandbox, "--json", "-m", dep.ModelName, "-c", `model_provider="xhub"`, "-c", fmt.Sprintf(`model_providers.xhub={name="XHub",base_url="%s/v1",env_key="XHUB_AGENT_TEST_KEY",wire_api="responses",requires_openai_auth=false,supports_websockets=false}`, h.server.URL), "-c", `model_reasoning_effort="medium"`, "-c", `web_search="disabled"`, "-c", `apps._default.enabled=false`, "-c", `features.plugins=false`, "-c", `features.remote_plugin=false`, "-c", `analytics.enabled=false`, prompt}
				if variant.search {
					// 自定义供应商可能过滤内置目录，令未知模型失去搜索能力；用二进制自带目录建立隔离测试配置。
					// 只改临时目录中的能力声明，不修改用户配置，也不通过伪造 HTTP 请求补搜索工具。
					catalogCommand := exec.CommandContext(ctx, binary, "debug", "models", "--bundled")
					catalogRaw, catalogErr := catalogCommand.Output()
					if catalogErr != nil {
						t.Fatalf("读取 Codex 内置模型目录失败: %v", catalogErr)
					}
					var catalog map[string]any
					if err := json.Unmarshal(catalogRaw, &catalog); err != nil {
						t.Fatal(err)
					}
					entries, _ := catalog["models"].([]any)
					var entry map[string]any
					for _, value := range entries {
						candidate, _ := value.(map[string]any)
						template := dep.ModelName
						if variant.standardTools {
							template = "gpt-5.5"
						}
						if candidate["slug"] == template {
							entry = candidate
							break
						}
					}
					if entry == nil {
						t.Fatal("Codex 内置目录没有测试模型")
					}
					entry["supports_search_tool"] = true
					entry["slug"] = dep.ModelName
					entry["use_responses_lite"] = false
					catalog["models"] = []any{entry}
					catalogFile := filepath.Join(directory, "codex-search-models.json")
					if err := os.WriteFile(catalogFile, mustJSON(catalog), 0600); err != nil {
						t.Fatal(err)
					}
					args = append([]string{"exec", "-c", `features.enable_request_compression=false`, "-c", fmt.Sprintf("model_catalog_json=%q", catalogFile)}, args[1:]...)

					// 显式开启 live，验证真实客户端确实发出搜索声明；网关只降级，不执行外部搜索。
					for i := 0; i+1 < len(args); i++ {
						if args[i] == "-c" && args[i+1] == `web_search="disabled"` {
							args[i+1] = `web_search="live"`
							break
						}
					}
				}
				env = []string{"XHUB_AGENT_TEST_KEY=" + owner.key}
			} else {
				// 显式禁用用户设置来源，避免本机 settings.env 覆盖临时网关地址而误调用个人供应商。
				args = []string{"--setting-sources", "", "--bare", "--print", "--no-session-persistence", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--tools", "Read", "--allowedTools", "Read", "--max-turns", "3", "--output-format", "json", "--model", dep.ModelName, "--effort", "low", prompt}
				env = []string{"ANTHROPIC_API_KEY=" + owner.key, "ANTHROPIC_BASE_URL=" + h.server.URL, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1"}
			}
			command := exec.CommandContext(ctx, binary, args...)
			command.Dir = directory
			isolateAgentCommand(command)
			// 子进程只拿一次性网关密钥，真实上游凭据仅留在测试转发器内存，不能暴露给 Agent 工具环境。
			for _, entry := range os.Environ() {
				name, _, _ := strings.Cut(entry, "=")
				if strings.Contains(name, "API_KEY") || strings.HasPrefix(name, "XHUB_AGENT_LIVE") || strings.HasPrefix(name, "CODEX_") || name == "ANTHROPIC_AUTH_TOKEN" {
					continue
				}
				command.Env = append(command.Env, entry)
			}
			command.Env = append(command.Env, env...)
			output, err := command.CombinedOutput()
			// 只有本地夹具与一次性虚拟密钥参与；失败诊断仍删除密钥后再输出。
			safe := strings.ReplaceAll(string(output), owner.key, "[REDACTED]")
			if report := os.Getenv("XHUB_AGENT_REPORT_DIR"); report != "" {
				if err := os.MkdirAll(report, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(report, strings.ReplaceAll(name, "/", "-")+".jsonl"), []byte(safe), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err != nil {
				t.Fatalf("实际 %s CLI 失败: %v\n%s", client, err, safe)
			}
			mu.Lock()
			count, returned, searched := requests, toolReturned, sawSearch
			mu.Unlock()
			if variant.search && !searched {
				t.Fatal("Codex 没有发送开启后的 web_search 声明，未覆盖用户故障")
			}
			if count < 2 || !returned || (!strings.Contains(safe, "AGENT_OK") || !strings.Contains(safe, sentinel)) {
				t.Fatalf("%s 没有完成真实工具往返: requests=%d returned=%v\n%s", client, count, returned, safe)
			}
			h.flushSpend()
			rows := h.successRows(t, admin, dep.ModelName)
			deadline := time.Now().Add(3 * time.Second)
			for len(rows) < count && time.Now().Before(deadline) {
				// 等待本轮 HTTP 处理器完成 RecordSpend；刷新队列不能代替尚未发生的入队。
				time.Sleep(10 * time.Millisecond)
				h.flushSpend()
				rows = h.successRows(t, admin, dep.ModelName)
			}
			holds, holdErr := h.db.PendingKeyBudget(t.Context(), owner.keyID)
			if holdErr != nil || len(holds) != 0 {
				t.Fatalf("真实 CLI 软预算账单不应创建预占: %+v %v", holds, holdErr)
			}
			if len(rows) != count {
				t.Fatalf("%s CLI 调用数与成功账单不符: calls=%d rows=%d", client, count, len(rows))
			}
			if variant.search {
				for _, row := range rows {
					detail := logDetail(t, h, admin, stringField(row, "request_id"))
					meta := detail["metadata"].(map[string]any)
					diagnostic, _ := meta["upstream_response"].(map[string]any)
					if string(mustJSON(diagnostic["dropped_capabilities"])) != `["web_search"]` {
						t.Fatalf("真实 Codex 降级未记录: %v", diagnostic)
					}
				}
			}
			t.Logf("实际 %s CLI：%d 次网关请求、一次文件工具往返、%d 条账单", client, count, len(rows))
		})
	}
}

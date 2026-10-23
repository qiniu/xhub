// 本文件验证 execution 的相关执行契约，阅读顺序为测试宿主/辅助方法，再到各个 Test 场景。
// 具体前置、期望、边界和清理方式见每个测试方法；夹具的空实现仅用于隔离当前测试。
package execution

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sunqirui1987/xhub/internal/auth"
	"github.com/sunqirui1987/xhub/internal/config"
	dpcontract "github.com/sunqirui1987/xhub/internal/dataplane/contract"
	"github.com/sunqirui1987/xhub/internal/iam"
	"github.com/sunqirui1987/xhub/internal/live"
	providercontract "github.com/sunqirui1987/xhub/internal/provider/contract"
	"github.com/sunqirui1987/xhub/internal/router"
	"github.com/sunqirui1987/xhub/internal/util"
)

// logHost 是统一执行日志测试的内存宿主，提供持久于本夹具的缓存。
// 捕获每个 callID 的响应正文及最终状态，不做真实扣费；配置和上游客户端由测试指定。
type logHost struct {
	// 当前测试配置及目录。
	cfg *config.Config
	// 本地服务或内存传输客户端。
	client *http.Client
	// 同一宿主持有的响应缓存，测试可预存数据。
	cache *util.DualCache
	// 捕获的调用 ID 与最终状态列表。
	spends []capturedSpend
	// 按 callID 复制的响应正文，不保存原请求正文。
	exchanges map[string][]byte
	// 捕获执行时的公开上游快照，无数据库副作用。
	upstreamNotes map[string]dpcontract.CallNote
}

// capturedSpend 保存日志宿主捕获的一次记账调用 ID 与状态，用来验证失败路径也经过记账。
type capturedSpend struct {
	// 独立 HTTP 调用标识，关联本轮响应暂存。
	callID string
	// 执行器提交记账的最终状态，可能与已开始 SSE 的客户端状态不同。
	status int
}

// newLogHost 初始化独立的日志测试宿主，供统一执行测试捕获响应与记账。
// 参数 cfg 为模型配置，client 为本地或内存 HTTP 客户端，nil 时使用 http.DefaultClient；返回宿主指针。
// 新建缓存和交互映射，不打开数据库；是否拨号由后续 Serve 和传入地址决定。
func newLogHost(cfg *config.Config, client *http.Client) *logHost {
	if client == nil {
		client = http.DefaultClient
	}
	return &logHost{
		cfg:           cfg,
		client:        client,
		cache:         util.NewCache(),
		exchanges:     map[string][]byte{},
		upstreamNotes: map[string]dpcontract.CallNote{},
	}
}

// RequireLLMPrincipal 是 logHost 的测试接口实现。
// 忽略测试请求中的凭据，返回固定会话身份；参数 w/r 仅满足接口，不写拒绝响应，不查询真实身份库。 固定用户为 log-user。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logHost) RequireLLMPrincipal(http.ResponseWriter, *http.Request) *auth.Principal {
	return &auth.Principal{Kind: "session", UserID: "log-user"}
}

// ResolveRequest 是 logHost 的测试接口实现。
// 在测试重试中返回相同固定会话身份及 nil 错误；参数请求不参与鉴权，不能验证生产密钥撤销逻辑。 固定用户为 log-user。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logHost) ResolveRequest(*http.Request) (*auth.Principal, error) {
	return &auth.Principal{Kind: "session", UserID: "log-user"}, nil
}

// RouteSettingsFor 是 logHost 的测试接口实现。
// 忽略身份参数并返回 BuiltinSettings；无外部查询，测试不依赖进程 cfg.RouterSettings 的旧字段。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logHost) RouteSettingsFor(*auth.Principal) router.RouteSettings {
	return router.BuiltinSettings()
}

// GatewayConfig 是 logHost 的测试接口实现。
// 返回本夹具的 cfg 指针；无参数，供执行入口读取目录，不复制配置，不产生外部副作用。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logHost) GatewayConfig() *config.Config { return h.cfg }

// HTTPClient 是 logHost 的测试接口实现。
// 返回本夹具指定的 client 指针；无参数，由执行器调用测试上游，方法本身不拨号。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logHost) HTTPClient() *http.Client { return h.client }

// ResponseCache 是 logHost 的测试接口实现。
// 返回 h.cache；无参数，同一宿主中持续保留数据，供预存响应和命中测试使用，不连接 Redis。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logHost) ResponseCache() *util.DualCache { return h.cache }

// RouteState 是 logHost 的测试接口实现。
// 返回测试路由运行快照；无参数，供调度器读取，不读取真实并发或 Redis 状态。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logHost) RouteState() router.State { return router.State{} }

// GuardrailBlocks 是 logHost 的测试接口实现。
// 接受但忽略调用 ID 和请求正文，返回 false 及空消息，保证本测试只关注执行链；不验证生产护栏。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logHost) GuardrailBlocks(string, map[string]any) (bool, string) {
	return false, ""
}

// AttachCredential 是 logHost 的测试接口实现。
// 原样返回参数 dep 和 nil 错误，供已含假凭据的测试部署执行；不连接凭据库或修改配置。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logHost) AttachCredential(dep config.ModelEntry) (config.ModelEntry, error) { return dep, nil }

// IncBusy 是 logHost 的测试接口实现。
// 接受部署运行 ID 但不修改并发状态；无返回，仅满足执行接口，测试不验证真实负载调度。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logHost) IncBusy(string) {}

// DecBusy 是 logHost 的测试接口实现。
// 接受部署运行 ID 但不修改并发状态；无返回，与空 IncBusy 对应，不释放外部资源。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logHost) DecBusy(string) {}

// NoteFailure 是 logHost 的测试接口实现。
// 处理执行器传入的运行部署 ID 与请求设置；无返回，测试宿主不更新真实 Redis 冷却。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logHost) NoteFailure(string, router.RouteSettings) {}

// NoteLatency 是 logHost 的测试接口实现。
// 忽略运行部署 ID 和毫秒值；无返回，测试不记录真实延迟统计。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logHost) NoteLatency(string, float64) {}

// SetChatHeaders 是 logHost 的测试接口实现。
// 忽略写入器、身份、公开模型及上游地址；无返回，不在测试响应上添加生产诊断头。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logHost) SetChatHeaders(http.ResponseWriter, *auth.Principal, string, string) {
}

// RecordSpend 是 logHost 的测试接口实现。
// 将 callID 和 status 追加到 spends，忽略其余计费参数；无返回，用于断言终止失败也被记录，不扣余额。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logHost) RecordSpend(_ http.ResponseWriter, _ *auth.Principal, callID, _ string, _ string, _ map[string]any, _ time.Time, _ bool, status int, _ string) {
	h.spends = append(h.spends, capturedSpend{callID: callID, status: status})
}

// RememberExchange 是 logHost 的测试接口实现。
// 把 response 复制到 exchanges[callID]，其他参数忽略；无返回，复制防止执行器复用缓冲后改变断言，不保存请求正文。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logHost) RememberExchange(callID string, _ *http.Request, _ []byte, response []byte) {
	h.exchanges[callID] = append([]byte(nil), response...)
}

// PlanRoute 是 logHost 的测试接口实现。
// 忽略请求、模型、正文和身份，返回零值 RoutePlan，供无会话偏好的执行测试；不读缓存或授权历史。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logHost) PlanRoute(*http.Request, string, map[string]any, *auth.Principal) dpcontract.RoutePlan {
	return dpcontract.RoutePlan{}
}

// CommitRoute 是 logHost 的测试接口实现。
// 忽略路由计划、稳定部署 ID 和响应 ID；无返回，不保存会话偏好或父响应归属。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logHost) CommitRoute(dpcontract.RoutePlan, string, string) {}

// AnnotateCall 是 logHost 的测试接口实现。
// 参数为调用 ID 和附注；仅捕获已选择部署的公开快照，无返回、不模拟业务附注合并或外部写入。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logHost) AnnotateCall(id string, note dpcontract.CallNote) {
	if note.UpstreamSelected {
		h.upstreamNotes[id] = note
	}
}

// ReleaseCall 满足日志捕获宿主的请求清理契约；参数为本次 ID，无返回。
// 本夹具保存的是事后断言副本，不模拟生产暂存，因此不删除捕获数据，也不连接外部存储。
func (h *logHost) ReleaseCall(string) {}

// FindDeployment 是 logHost 的测试接口实现。
// 忽略部署 ID，恒返回零部署与 false；测试不恢复原生任务归属。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logHost) FindDeployment(string) (config.ModelEntry, bool) { return config.ModelEntry{}, false }

// PinOfficial 是 logHost 的测试接口实现。
// 忽略任务范围键和部署 ID；无返回，不登记原生任务，该能力仅为接口兼容。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logHost) PinOfficial(string, string) {}

// OfficialDeployment 是 logHost 的测试接口实现。
// 忽略任务范围键并返回空串；无归属状态，不查询上游。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logHost) OfficialDeployment(string) string { return "" }

// WriteCacheHit 是 logHost 的测试接口实现。
// 接受响应、身份、调用/模型/缓存/操作标识、缓存正文和开始时间，但不输出或结算；无返回，只满足接口。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logHost) WriteCacheHit(http.ResponseWriter, *auth.Principal, string, string, string, string, []byte, time.Time) {
}

// WriteChatJSON 是 logHost 的测试接口实现。
// 接受统一响应及记账参数（含可变 models 授权范围）并全部忽略；本基础夹具不输出或结算。
// 无返回，具体测试需要写出时另行覆写。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logHost) WriteChatJSON(http.ResponseWriter, *auth.Principal, string, string, string, string, string, []byte, int, time.Time, string, ...string) {
}

// EnforceIdentityLimits 是 logHost 的测试接口实现。
// 忽略写入器、路径、身份、模型和估算值，恒返回 true；不验证真实模型授权、预算或 RPM/TPM。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logHost) EnforceIdentityLimits(http.ResponseWriter, string, *auth.Principal, string, int) bool {
	return true
}

// Redis 是 logHost 的测试接口实现。
// 返回 nil 热状态客户端；无参数，保证测试不连接 Redis，相关生产路径会按缺配置降级。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logHost) Redis() *live.Client { return nil }

// Models 是 logHost 的测试接口实现。
// 返回 h.cfg.ModelList；无参数，不复制配置，用于接口能力检查，测试须提供非 nil cfg。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logHost) Models() []config.ModelEntry { return h.cfg.ModelList }

// BusyMap 是 logHost 的测试接口实现。
// 返回新建的空并发映射；无参数，表示测试没有正在占用的部署，不读取生产计数。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logHost) BusyMap() map[string]int { return map[string]int{} }

// Identity 是 logHost 的测试接口实现。
// 返回 nil 身份数据库；无参数，保证测试不产生数据库写入。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logHost) Identity() *iam.DB { return nil }

// chatConfig 将测试部署登记为显式 OpenAI Chat 传输，供 Serve 的日志与回退测试使用。
// 参数 entries 为测试 ModelEntry 列表；返回含这些部署及基础路由设置的 Config。
// 会原地修改各 entry 的 ModelInfo/LiteLLMParams 映射，缺省供应商标签填 custom-test；调用方不得复用为只读生产配置。
func chatConfig(entries ...config.ModelEntry) *config.Config {
	for i := range entries {
		if entries[i].ModelInfo == nil {
			entries[i].ModelInfo = map[string]any{}
		}
		entries[i].ModelInfo["transport"] = "bypass_openai_chat"
		if entries[i].ParamString("custom_llm_provider", "") == "" {
			entries[i].LiteLLMParams["custom_llm_provider"] = "custom-test"
		}
		entries[i].ModelInfo["endpoint_types"] = []string{"chat"}
	}
	return &config.Config{
		ModelList: entries,
	}
}

// chatRequest 构造统一 Chat 入站请求，供 Serve 单测使用。
// 参数 t 为测试上下文，model 为公开模型，stream 控制是否携带流标志；返回 httptest 请求，正文使用英文 hi。
// 编码失败 t.Fatal；Authorization 使用固定假凭据，只创建内存请求，不访问上游或创建数据。
func chatRequest(t *testing.T, model string, stream bool) *http.Request {
	t.Helper()
	payload := map[string]any{
		"model":    model,
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	}
	if stream {
		payload["stream"] = true
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer sk-local-master")
	return req
}

// captureLog 把进程标准日志暂时重定向到内存，供测试断言安全诊断文本。
// 参数 t 为测试上下文；返回可读缓冲区，t.Cleanup 恢复之前的日志 writer。
// 修改进程全局状态，因此使用此帮助方法的测试不能并行争用日志输出；不写磁盘。
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	return &buf
}

// assertNoSecrets 检查当前日志文本是否包含测试约定的密钥哨兵或 Bearer 字样。
// 参数 t 为测试上下文、line 为待检查文本；无返回，命中时 t.Fatalf 指明日志泄露。
// 供执行失败日志测试调用，只覆盖已知哨兵，不证明任意形式的秘密都已被过滤；无外部副作用。
func assertNoSecrets(t *testing.T, line string) {
	t.Helper()
	if strings.Contains(line, "sk-local-master") || strings.Contains(line, "Bearer") || strings.Contains(line, "sk-") {
		t.Fatalf("log leaked credentials: %s", line)
	}
}

// TestServePersistsCompleteUpstreamFailure 验证重试耗尽后保留上游完整 500 正文。
// 前置条件是本地假上游固定失败；结果应记录客户端 500 和完整错误字段；本地服务由 Cleanup 关闭。
// 参数 t 为 Go 测试上下文；返回值无，断言失败时终止当前测试。
func TestServePersistsCompleteUpstreamFailure(t *testing.T) {
	rawError := []byte("{\"error\":{\"message\":\"complete upstream failure\",\"detail\":\"原始正文\"}}")
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write(rawError)
	}))
	t.Cleanup(up.Close)
	h := newLogHost(chatConfig(config.ModelEntry{ModelName: "failed-model", LiteLLMParams: map[string]any{
		"model": "openai/failed-model", "api_key": "test-key", "api_base": up.URL + "/v1",
	}}), up.Client())
	rec := httptest.NewRecorder()
	Serve(h, rec, chatRequest(t, "failed-model", false), "chat")
	if rec.Code != http.StatusInternalServerError || len(h.spends) != 1 || h.spends[0].status != http.StatusInternalServerError {
		t.Fatalf("失败日志状态不完整: http=%d spends=%+v", rec.Code, h.spends)
	}
	if note := h.upstreamNotes[h.spends[0].callID]; note.APIBase != up.URL+"/v1" || note.Supplier != up.URL+"/v1" || note.UpstreamModel != "openai/failed-model" {
		t.Fatalf("重试耗尽丢失上游快照: %+v", note)
	}
	if got := h.exchanges[h.spends[0].callID]; !bytes.Equal(got, rawError) {
		t.Fatalf("上游错误正文被改写: got=%q want=%q", got, rawError)
	}
}

// TestServePersistsNetworkFailure 验证普通数据面网络错误也写入 502 日志。
// 前置条件是自定义 RoundTripper 返回固定错误；结果应保存安全错误文本；不访问外网且无需清理数据。
// 参数 t 为 Go 测试上下文；返回值无，断言失败时终止当前测试。
func TestServePersistsNetworkFailure(t *testing.T) {
	client := &http.Client{Transport: roundTripError{err: errors.New("dial tcp: connection refused")}}
	h := newLogHost(chatConfig(config.ModelEntry{ModelName: "network-model", LiteLLMParams: map[string]any{
		"model": "openai/network-model", "api_key": "test-key", "api_base": "https://example.invalid/v1",
	}}), client)
	rec := httptest.NewRecorder()
	Serve(h, rec, chatRequest(t, "network-model", false), "chat")
	if rec.Code != http.StatusBadGateway || len(h.spends) != 1 {
		t.Fatalf("网络失败未形成一条日志: http=%d spends=%+v", rec.Code, h.spends)
	}
	if note := h.upstreamNotes[h.spends[0].callID]; note.APIBase != "https://example.invalid/v1" || !note.UpstreamSelected {
		t.Fatalf("网络失败丢失上游快照: %+v", note)
	}
	if got := string(h.exchanges[h.spends[0].callID]); !strings.Contains(got, "connection refused") {
		t.Fatalf("网络错误正文缺失: %q", got)
	}
}

// roundTripError 是始终返回预设网络错误的 HTTP 传输夹具；不拨号，用于确定性失败测试。
type roundTripError struct {
	// RoundTrip 直接返回的预设错误，不触发网络请求。
	err error
}

// RoundTrip 返回固定网络错误；参数请求仅用于满足接口，响应始终为空。
// 供本包测试调用，不创建外部数据；资源由所属测试管理。
func (r roundTripError) RoundTrip(*http.Request) (*http.Response, error) { return nil, r.err }

// TestServeLogsBuildSkipAndTerminalAuth 验证任意供应商按显式协议调用且型号原样传递。
// 参数 t：测试上下文；本地上游检查请求及日志，不依赖外网，服务和日志 writer 自动恢复。
// 测试只操作内存配置或本地假上游，HTTP 服务及正文由本例关闭，不创建外部付费任务或持久化数据。
func TestServeLogsBuildSkipAndTerminalAuth(t *testing.T) {
	buf := captureLog(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var doc map[string]any
		_ = json.NewDecoder(r.Body).Decode(&doc)
		if doc["model"] != "base_llm/some-model" {
			t.Errorf("上游型号被改写: %v", doc)
		}
		io.WriteString(w, `{"id":"local","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	defer up.Close()
	h := newLogHost(chatConfig(config.ModelEntry{ModelName: "custom", LiteLLMParams: map[string]any{"model": "base_llm/some-model", "api_key": "sk-local-master", "api_base": up.URL}}), up.Client())
	rec := httptest.NewRecorder()
	Serve(h, rec, chatRequest(t, "custom", false), "chat")
	if rec.Code != 200 {
		t.Fatalf("自定义供应商调用失败: %d %s", rec.Code, rec.Body.String())
	}
	assertNoSecrets(t, buf.String())
}

// TestServeLogsMissingCredential 验证缺失上游凭据时不拨号，返回明确错误并记录安全诊断。
// 参数 t 为 Go 测试上下文；返回值无，断言失败定位具体协议、路由或日志行为。
// 前置使用独立测试配置及内存传输/纯函数输入，不依赖外部供应商凭据。
// 全局日志 writer 由 captureLog 的 Cleanup 恢复，测试状态只存在本例内存，不产生持久化数据。
func TestServeLogsMissingCredential(t *testing.T) {
	buf := captureLog(t)
	h := newLogHost(chatConfig(config.ModelEntry{
		ModelName: "gpt-4o-mini",
		LiteLLMParams: map[string]any{
			"model":    "openai/gpt-4o-mini",
			"api_base": "https://api.openai.com/v1",
		},
	}), nil)
	rec := httptest.NewRecorder()
	Serve(h, rec, chatRequest(t, "gpt-4o-mini", false), "chat")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	line := buf.String()
	t.Log(line)
	for _, want := range []string{
		"trace dataplane hop path=/v1/chat/completions",
		"trace process path=/v1/chat/completions step=start op=chat",
		"debug process path=/v1/chat/completions step=auth ok=true kind=session",
		"debug process path=/v1/chat/completions step=route model=gpt-4o-mini deployments=1 stream=false",
		"debug skip deployment path=/v1/chat/completions provider=custom-test model=openai/gpt-4o-mini reason=missing api key or api base",
		"error dataplane path=/v1/chat/completions status=401 code=authentication_error provider=custom-test",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("log missing %q\n%s", want, line)
		}
	}
	assertNoSecrets(t, line)
}

// TestServeLogsUnregisteredTransport 验证未注册的显式 transport 在联网前被拒绝。
// 参数 t：测试上下文；供应商名称只是用户数据，不参与能力判断；无外部调用，日志 writer 由 cleanup 恢复。
func TestServeLogsUnregisteredTransport(t *testing.T) {
	buf := captureLog(t)
	cfg := chatConfig(config.ModelEntry{ModelName: "ghost", LiteLLMParams: map[string]any{"model": "openai/gpt", "api_key": "sk-local-master", "api_base": "https://example.invalid"}})
	cfg.ModelList[0].ModelInfo["transport"] = "unregistered_transport"
	rec := httptest.NewRecorder()
	Serve(newLogHost(cfg, nil), rec, chatRequest(t, "ghost", false), "chat")
	if rec.Code != 400 {
		t.Fatalf("未注册 transport 未被拒绝: %d %s", rec.Code, rec.Body.String())
	}
	assertNoSecrets(t, buf.String())
}

// TestServeLogsEmptyStreamAndUpstreamStatus 验证未完成空流和上游 500 返回网关失败，日志保留原因而不泄露完整地址及假密钥。
// 参数 t 为 Go 测试上下文；返回值无，断言失败定位具体协议、路由或日志行为。
// 前置使用独立测试配置及本地 HTTP 假上游，不依赖外部供应商凭据。
// 服务器通过 defer/Cleanup 关闭，全局日志 writer 由 captureLog 的 Cleanup 恢复，测试状态只存在本例内存，不产生持久化数据。
func TestServeLogsEmptyStreamAndUpstreamStatus(t *testing.T) {
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer empty.Close()
	failed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":"sk-local-master Bearer sk-local-master"}`)
	}))
	defer failed.Close()

	t.Run("empty stream", func(t *testing.T) {
		buf := captureLog(t)
		h := newLogHost(chatConfig(config.ModelEntry{
			ModelName: "gpt-4o-mini",
			LiteLLMParams: map[string]any{
				"model":    "openai/gpt-4o-mini",
				"api_key":  "sk-local-master",
				"api_base": empty.URL + "/v1",
			},
		}), empty.Client())
		rec := httptest.NewRecorder()
		Serve(h, rec, chatRequest(t, "gpt-4o-mini", true), "chat")
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
		}
		line := buf.String()
		t.Log(line)
		for _, want := range []string{
			"trace dataplane hop path=/v1/chat/completions",
			"error upstream stream path=/v1/chat/completions provider=custom-test model=openai/gpt-4o-mini",
			"err=upstream stream ended without completion",
			"error dataplane path=/v1/chat/completions status=502 code=upstream_error detail=upstream stream ended without completion",
		} {
			if !strings.Contains(line, want) {
				t.Fatalf("log missing %q\n%s", want, line)
			}
		}
		if strings.Contains(line, empty.URL) {
			t.Fatalf("log included full upstream url: %s", line)
		}
		assertNoSecrets(t, line)
	})

	t.Run("upstream 500", func(t *testing.T) {
		buf := captureLog(t)
		h := newLogHost(chatConfig(config.ModelEntry{
			ModelName: "gpt-4o-mini",
			LiteLLMParams: map[string]any{
				"model":    "openai/gpt-4o-mini",
				"api_key":  "sk-local-master",
				"api_base": failed.URL + "/v1",
			},
		}), failed.Client())
		rec := httptest.NewRecorder()
		Serve(h, rec, chatRequest(t, "gpt-4o-mini", false), "chat")
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
		}
		line := buf.String()
		t.Log(line)
		for _, want := range []string{
			"trace dataplane hop path=/v1/chat/completions",
			"error upstream status path=/v1/chat/completions provider=custom-test model=openai/gpt-4o-mini",
			"status=500",
			"error dataplane path=/v1/chat/completions status=500 code=upstream_error detail=upstream 500",
		} {
			if !strings.Contains(line, want) {
				t.Fatalf("log missing %q\n%s", want, line)
			}
		}
		if strings.Contains(line, failed.URL) {
			t.Fatalf("log included full upstream url: %s", line)
		}
		assertNoSecrets(t, line)
	})
}

// TestServeLogsCacheHitAndStreamMetrics 验证预存响应命中与本地成功 SSE 的 token、首字及速率日志，缓存速率保持零。
// 参数 t 为 Go 测试上下文；返回值无，断言失败定位具体协议、路由或日志行为。
// 前置使用独立测试配置及本地 HTTP 假上游，不依赖外部供应商凭据。
// 服务器通过 defer/Cleanup 关闭，全局日志 writer 由 captureLog 的 Cleanup 恢复，测试状态只存在本例内存，不产生持久化数据。
func TestServeLogsCacheHitAndStreamMetrics(t *testing.T) {
	raw := []byte(`{"messages":[{"content":"hi","role":"user"}],"model":"gpt-4o-mini"}`)
	h := newLogHost(chatConfig(config.ModelEntry{
		ModelName: "gpt-4o-mini",
		LiteLLMParams: map[string]any{
			"model":    "openai/gpt-4o-mini",
			"api_key":  "test-key",
			"api_base": "https://api.openai.com/v1",
		},
	}), nil)
	scope, err := json.Marshal(map[string]any{"models": h.cfg.ModelList, "router": h.RouteSettingsFor(nil).Settings, "model_defaults": h.RouteSettingsFor(nil).ModelDefaults, "session": "", "query": "", "routing_groups": h.RouteSettingsFor(nil).RoutingGroups, "fallbacks": h.RouteSettingsFor(nil).ModelFallbacks})
	if err != nil {
		t.Fatal(err)
	}
	h.cache.Set(util.CacheKey("user:log-user", "chat", "gpt-4o-mini", string(raw), string(scope)), []byte(`{"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}`))

	buf := captureLog(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	Serve(h, rec, req, "chat")
	if rec.Code != http.StatusOK {
		t.Fatalf("cache status %d body %s", rec.Code, rec.Body.String())
	}
	line := buf.String()
	t.Log(line)
	for _, want := range []string{
		"step=cache hit=true model=gpt-4o-mini",
		"step=metrics model=gpt-4o-mini cache_hit=true",
		"ttft=",
		"prompt_tokens=11",
		"completion_tokens=7",
		"total_tokens=18",
		"tokens_per_s=0.00",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("cache log missing %q\n%s", want, line)
		}
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":4,\"total_tokens\":9}}\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()
	streamHost := newLogHost(chatConfig(config.ModelEntry{
		ModelName: "gpt-4o-mini",
		LiteLLMParams: map[string]any{
			"model":    "openai/gpt-4o-mini",
			"api_key":  "test-key",
			"api_base": upstream.URL + "/v1",
		},
	}), upstream.Client())
	streamRaw := []byte(`{"messages":[{"content":"hi","role":"user"}],"model":"gpt-4o-mini","stream":true}`)
	buf = captureLog(t)
	sreq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(streamRaw))
	srec := httptest.NewRecorder()
	Serve(streamHost, srec, sreq, "chat")
	if srec.Code != http.StatusOK {
		t.Fatalf("stream status %d body %s", srec.Code, srec.Body.String())
	}
	sline := buf.String()
	t.Log(sline)
	for _, want := range []string{
		"step=metrics model=gpt-4o-mini cache_hit=false",
		"ttft=",
		"prompt_tokens=5",
		"completion_tokens=4",
		"total_tokens=9",
		"tokens_per_s=",
	} {
		if !strings.Contains(sline, want) {
			t.Fatalf("stream log missing %q\n%s", want, sline)
		}
	}
	if strings.Contains(sline, "http://") || strings.Contains(sline, "https://") {
		t.Fatalf("log included a full upstream url: %s", sline)
	}
}

// PinOfficialContext 是 logHost 的测试接口实现。
// 忽略任务范围键和事实；无返回，不保存创建上下文。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logHost) PinOfficialContext(string, providercontract.TaskContext) {}

// OfficialContext 是 logHost 的测试接口实现。
// 忽略任务范围键，返回零值 TaskContext；不恢复或推断计价事实。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logHost) OfficialContext(string) providercontract.TaskContext {
	return providercontract.TaskContext{}
}

// AdmitRequest 是 logHost 的首次准入替身；参数沿用宿主接口，返回身份检查结果。
// 执行器测试调用；不维护分钟窗口，无需清理。
func (h *logHost) AdmitRequest(w http.ResponseWriter, path string, p *auth.Principal, alias string, est int) bool {
	return h.EnforceIdentityLimits(w, path, p, alias, est)
}

// TestServePreservesRetryableHTTPStatus 验证重试耗尽后客户端和账单保留上游状态及错误；本地上游和内存host结束清理。
// 参数t为上下文；覆盖限流、服务不可用、网关失败，避免把HTTP错误与流内错误混淆。
func TestServePreservesRetryableHTTPStatus(t *testing.T) {
	for _, code := range []int{429, 500, 502, 503, 504} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(code)
				io.WriteString(w, `{"error":{"type":"api_error","message":"Service temporarily unavailable"},"request_id":"fixture"}`)
			}))
			defer up.Close()
			h := newLogHost(chatConfig(config.ModelEntry{ModelName: "gpt-4o-mini", LiteLLMParams: map[string]any{"model": "openai/gpt-4o-mini", "api_key": "sk-local-master", "api_base": up.URL + "/v1"}}), up.Client())
			rec := httptest.NewRecorder()
			Serve(h, rec, chatRequest(t, "gpt-4o-mini", false), "chat")
			if rec.Code != code || len(h.spends) != 1 || h.spends[0].status != code || !strings.Contains(rec.Body.String(), "Service temporarily unavailable") || !strings.Contains(rec.Body.String(), "fixture") {
				t.Fatalf("上游状态/错误丢失: %d %s %+v", rec.Code, rec.Body.String(), h.spends)
			}
		})
	}
}

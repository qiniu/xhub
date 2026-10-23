// 本文件验证 execution 的相关执行契约，阅读顺序为测试宿主/辅助方法，再到各个 Test 场景。
// 具体前置、期望、边界和清理方式见每个测试方法；夹具的空实现仅用于隔离当前测试。
package execution

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sunqirui1987/xhub/internal/auth"
	"github.com/sunqirui1987/xhub/internal/config"
	dpcontract "github.com/sunqirui1987/xhub/internal/dataplane/contract"
	"github.com/sunqirui1987/xhub/internal/iam"
	"github.com/sunqirui1987/xhub/internal/live"
	_ "github.com/sunqirui1987/xhub/internal/provider/all"
	providercontract "github.com/sunqirui1987/xhub/internal/provider/contract"
	providerdeployment "github.com/sunqirui1987/xhub/internal/provider/deployment"
	providerregistry "github.com/sunqirui1987/xhub/internal/provider/registry"
	"github.com/sunqirui1987/xhub/internal/router"
	"github.com/sunqirui1987/xhub/internal/util"
)

// TestEndpointAndModelLogic 验证按显式端点选择 Chat、Ark、七牛及自定义部署，模型替换、鉴权、任务归属及缺失/歧义配置的完整请求结果。
// 参数 t 为 Go 测试上下文；返回值无，断言失败定位具体协议、路由或日志行为。
// 前置使用独立测试配置及本地 HTTP 假上游，不依赖外部供应商凭据。
// 服务器通过 defer/Cleanup 关闭，测试状态只存在本例内存，不产生持久化数据。
func TestEndpointAndModelLogic(t *testing.T) {
	var mu sync.Mutex
	var seen []captured
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen = append(seen, captured{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization"), string(body)})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/contents/generations/tasks":
			_, _ = w.Write([]byte(`{"id":"cgt-1"}`))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v3/contents/generations/tasks/"):
			_, _ = w.Write([]byte(`{"id":"cgt-1","status":"succeeded","usage":{"completion_tokens":12}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/contents/generations/tasks":
			_, _ = w.Write([]byte(`{"items":[]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v3/contents/generations/tasks":
			_, _ = w.Write([]byte(`{"id":"qvideo-1"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/generate":
			_, _ = w.Write([]byte(`{"data":{"taskId":"suno-1"}}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer upstream.Close()

	ark := Deployment("volcengine/doubao-seedance-2-0-260128", "volcengine/doubao-seedance-2-0-260128", "ark-key", upstream.URL, "ark_contents_generation", nil)
	arkFast := Deployment("volcengine/doubao-seedance-2-0-fast-260128", "volcengine/doubao-seedance-2-0-fast-260128", "ark-key-2", upstream.URL, "ark_contents_generation", nil)
	gpt := config.ModelEntry{
		ModelName: "gpt-4o",
		ModelInfo: map[string]any{"endpoint_types": []any{"chat", "completion"}},
	}
	host := &logicHost{
		cfg:    &config.Config{},
		client: upstream.Client(),
		models: []config.ModelEntry{gpt, ark, arkFast},
		pins:   map[string]string{},
	}

	// 适配的入口不再登记成端点类型：它们由能力表描述，上游路径由
	// llm.Endpoint 决定。所以这里核对的是能力，不是一组动作。
	if !providerdeployment.IncludesCapability(gpt, "chat") {
		t.Fatalf("the chat deployment does not answer chat: %+v", gpt)
	}
	if providerdeployment.IncludesCapability(gpt, "embedding") {
		t.Fatalf("the chat deployment answered a capability it never declared")
	}
	arkBound := providerdeployment.BoundTransports(ark)
	if len(arkBound) != 1 || arkBound[0].ID != "ark_contents_generation" || len(arkBound[0].Actions) != 3 {
		t.Fatalf("ark endpoints %+v", arkBound)
	}

	if _, ok := providerregistry.Match(http.MethodPost, "/v1/chat/completions"); ok {
		t.Fatal("chat completions was claimed by bypass")
	}

	rec := host.call(t, http.MethodPost, "/api/v3/contents/generations/tasks", `{"model":"volcengine/doubao-seedance-2-0-260128","prompt":"street"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("ark create %d %s", rec.Code, rec.Body.String())
	}
	got := lastCall(t, &mu, seen)
	if got.auth != "Bearer ark-key" || !strings.Contains(got.body, `"model":"doubao-seedance-2-0-260128"`) || !strings.Contains(got.body, `"prompt":"street"`) {
		t.Fatalf("ark upstream %+v", got)
	}
	if host.spend[0].op != "ark_contents_generation:create" || host.spend[0].usage != nil || host.spend[0].depID == "" {
		t.Fatalf("create spend %+v", host.spend)
	}
	if len(host.notes) == 0 || host.notes[0].Provider != "volcengine" || host.notes[0].TTFTMs == nil {
		t.Fatalf("create note %+v", host.notes)
	}

	rec = host.call(t, http.MethodGet, "/api/v3/contents/generations/tasks/cgt-1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("ark get %d %s", rec.Code, rec.Body.String())
	}
	if host.spend[1].op != "ark_contents_generation:get" || host.spend[1].usage["completion_tokens"] == nil {
		t.Fatalf("first poll spend %+v", host.spend[1])
	}
	rec = host.call(t, http.MethodGet, "/api/v3/contents/generations/tasks/cgt-1", "")
	if host.spend[2].usage == nil || host.spend[2].callID == host.spend[1].callID || host.notes[2].SettlementID != host.notes[1].SettlementID || host.notes[1].SettlementID == "" {
		t.Fatalf("second poll must retry the same durable settlement %+v", host.spend[2])
	}

	if qiniu, ok := providerregistry.Match(http.MethodGet, "/v3/contents/generations/tasks/cgt-1"); !ok || qiniu.Transport.ID != "qiniu_contents_generation" {
		t.Fatal("custom Qiniu-compatible route is missing")
	}

	delete(host.pins, officialTaskScope(&auth.Principal{UserID: "test-user"}, "ark_contents_generation", "cgt-1"))
	rec = host.call(t, http.MethodGet, "/api/v3/contents/generations/tasks/cgt-1", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expired pin %d", rec.Code)
	}

	before := len(snapshot(&mu, seen))
	rec = host.call(t, http.MethodGet, "/api/v3/contents/generations/tasks?page_size=2", "")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), ark.ModelName) || !strings.Contains(rec.Body.String(), arkFast.ModelName) {
		t.Fatalf("two-key list %d %s", rec.Code, rec.Body.String())
	}
	if len(snapshot(&mu, seen)) != before {
		t.Fatal("two-key list called upstream")
	}
	host.models = []config.ModelEntry{ark}
	rec = host.call(t, http.MethodGet, "/api/v3/contents/generations/tasks?page_size=2", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("one-key list %d %s", rec.Code, rec.Body.String())
	}
	if lastCall(t, &mu, seen).query != "page_size=2" {
		t.Fatalf("query dropped %+v", lastCall(t, &mu, seen))
	}
	host.models = []config.ModelEntry{gpt, ark, arkFast}

	// 部署上自带路径表不再是 bypass 来源：这两个路径不再被任何转发方式认领，
	// 所以它们根本进不了 bypass 这一层，也就不会被送错地方。
	//
	// 这里刻意不通过 host.call 去发请求：那个辅助函数在没有命中时会直接 Fatal，
	// 而"没有命中"正是这一条要断言的事实。
	for _, target := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/generate"},
		{http.MethodGet, "/api/v1/generate/record-info"},
	} {
		if hit, ok := providerregistry.Match(target.method, target.path); ok {
			t.Fatalf("%s %s was claimed by %s; bypass must come only from the registry",
				target.method, target.path, hit.Transport.ID)
		}
	}
}

// captured 保存本地假上游实际看到的 HTTP 方法、路径、查询、鉴权及正文，供原生执行断言使用；不写数据库。
type captured struct {
	// 假上游观察到的 HTTP 方法、路径、原始查询、鉴权值及正文，测试断言时读取，鉴权只使用假值。
	method, path, query, auth, body string
}

// Deployment 构造原生执行测试的显式部署。
// 参数 name/model 分别为公开别名和上游型号，key/base 为假凭据与本地地址，typeID 为登记传输，extra 覆盖请求参数；返回新 ModelEntry。
// 测试调用；根据传输登记补齐端点和供应商标签，不拨号、不持久化，嵌套 extra 值仍按引用保存。
func Deployment(name, model, key, base, typeID string, extra map[string]any) config.ModelEntry {
	params := map[string]any{"model": model, "api_key": key, "api_base": base}
	endpoint := ""
	for _, transport := range providerregistry.Transports() {
		if transport.ID == typeID {
			endpoint = transport.EndpointID
			if len(transport.Providers) > 0 {
				params["custom_llm_provider"] = transport.Providers[0]
			}
		}
	}
	for k, v := range extra {
		params[k] = v
	}
	return config.ModelEntry{
		ModelName:     name,
		LiteLLMParams: params,
		ModelInfo:     map[string]any{"transport": typeID, "endpoint_types": []string{endpoint}},
	}
}

// snapshot 在互斥锁保护下复制已捕获请求切片，供测试安全读取当前记录。
// 参数 mu 为与写入方共同使用的锁，seen 为待复制记录；返回新切片，空输入返回空切片。
// 只复制元素，不持有锁给调用方；传入切片头的读取也应由测试保证与写入时序协调。
func snapshot(mu *sync.Mutex, seen []captured) []captured {
	mu.Lock()
	defer mu.Unlock()
	out := make([]captured, len(seen))
	copy(out, seen)
	return out
}

// lastCall 返回本地上游最近一次捕获请求，供原生执行测试检查路径、模型及鉴权。
// 参数 t 为测试上下文，mu/seen 为捕获记录的锁和切片；返回最后一项，没有调用时 t.Fatal 终止测试。
// 通过 snapshot 复制，不清除记录或关闭服务器，资源仍由所属测试清理。
func lastCall(t *testing.T, mu *sync.Mutex, seen []captured) captured {
	t.Helper()
	got := snapshot(mu, seen)
	if len(got) == 0 {
		t.Fatal("upstream was not called")
	}
	return got[len(got)-1]
}

// spendNote 保存逻辑宿主捕获的操作、运行部署 ID、调用 ID 和用量；只表示调用过记账边界，不执行费用计算。
type spendNote struct {
	// 原生操作名、运行部署身份和本轮调用 ID，验证记账归属。
	op, depID, callID string
	// 执行器提交的用量映射引用，夹具不计算金额。
	usage map[string]any
}

// logicHost 是原生执行测试的内存宿主，提供固定身份、显式部署、路由快照及任务归属。
// 本地上游仍接收真实 HTTP；宿主不连接身份库或 Redis，记录失败、用量和备注供断言。
// 字段只由对应测试管理，除特别声明的并发宿主外不可并发写；空实现是隔离测试边界，不代表生产行为。
type logicHost struct {
	// 统一入口读取的进程配置，可与原生 models 测试目录分开。
	cfg *config.Config
	// 访问本地假上游的 HTTP 客户端。
	client *http.Client
	// 原生入口的显式测试部署目录。
	models []config.ModelEntry
	// 按已隔离任务键保存创建时计价事实。
	facts map[string]providercontract.TaskContext
	// 已隔离任务键到运行部署 ID 的内存归属。
	pins map[string]string
	// 顺序捕获的记账调用，不代表真实扣费。
	spend []spendNote
	// 顺序捕获的调用备注，基础夹具不按 callID 合并。
	notes []dpcontract.CallNote
	// 本例自定义路由设置，nil 时使用内置设置。
	settings map[string]any
	// 模型目录提供的实时默认权重，独立于策略模板。
	modelDefaults map[string]any
	// 被执行器报告失败的运行部署 ID 列表。
	failures []string
}

// call 构造原生入口请求并执行 ServeBypass，供 logicHost 的生命周期测试复用。
// 参数 t 为测试上下文，method/target/body 指定入站方法、路径及正文；返回 HTTP 记录器。
// 登记路径找不到时 t.Fatal；不启动网关监听器，ServeBypass 可能访问测试指定本地上游，服务由测试关闭。
func (h *logicHost) call(t *testing.T, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	hit, ok := providerregistry.Match(method, req.URL.Path)
	if !ok {
		t.Fatalf("no endpoint for %s %s", method, target)
	}
	rec := httptest.NewRecorder()
	ServeBypass(h, rec, req, hit)
	return rec
}

// RequireLLMPrincipal 是 logicHost 的测试接口实现。
// 忽略测试请求中的凭据，返回固定会话身份；参数 w/r 仅满足接口，不写拒绝响应，不查询真实身份库。 固定用户为 test-user。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logicHost) RequireLLMPrincipal(http.ResponseWriter, *http.Request) *auth.Principal {
	return &auth.Principal{Kind: "session", UserID: "test-user"}
}

// ResolveRequest 是 logicHost 的测试接口实现。
// 在测试重试中返回相同固定会话身份及 nil 错误；参数请求不参与鉴权，不能验证生产密钥撤销逻辑。 固定用户为 test-user。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logicHost) ResolveRequest(*http.Request) (*auth.Principal, error) {
	return &auth.Principal{Kind: "session", UserID: "test-user"}, nil
}

// RouteSettingsFor 是 logicHost 的测试接口实现。
// 优先返回 h.settings 与 h.modelDefaults，未指定设置时使用 BuiltinSettings 并补入模型默认；身份参数不参与解析，不访问数据库。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logicHost) RouteSettingsFor(*auth.Principal) router.RouteSettings {
	if h.settings != nil {
		return router.RouteSettings{Settings: h.settings, ModelDefaults: h.modelDefaults}
	}
	settings := router.BuiltinSettings()
	settings.ModelDefaults = h.modelDefaults
	return settings
}

// GatewayConfig 是 logicHost 的测试接口实现。
// 返回本夹具的 cfg 指针；无参数，供执行入口读取目录，不复制配置，不产生外部副作用。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logicHost) GatewayConfig() *config.Config { return h.cfg }

// HTTPClient 是 logicHost 的测试接口实现。
// 返回本夹具指定的 client 指针；无参数，由执行器调用测试上游，方法本身不拨号。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logicHost) HTTPClient() *http.Client { return h.client }

// ResponseCache 是 logicHost 的测试接口实现。
// 每次返回一个全新内存缓存；无参数，缓存内容不跨方法调用保留，因此不能用此宿主验证缓存命中。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logicHost) ResponseCache() *util.DualCache { return util.NewCache() }

// RouteState 是 logicHost 的测试接口实现。
// 返回测试路由运行快照；无参数，供调度器读取，不读取真实并发或 Redis 状态。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logicHost) RouteState() router.State { return router.State{} }

// GuardrailBlocks 是 logicHost 的测试接口实现。
// 接受但忽略调用 ID 和请求正文，返回 false 及空消息，保证本测试只关注执行链；不验证生产护栏。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logicHost) GuardrailBlocks(string, map[string]any, ...bool) (bool, string) {
	return false, ""
}

// AttachCredential 是 logicHost 的测试接口实现。
// 原样返回参数 dep 和 nil 错误，供已含假凭据的测试部署执行；不连接凭据库或修改配置。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logicHost) AttachCredential(dep config.ModelEntry) (config.ModelEntry, error) {
	return dep, nil
}

// IncBusy 是 logicHost 的测试接口实现。
// 接受部署运行 ID 但不修改并发状态；无返回，仅满足执行接口，测试不验证真实负载调度。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logicHost) IncBusy(string) {}

// DecBusy 是 logicHost 的测试接口实现。
// 接受部署运行 ID 但不修改并发状态；无返回，与空 IncBusy 对应，不释放外部资源。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logicHost) DecBusy(string) {}

// NoteFailure 是 logicHost 的测试接口实现。
// 把参数运行部署 ID 追加到 failures，忽略路由设置；无返回，不计算阈值或修改 Redis，用于断言失败次数。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logicHost) NoteFailure(id string, _ router.RouteSettings) {
	h.failures = append(h.failures, id)
}

// NoteLatency 是 logicHost 的测试接口实现。
// 忽略运行部署 ID 和毫秒值；无返回，测试不记录真实延迟统计。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logicHost) NoteLatency(string, float64) {}

// SetChatHeaders 是 logicHost 的测试接口实现。
// 忽略写入器、身份、公开模型及上游地址；无返回，不在测试响应上添加生产诊断头。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logicHost) SetChatHeaders(http.ResponseWriter, *auth.Principal, string, string) {}

// RecordSpend 是 logicHost 的测试接口实现。
// 将 op、depID、callID 和 usage 追加到 spend，其他参数忽略；无返回，不扣费，usage 保留原映射引用，不可并发写。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logicHost) RecordSpend(_ http.ResponseWriter, _ *auth.Principal, callID, _, op string, usage map[string]any, _ time.Time, _ bool, _ int, depID string) {
	h.spend = append(h.spend, spendNote{op: op, depID: depID, callID: callID, usage: usage})
}

// RememberExchange 是 logicHost 的测试接口实现。
// 忽略调用 ID、请求和正文；无返回，不捕获交互，需验证正文的测试使用 logHost 或并发宿主。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logicHost) RememberExchange(string, *http.Request, []byte, []byte) {}

// PlanRoute 是 logicHost 的测试接口实现。
// 忽略请求、模型、正文和身份，返回零值 RoutePlan，供无会话偏好的执行测试；不读缓存或授权历史。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logicHost) PlanRoute(*http.Request, string, map[string]any, *auth.Principal) dpcontract.RoutePlan {
	return dpcontract.RoutePlan{}
}

// CommitRoute 是 logicHost 的测试接口实现。
// 忽略路由计划、稳定部署 ID 和响应 ID；无返回，不保存会话偏好或父响应归属。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logicHost) CommitRoute(dpcontract.RoutePlan, string, string) {}

// AnnotateCall 是 logicHost 的测试接口实现。
// 把参数 note 追加到 notes，忽略 callID；无返回，不执行生产按调用合并或去重，只用于顺序测试断言。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logicHost) AnnotateCall(_ string, note dpcontract.CallNote) { h.notes = append(h.notes, note) }

// ReleaseCall 满足原生捕获宿主的清理契约；参数为本次 ID，无返回。
// notes 是断言用历史副本而非生产暂存，不在请求退出时删除；夹具由测试回收。
func (h *logicHost) ReleaseCall(string) {}

// FindDeployment 是 logicHost 的测试接口实现。
// 按参数运行部署 ID 查找测试目录，返回部署及 ok；不存在时返回零部署和 false，不访问外部目录。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logicHost) FindDeployment(id string) (config.ModelEntry, bool) {
	for _, m := range h.models {
		if router.CooldownID(m) == id {
			return m, true
		}
	}
	return config.ModelEntry{}, false
}

// PinOfficial 是 logicHost 的测试接口实现。
// 把已隔离任务范围键与运行部署 ID 保存到内存 pins；无返回，不设置真实缓存 TTL，map 须提前初始化。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logicHost) PinOfficial(taskID, deploymentID string) { h.pins[taskID] = deploymentID }

// OfficialDeployment 是 logicHost 的测试接口实现。
// 按已隔离任务范围键读取 pins；返回运行部署 ID，缺失为空，不查询远程任务。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logicHost) OfficialDeployment(taskID string) string { return h.pins[taskID] }

// WriteCacheHit 是 logicHost 的测试接口实现。
// 接受响应、身份、调用/模型/缓存/操作标识、缓存正文和开始时间，但不输出或结算；无返回，只满足接口。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logicHost) WriteCacheHit(http.ResponseWriter, *auth.Principal, string, string, string, string, []byte, time.Time) {
}

// WriteChatJSON 是 logicHost 的测试接口实现。
// 接受统一响应及记账参数（含可变 models 授权范围）并全部忽略；本基础夹具不输出或结算。
// 无返回，具体测试需要写出时另行覆写。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logicHost) WriteChatJSON(http.ResponseWriter, *auth.Principal, string, string, string, string, string, []byte, int, time.Time, string, ...string) {
}

// EnforceIdentityLimits 是 logicHost 的测试接口实现。
// 忽略写入器、路径、身份、模型和估算值，恒返回 true；不验证真实模型授权、预算或 RPM/TPM。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logicHost) EnforceIdentityLimits(http.ResponseWriter, string, *auth.Principal, string, int) bool {
	return true
}

// Redis 是 logicHost 的测试接口实现。
// 返回 nil 热状态客户端；无参数，保证测试不连接 Redis，相关生产路径会按缺配置降级。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logicHost) Redis() *live.Client { return nil }

// Models 是 logicHost 的测试接口实现。
// 返回本夹具的部署切片；无参数，不复制嵌套映射，执行器只能只读使用。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logicHost) Models() []config.ModelEntry { return h.models }

// BusyMap 是 logicHost 的测试接口实现。
// 返回新建的空并发映射；无参数，表示测试没有正在占用的部署，不读取生产计数。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logicHost) BusyMap() map[string]int { return map[string]int{} }

// Identity 是 logicHost 的测试接口实现。
// 返回 nil 身份数据库；无参数，保证测试不产生数据库写入。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logicHost) Identity() *iam.DB { return nil }

// PinOfficialContext 是 logicHost 的测试接口实现。
// 按 scope 保存 facts；无返回，首次调用时初始化内存 facts 表，供原生轮询恢复，不设置 TTL。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logicHost) PinOfficialContext(scope string, facts providercontract.TaskContext) {
	if h.facts == nil {
		h.facts = map[string]providercontract.TaskContext{}
	}
	h.facts[scope] = facts
}

// OfficialContext 是 logicHost 的测试接口实现。
// 按已隔离任务范围键读取创建事实；缺失返回零值 TaskContext，不推断原任务输入或访问上游。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *logicHost) OfficialContext(scope string) providercontract.TaskContext { return h.facts[scope] }

// AdmitRequest 是 logicHost 的首次准入替身；参数沿用宿主接口，返回身份检查结果。
// 执行器测试调用；不维护分钟窗口，无需清理。
func (h *logicHost) AdmitRequest(w http.ResponseWriter, path string, p *auth.Principal, alias string, est int) bool {
	return h.EnforceIdentityLimits(w, path, p, alias, est)
}

// NativeResponsesAllowed 保持测试宿主默认关闭，不读外部配置。
func (h *logicHost) NativeResponsesAllowed(string) bool { return false }

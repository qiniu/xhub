// 本文件验证 execution 的相关执行契约，阅读顺序为测试宿主/辅助方法，再到各个 Test 场景。
// 具体前置、期望、边界和清理方式见每个测试方法；夹具的空实现仅用于隔离当前测试。
package execution

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sunqirui1987/xhub/internal/catalog"
	dptransport "github.com/sunqirui1987/xhub/internal/dataplane/transport"
	providercontract "github.com/sunqirui1987/xhub/internal/provider/contract"
	providerregistry "github.com/sunqirui1987/xhub/internal/provider/registry"
)

// TestNativeBypassJSONAndUsage 验证原生路径、协议鉴权、模型替换、数字精度和用量合并，并阻止客户端凭据泄露。
// 参数 t：Go 测试上下文。返回：无；断言失败时报告协议或计费契约回归。
// 调用：go test；使用本地注册表、价格目录或假上游，不创建真实付费任务。
// 测试只操作内存配置或本地假上游，HTTP 服务及正文由本例关闭，不创建外部付费任务或持久化数据。
func TestNativeBypassJSONAndUsage(t *testing.T) {
	for _, spec := range []struct{ transport, path, upstream, protocol, response string }{
		{"bypass_openai_chat", "/bypass/openai/v1/chat/completions", "/v1/chat/completions", "openai", `{"id":"c1","usage":{"prompt_tokens":100,"completion_tokens":4,"prompt_tokens_details":{"cached_tokens":25}}}`},
		{"bypass_openai_responses", "/bypass/openai/v1/responses", "/v1/responses", "openai", `{"id":"r1","usage":{"input_tokens":100,"output_tokens":4,"input_tokens_details":{"cached_tokens":25}}}`},
		{"bypass_anthropic_messages", "/bypass/anthropic/v1/messages", "/v1/messages", "anthropic", `{"id":"m1","usage":{"input_tokens":20,"output_tokens":4,"cache_read_input_tokens":30,"cache_creation_input_tokens":10}}`},
	} {
		t.Run(spec.transport, func(t *testing.T) {
			hits := 0
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits++
				if r.URL.Path != spec.upstream || r.URL.RawQuery != "beta=1" {
					t.Error(r.URL)
				}
				if spec.protocol == "anthropic" {
					if r.Header.Get("X-Api-Key") != "supplier-key" || r.Header.Get("Authorization") != "" || r.Header.Get("Anthropic-Version") != "2023-06-01" || r.Header.Get("Anthropic-Beta") != "test-beta" {
						t.Error("native auth/headers incorrect")
					}
				} else if r.Header.Get("Authorization") != "Bearer supplier-key" || r.Header.Get("X-Api-Key") != "" {
					t.Error("native auth incorrect")
				}
				if r.Header.Get("Cookie") != "" || r.Header.Get("X-Hop") != "" {
					t.Error("gateway credentials/hop headers leaked")
				}
				var doc map[string]json.RawMessage
				json.NewDecoder(r.Body).Decode(&doc)
				if string(doc["model"]) != `"`+spec.protocol+`/upstream"` || string(doc["opaque"]) != `9007199254740993` || !bytes.Contains(doc["custom"], []byte("keep")) {
					t.Error("native fields modified", doc)
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Request-Id", "upstream-id")
				io.WriteString(w, spec.response)
			}))
			defer up.Close()
			h := officialHost(up, Deployment("public", spec.protocol+"/upstream", "supplier-key", up.URL+"/v1", spec.transport, nil))
			req := httptest.NewRequest("POST", spec.path+"?beta=1", strings.NewReader(`{"model":"public","opaque":9007199254740993,"custom":{"keep":true}}`))
			req.Header.Set("Authorization", "Bearer gateway-key")
			req.Header.Set("X-Api-Key", "gateway-key")
			req.Header.Set("Cookie", "session=private")
			req.Header.Set("Connection", "X-Hop")
			req.Header.Set("X-Hop", "private")
			req.Header.Set("Anthropic-Beta", "test-beta")
			hit, _ := providerregistry.Match("POST", req.URL.Path)
			rec := httptest.NewRecorder()
			ServeBypass(h, rec, req, hit)
			if rec.Code != 200 || rec.Body.String() != spec.response || rec.Header().Get("X-Request-Id") != "upstream-id" || hits != 1 || len(h.spend) != 1 {
				t.Fatal(rec.Code, rec.Body.String(), hits)
			}
			usage := catalog.NormalizeUsage(h.spend[0].usage)
			if usage.CompletionTokens != 4 {
				t.Fatal(usage)
			}
			if spec.protocol == "anthropic" && (usage.PromptTokens != 50 || usage.CachedTokens != 30 || usage.CacheWriteTokens != 10) {
				t.Fatal(usage)
			}
			if spec.protocol == "openai" && (usage.PromptTokens != 100 || usage.CachedTokens != 25) {
				t.Fatal(usage)
			}
		})
	}
}

// TestNativeImageEditMultipart 验证图片编辑保留 multipart 边界、文件字节与字段，并正确记录图片规格和用量。
// 参数 t：Go 测试上下文。返回：无；断言失败时报告协议或计费契约回归。
// 调用：go test；使用本地注册表、价格目录或假上游，不创建真实付费任务。
// 测试只操作内存配置或本地假上游，HTTP 服务及正文由本例关闭，不创建外部付费任务或持久化数据。
func TestNativeImageEditMultipart(t *testing.T) {
	for _, mode := range []struct{ transport, path string }{{"openai_image_edit", "/v1/images/edits"}, {"bypass_openai_image_edit", "/bypass/openai/v1/images/edits"}} {
		t.Run(mode.transport, func(t *testing.T) {
			var raw bytes.Buffer
			mw := multipart.NewWriter(&raw)
			mw.SetBoundary("native-boundary")
			mw.WriteField("model", "public")
			mw.WriteField("quality", "high")
			mw.WriteField("size", "1024x1024")
			image := []byte{0, 255, 1, 2, 13, 10, 3}
			part, _ := mw.CreateFormFile("image[]", "original.png")
			part.Write(image)
			mw.Close()
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/images/edits" || !strings.Contains(r.Header.Get("Content-Type"), "native-boundary") {
					t.Error(r.URL, r.Header.Get("Content-Type"))
				}
				reader, err := r.MultipartReader()
				if err != nil {
					t.Error(err)
					return
				}
				fields := map[string]string{}
				for {
					part, err := reader.NextPart()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Error(err)
						return
					}
					data, _ := io.ReadAll(part)
					if part.FileName() != "" {
						if part.FileName() != "original.png" || part.FormName() != "image[]" || !bytes.Equal(data, image) {
							t.Error("file changed")
						}
					} else {
						fields[part.FormName()] = string(data)
					}
				}
				if fields["model"] != "openai/gpt-image" || fields["quality"] != "high" || fields["size"] != "1024x1024" {
					t.Error(fields)
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"data":[{"b64_json":"AAAA"}],"usage":{"input_tokens":2,"output_tokens":3}}`)
			}))
			defer up.Close()
			h := officialHost(up, Deployment("public", "openai/gpt-image", "supplier-key", up.URL, mode.transport, nil))
			req := httptest.NewRequest("POST", mode.path, &raw)
			req.Header.Set("Content-Type", mw.FormDataContentType())
			hit, _ := providerregistry.Match("POST", req.URL.Path)
			rec := httptest.NewRecorder()
			ServeBypass(h, rec, req, hit)
			if rec.Code != 200 || len(h.spend) != 1 {
				t.Fatal(rec.Code, rec.Body.String())
			}
			u := catalog.NormalizeUsage(h.spend[0].usage)
			if u.Images != 1 || u.ImageVariant != "high_1024x1024" || u.OutputVariant != "image" || u.CompletionTokens != 3 {
				t.Fatal(u)
			}
		})
	}
}

// TestNativeBypassStreamingFlushAndMergedUsage 验证 SSE 首个事件立即送达，保留原始换行，并合并 Anthropic 分散报告的缓存和 token 事实。
// 参数 t：Go 测试上下文。返回：无；断言失败时报告协议或计费契约回归。
// 调用：go test；使用本地注册表、价格目录或假上游，不创建真实付费任务。
// 测试只操作内存配置或本地假上游，HTTP 服务及正文由本例关闭，不创建外部付费任务或持久化数据。
func TestNativeBypassStreamingFlushAndMergedUsage(t *testing.T) {
	first := "event: message_start\r\ndata: {\"message\":{\"usage\":{\"input_tokens\":20,\"output_tokens\":0,\"cache_read_input_tokens\":30}}}\r\n\r\n"
	last := "event: message_delta\ndata: {\"usage\":{\"output_tokens\":4}}\n\nevent: message_stop\ndata: {}\n\n"
	release := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, first)
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		io.WriteString(w, last)
	}))
	defer up.Close()
	h := officialHost(up, Deployment("public", "anthropic/claude", "supplier-key", up.URL, "bypass_anthropic_messages", nil))
	done := make(chan struct{})
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		hit, _ := providerregistry.Match("POST", r.URL.Path)
		ServeBypass(h, w, r, hit)
	}))
	defer gateway.Close()
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Post(gateway.URL+"/bypass/anthropic/v1/messages", "application/json", strings.NewReader(`{"model":"public","stream":true}`))
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	head := make([]byte, len(first))
	if !strings.Contains(resp.Header.Get("Cache-Control"), "no-transform") {
		t.Fatal("原生bypass首帧缺少代理防缓冲头")
	}
	_, err = io.ReadFull(resp.Body, head)
	close(release)
	if err != nil || string(head) != first {
		t.Fatal("first event did not flush", err)
	}
	tail, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	<-done
	if err != nil || string(tail) != last || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatal(err, string(tail))
	}
	if len(h.spend) != 1 {
		t.Fatal(h.spend)
	}
	u := catalog.NormalizeUsage(h.spend[0].usage)
	if u.PromptTokens != 50 || u.CachedTokens != 30 || u.CompletionTokens != 4 {
		t.Fatal(u)
	}
}

// TestNativeCreateServerErrorIsNotReplayed 验证异步创建操作返回 5xx 后不会因路由重试设置生成第二个付费任务。
// 参数 t：Go 测试上下文。返回：无；断言失败时报告协议或计费契约回归。
// 调用：go test；使用本地注册表、价格目录或假上游，不创建真实付费任务。
// 测试只操作内存配置或本地假上游，HTTP 服务及正文由本例关闭，不创建外部付费任务或持久化数据。
func TestNativeCreateServerErrorIsNotReplayed(t *testing.T) {
	calls := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(503)
		io.WriteString(w, `{"error":{"message":"busy"}}`)
	}))
	defer up.Close()
	h := officialHost(up, Deployment("public", "openai/gpt", "key", up.URL, "bypass_openai_responses", nil))
	h.settings = map[string]any{
		"model_routes": []any{},
		"retry_policy": map[string]any{"max_attempts": 3, "timeout_seconds": 60, "failure_threshold": 3, "cooldown_seconds": 0},
	}
	rec := h.call(t, "POST", "/bypass/openai/v1/responses", `{"model":"public","background":true}`)
	if rec.Code != 503 || calls != 1 || h.spend[0].usage != nil {
		t.Fatal(rec.Code, calls, h.spend)
	}
}

// TestCustomQiniuTransportDoesNotDependOnHostname 验证 Custom 凭据显式选择七牛传输后可使用任意配置地址，
// 并只按传输定义移除 qiniu 路由前缀；测试使用纯 URL 组合，不访问外部服务。
// 参数 t 为 Go 测试上下文；返回值无，断言失败时终止当前测试。
// 测试只操作内存配置或本地假上游，HTTP 服务及正文由本例关闭，不创建外部付费任务或持久化数据。
func TestCustomQiniuTransportDoesNotDependOnHostname(t *testing.T) {
	hit, ok := providerregistry.Match("POST", "/v3/contents/generations/tasks")
	if !ok || hit.Transport.ID != "qiniu_contents_generation" {
		t.Fatal("七牛兼容传输未登记")
	}
	if got := dptransport.BypassDeploymentURL("https://configured.example", hit); got != "https://configured.example/v3/contents/generations/tasks" {
		t.Fatal(got)
	}
	if got := providercontract.OfficialID(hit.Transport.StripPrefix, "qiniu/bytedance/doubao-seedance-2-0-260128"); got != "bytedance/doubao-seedance-2-0-260128" {
		t.Fatal(got)
	}
}

// TestNativeMultipleImageEventsRemainUnpriced 验证多张流式图片不会按最后一张的规格错误结算。
// 参数 t：Go 测试上下文。返回：无；同时断言原始流保真、完成图片数和待核价原因。
// 调用：go test；使用本地假上游，不消耗图片生成额度。
// 测试只操作内存配置或本地假上游，HTTP 服务及正文由本例关闭，不创建外部付费任务或持久化数据。
func TestNativeMultipleImageEventsRemainUnpriced(t *testing.T) {
	const events = "event: image_generation.completed\ndata: {\"quality\":\"high\",\"size\":\"1024x1024\",\"usage\":{\"output_tokens\":3}}\n\nevent: image_generation.completed\ndata: {\"quality\":\"low\",\"size\":\"1536x1024\",\"usage\":{\"output_tokens\":5}}\n\n"
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, events)
	}))
	defer up.Close()
	h := officialHost(up, Deployment("public", "openai/gpt-image", "supplier-key", up.URL, "bypass_openai_image_generation", nil))
	rec := h.call(t, "POST", "/bypass/openai/v1/images/generations", `{"model":"public","stream":true}`)
	if rec.Code != http.StatusOK || rec.Body.String() != events || len(h.spend) != 1 {
		t.Fatal("image stream was changed or not recorded", rec.Code, len(h.spend))
	}
	usage := catalog.NormalizeUsage(h.spend[0].usage)
	if usage.Images != 2 || usage.PricingBlocked != "multiple_image_events_require_itemized_pricing" {
		t.Fatal("multiple variants incorrectly became a single priced bill", usage)
	}
}

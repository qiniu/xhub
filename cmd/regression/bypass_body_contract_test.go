package regression

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

// TestBypassBodyHTTPContract 验证真实原生入口的载荷保留、同步/流式计费、失败拒绝和恢复。
// 参数 t 为回归上下文；前置隔离 PostgreSQL 与本地 HTTP 供应商，检查实际出站字段/次数/日志/余额，harness 清理 schema 与账单。
func TestBypassBodyHTTPContract(t *testing.T) {
	var calls atomic.Int32
	var reject atomic.Bool
	h, owner, admin, id := historyHarness(t, "responses", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer local-key" {
			t.Errorf("原生出站路径/凭据错误: %s", r.URL.Path)
		}
		if string(body["model"]) != `"wire-model"` || string(body["opaque"]) != "9007199254740993" || string(body["vendor"]) != `{"nullable":null,"empty":"","zero":0}` || body["session_id"] != nil || body["disable_fallbacks"] != nil {
			t.Errorf("原生出站载荷被破坏: %s", mustJSON(body))
		}
		if reject.Load() {
			w.WriteHeader(422)
			writeJSON(w, map[string]any{"error": map[string]any{"type": "vendor_reject", "message": "Fixture rejected"}})
			return
		}
		writeAgentResponse(w, nativeAgentResponse(fmt.Sprintf("resp_body_%d", calls.Load()), "Payload retained", false, nil), string(body["stream"]) == "true")
	}))
	h.ok("POST", "/model/update", admin, map[string]any{"id": id, "litellm_params": map[string]any{"model": "wire-model"}, "model_info": map[string]any{"transport": "bypass_openai_responses", "endpoint_types": []string{"responses", "bypass:openai-responses"}}})
	const path = "/bypass/openai/v1/responses"
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			raw := fmt.Sprintf(`{"model":"history-model","input":"Reply briefly.","session_id":"body-contract","disable_fallbacks":true,"opaque":9007199254740993,"vendor":{"nullable":null,"empty":"","zero":0},"stream":%t}`, stream)
			before := h.moneyOf(t, owner)
			n := calls.Load()
			got := h.doRaw("POST", path, owner.key, "application/json", raw)
			if got.status != 200 || !strings.Contains(got.text(), "Payload retained") || calls.Load() != n+1 {
				t.Fatalf("原生真实调用未完成或重复: %s", got.describe())
			}
			if stream && !strings.Contains(got.text(), "response.completed") {
				t.Fatal("原生流缺少成功终态")
			}
			if !h.moneyOf(t, owner).grewBy(before, 8*testInputRate+2*testOutputRate) {
				t.Fatal("解析重构改变真实消费")
			}
			h.flushSpend()
			log := h.ok("GET", "/spend/logs/ui/"+got.header("x-litellm-call-id"), admin, nil).json()
			amount, _ := floatField(log, "spend")
			if log["status"] != "success" || log["api_key"] != owner.keyID || !nearlyEqual(amount, 8*testInputRate+2*testOutputRate) {
				t.Fatalf("日志/费用归属错误: %v", log)
			}
		})
	}
	t.Run("rejected-before-upstream", func(t *testing.T) {
		n := calls.Load()
		before := h.moneyOf(t, owner)
		for _, raw := range []string{"{", "null", "[]", `{"input":"Hello"}`} {
			got := h.doRaw("POST", path, owner.key, "application/json", raw)
			if got.status != 400 || !strings.Contains(got.text(), "error") {
				t.Fatalf("非法载荷未明确拒绝: %s", got.describe())
			}
		}
		if got := h.doRaw("POST", path, "invalid", "application/json", "{"); got.status != 401 {
			t.Fatalf("解析抢在鉴权之前: %s", got.describe())
		}
		if calls.Load() != n || !h.moneyOf(t, owner).grewBy(before, 0) {
			t.Fatal("非法请求访问上游或产生费用")
		}
	})
	t.Run("upstream-error-and-recovery", func(t *testing.T) {
		raw := `{"model":"history-model","input":"Reply briefly.","opaque":9007199254740993,"vendor":{"nullable":null,"empty":"","zero":0}}`
		reject.Store(true)
		before := h.moneyOf(t, owner)
		n := calls.Load()
		got := h.doRaw("POST", path, owner.key, "application/json", raw)
		if got.status != 422 || !strings.Contains(got.text(), "Fixture rejected") || calls.Load() != n+1 {
			t.Fatalf("上游失败语义改变: %s", got.describe())
		}
		h.flushSpend()
		log := h.ok("GET", "/spend/logs/ui/"+got.header("x-litellm-call-id"), admin, nil).json()
		if log["status"] != "error" || !h.moneyOf(t, owner).grewBy(before, 0) {
			t.Fatalf("失败被记成功或产生费用: %v", log)
		}
		reject.Store(false)
		got = h.doRaw("POST", path, owner.key, "application/json", raw)
		if got.status != 200 || !strings.Contains(got.text(), "Payload retained") {
			t.Fatalf("失败后未恢复: %s", got.describe())
		}
	})
}

// TestBypassMultipartHTTPContract 验证图片编辑真实出站分段、二进制、控制字段、用量和非法表单阻断。
// 参数 t 为回归上下文；前置真实网关/隔离 PostgreSQL/本地上游；关闭服务并由 harness 删除 schema、模型、账户与账单。
func TestBypassMultipartHTTPContract(t *testing.T) {
	var calls atomic.Int32
	image := []byte{0, 255, 13, 10, 1, 42}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/images/edits" || r.Header.Get("Authorization") != "Bearer sk-fake" {
			t.Errorf("图片出站路径/鉴权错误: %s", r.URL.Path)
		}
		reader, err := r.MultipartReader()
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		var names, values []string
		for {
			part, err := reader.NextRawPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			data, err := io.ReadAll(part)
			if err != nil {
				t.Error(err)
				return
			}
			names = append(names, part.FormName())
			values = append(values, string(data))
			if part.FormName() == "image[]" && part.FileName() != "original.bin" {
				t.Error("文件名丢失")
			}
		}
		if !reflect.DeepEqual(names, []string{"model", "quality", "size", "vendor", "vendor", "image[]"}) || !reflect.DeepEqual(values, []string{"wire-image", "high", "1024x1024", "one", "two", string(image)}) {
			t.Errorf("图片出站字段或文件错误: %v %q", names, values)
		}
		writeJSON(w, map[string]any{"data": []any{map[string]any{"b64_json": "AAAA"}}, "usage": map[string]any{"input_tokens": 8, "output_tokens": 2}})
	}))
	t.Cleanup(up.Close)
	dep := chatDeployment("image-public")
	dep.LiteLLMParams["model"] = "wire-image"
	dep.LiteLLMParams["api_base"] = up.URL
	dep.ModelInfo["transport"] = "openai_image_edit"
	dep.ModelInfo["endpoint_types"] = []string{"image_edit"}
	dep.ModelInfo["pricing_source"] = "manual"
	h := newHarness(t, dep)
	admin := h.adminSession()
	owner := h.openScope(t, admin, "multipart-owner")
	var raw bytes.Buffer
	writer := multipart.NewWriter(&raw)
	for _, field := range [][2]string{{"model", "image-public"}, {"session_id", "form-session"}, {"disable_fallbacks", "true"}, {"quality", "high"}, {"size", "1024x1024"}, {"vendor", "one"}, {"vendor", "two"}} {
		if err := writer.WriteField(field[0], field[1]); err != nil {
			t.Fatal(err)
		}
	}
	part, err := writer.CreateFormFile("image[]", "original.bin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(image); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	before := h.moneyOf(t, owner)
	got := h.doRaw("POST", "/v1/images/edits", owner.key, writer.FormDataContentType(), raw.String())
	if got.status != 200 || !strings.Contains(got.text(), "AAAA") || calls.Load() != 1 {
		t.Fatalf("图片编辑未真实成功: %s", got.describe())
	}
	if !h.moneyOf(t, owner).grewBy(before, 8*testInputRate+2*testOutputRate) {
		t.Fatal("图片编辑消费错误")
	}
	h.flushSpend()
	log := h.ok("GET", "/spend/logs/ui/"+got.header("x-litellm-call-id"), admin, nil).json()
	if log["status"] != "success" {
		t.Fatalf("图片编辑日志未成功: %v", log)
	}
	t.Run("malformed-form", func(t *testing.T) {
		before := h.moneyOf(t, owner)
		for _, media := range []string{"multipart/form-data", "multipart/form-data; boundary=broken"} {
			got := h.doRaw("POST", "/v1/images/edits", owner.key, media, "--broken\r\n")
			if got.status != 400 || !strings.Contains(got.text(), "error") {
				t.Fatalf("非法表单未拒绝: %s", got.describe())
			}
		}
		if calls.Load() != 1 || !h.moneyOf(t, owner).grewBy(before, 0) {
			t.Fatal("非法表单访问上游或产生费用")
		}
	})
}

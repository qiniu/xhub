package regression

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// TestProviderFileBoundary 通过真实HTTP/PG验证供应商前缀的旧文件接口不把公共配置当作文件。
// 前置独立schema、存量同名配置与可计数本地模型；验证原鉴权、明确错误、零副作用及正常计费和零预算拦截。
// 配置夹具显式清理，用户、Key与账单由harness回收；不访问外部供应商或生产。
func TestProviderFileBoundary(t *testing.T) {
	h := newHarness(t, chatDeployment("file-boundary"))
	admin := h.personalBillingSession()
	owner := h.openScope(t, admin, "file-boundary")
	for _, id := range []string{"files", "fixture-file", "content"} {
		if err := h.store.PutKV("fixture-provider", id, `{"id":"`+id+`","name":"keep-config","private_fixture":"must-not-echo"}`); err != nil {
			t.Fatal(err)
		}
	}
	before, err := h.store.ListKV("fixture-provider")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, method, path string }{
		{"list", "GET", "/fixture-provider/v1/files"},
		{"create", "POST", "/fixture-provider/v1/files"},
		{"retrieve", "GET", "/fixture-provider/v1/files/fixture-file"},
		{"content", "GET", "/fixture-provider/v1/files/fixture-file/content"},
		{"delete", "DELETE", "/fixture-provider/v1/files/fixture-file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, actor := range []struct {
				token  string
				status int
			}{{"", 401}, {owner.session, 403}, {owner.key, 403}, {admin, 501}} {
				out := h.do(tc.method, tc.path, actor.token, map[string]any{"filename": "input-fixture", "private_fixture": "must-not-echo"})
				if out.status != actor.status {
					t.Errorf("旧文件状态错误: %s", out.describe())
				}
				if actor.token == admin && (!strings.Contains(out.text(), "unsupported_operation") || strings.Contains(out.text(), "must-not-echo") || out.header("Cache-Control") != "no-store") {
					t.Errorf("旧文件伪成功或泄漏配置: %s", out.describe())
				}
			}
			after, err := h.store.ListKV("fixture-provider")
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Errorf("旧文件误写公共配置: %v err=%v", after, err)
			}
			if len(h.upstreamCalls()) != 0 {
				t.Fatal("未实现文件不能调用供应商")
			}
		})
	}
	for _, invalid := range []any{[]any{}, "invalid-object", map[string]any{"jsonrpc": "2.0", "method": "submit"}} {
		if out := h.do("POST", "/fixture-provider/v1/files", admin, invalid); out.status != 400 {
			t.Fatal("JSON校验改变", out.describe())
		}
	}
	// 实际文件客户端发送multipart而非JSON；保留既有正文校验400，也不能把文件字节存成配置。
	var upload bytes.Buffer
	writer := multipart.NewWriter(&upload)
	part, err := writer.CreateFormFile("file", "fixture.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(part, "Private fixture content."); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest("POST", h.server.URL+"/fixture-provider/v1/files", &upload)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+admin)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := h.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	if readErr != nil || resp.StatusCode != 400 || strings.Contains(string(raw), "Private fixture content.") {
		t.Fatalf("未支持的实际文件上传错误: status=%d body=%s err=%v", resp.StatusCode, raw, readErr)
	}
	afterUpload, err := h.store.ListKV("fixture-provider")
	if err != nil || !reflect.DeepEqual(before, afterUpload) {
		t.Fatal("失败上传修改公共配置", err)
	}
	// OpenAI和标准文件创建保留原LLM鉴权与未实现状态，不变成管理权限。
	for _, path := range []string{"/v1/files", "/files", "/openai/v1/files"} {
		if out := h.do("POST", path, owner.key, map[string]any{}); out.status != 501 {
			t.Errorf("原文件边界改变: %s", out.describe())
		}
	}
	got := h.ok("POST", "/v1/chat/completions", owner.key, chatRequest("file-boundary", "Verify chat remains available after rejected file operations."))
	h.flushSpend()
	cost, _ := floatField(logDetail(t, h, admin, got.header("x-litellm-call-id")), "spend")
	if len(h.upstreamCalls()) != 1 || cost <= 0 {
		t.Fatal("正常推理与计费异常")
	}
	blocked := h.openScope(t, admin, "file-zero")
	h.setUserBudget(t, admin, blocked.userID, 0)
	if out := h.do("POST", "/v1/chat/completions", blocked.key, chatRequest("file-boundary", "Verify zero budget remains blocked.")); out.status != 403 || len(h.upstreamCalls()) != 1 {
		t.Fatal("预算拦截改变", out.describe())
	}
	for _, id := range []string{"files", "fixture-file", "content"} {
		if err := h.store.DeleteKV("fixture-provider", id); err != nil {
			t.Fatal(err)
		}
	}
}

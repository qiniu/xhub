package usage

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/sunqirui1987/xhub/internal/auth"
	"github.com/sunqirui1987/xhub/internal/config"
	"github.com/sunqirui1987/xhub/internal/iam"
)

// logOptionDeniedHost 模拟未登录用户；嵌入的其余方法不得调用，纯内存且无需清理。
type logOptionDeniedHost struct{ Host }

// RequireUser 为鉴权失败请求返回 401；参数为响应与请求，返回 nil，不读取数据。
func (logOptionDeniedHost) RequireUser(w http.ResponseWriter, _ *http.Request) *auth.Principal {
	w.WriteHeader(http.StatusUnauthorized)
	return nil
}

// TestLogFilterOptionsUnauthorizedNoStore 验证鉴权失败也禁止缓存；使用内存请求，无持久数据需清理。
func TestLogFilterOptionsUnauthorizedNoStore(t *testing.T) {
	w := httptest.NewRecorder()
	LogFilterOptions(logOptionDeniedHost{}, w, httptest.NewRequest(http.MethodGet, "/spend/logs/ui/filter-options", nil))
	if w.Code != http.StatusUnauthorized || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("未登录响应应返回 401 并禁止缓存：status=%d headers=%v", w.Code, w.Header())
	}
}

// TestLogOptionPairs 验证候选只取当前授权、启用且有实际模型的连接；纯内存测试，无清理需求。
func TestLogOptionPairs(t *testing.T) {
	entries := []config.ModelEntry{
		{ModelName: "a", LiteLLMParams: map[string]any{"litellm_credential_name": "one"}},
		{ModelName: "a", LiteLLMParams: map[string]any{"litellm_credential_name": "one"}},
		{ModelName: "a", LiteLLMParams: map[string]any{"litellm_credential_name": "disabled"}, ModelInfo: map[string]any{"disabled": true}},
		{ModelName: "b", LiteLLMParams: map[string]any{"litellm_credential_name": "two"}},
		{ModelName: "empty"},
		{ModelName: "inline", LiteLLMParams: map[string]any{"api_base": "https://u:secret@relay.example/v1?token=secret"}},
		{ModelName: "shell", LiteLLMParams: map[string]any{"litellm_credential_name": "shell"}, ModelInfo: map[string]any{"role": "provider"}},
	}
	for _, tc := range []struct {
		name    string
		allowed []string
		want    []iam.LogOptionDeployment
	}{
		{"one", []string{"a"}, []iam.LogOptionDeployment{{Model: "a", Supplier: "one"}}},
		{"empty denies", []string{}, []iam.LogOptionDeployment{}},
		{"empty supplier and sanitized URL", []string{"empty", "inline"}, []iam.LogOptionDeployment{{Model: "empty", Supplier: ""}, {Model: "inline", Supplier: "https://relay.example/v1"}}},
		{"unrestricted", nil, []iam.LogOptionDeployment{{Model: "a", Supplier: "one"}, {Model: "b", Supplier: "two"}, {Model: "empty", Supplier: ""}, {Model: "inline", Supplier: "https://relay.example/v1"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := logOptionPairs(entries, tc.allowed); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("授权候选错误：%+v；期望 %+v", got, tc.want)
			}
		})
	}
}

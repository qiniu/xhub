package identity

import (
	"errors"
	"github.com/sunqirui1987/xhub/internal/iam"
	"testing"
)

// TestEnterpriseIdentityInput 验证 HTTP 字段解析的省略、解绑、规范化和非法类型；纯内存测试，无资源需要清理。
func TestEnterpriseIdentityInput(t *testing.T) {
	for _, tc := range []struct {
		name            string
		body            map[string]any
		invalid, absent bool
	}{
		{name: "omitted", body: map[string]any{}, absent: true},
		{name: "unbind", body: map[string]any{"sso_connection_id": "", "sso_external_id": ""}},
		{name: "trim", body: map[string]any{"sso_connection_id": " connection ", "sso_external_id": " employee "}},
		{name: "missing", body: map[string]any{"sso_connection_id": "connection"}, invalid: true},
		{name: "null", body: map[string]any{"sso_connection_id": nil, "sso_external_id": "employee"}, invalid: true},
		{name: "number", body: map[string]any{"sso_connection_id": 1, "sso_external_id": "employee"}, invalid: true},
		{name: "partial blank", body: map[string]any{"sso_connection_id": " ", "sso_external_id": "employee"}, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := enterpriseIdentityInput(tc.body)
			if tc.invalid {
				if !errors.Is(err, iam.ErrInvalid) {
					t.Fatalf("非法字段未拒绝: %v", err)
				}
				return
			}
			if err != nil || (got == nil) != tc.absent {
				t.Fatalf("字段解析失败: %+v %v", got, err)
			}
			if tc.name == "trim" && (got.ConnectionID != "connection" || got.ExternalID != "employee") {
				t.Fatalf("未规范化: %+v", got)
			}
		})
	}
}

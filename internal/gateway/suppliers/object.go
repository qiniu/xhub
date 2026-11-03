package suppliers

import (
	"strings"
	"time"

	"github.com/sunqirui1987/xhub/internal/credentialfields"
	"github.com/sunqirui1987/xhub/internal/util"
)

// newID 为缺省名称生成旧格式的 credential_ 标识；创建、upsert 和规范化共用，无持久化副作用。
func newID() string { return "credential_" + util.CallID()[:12] }

// Normalize 就地补齐供应商公共字段并移除 password；服务和旧 Freeze 适配器调用。
// obj 必须非 nil；只对 nil/缺省字段补值，保留非 nil 扩展和已有创建时间，不写库。
func Normalize(obj map[string]any) {
	delete(obj, "password")
	if obj["id"] == nil {
		obj["id"] = newID()
	}
	if obj["created_at"] == nil {
		obj["created_at"] = time.Now().UTC().Format(time.RFC3339)
	}
	if obj["credential_name"] == nil {
		id, _ := obj["id"].(string)
		obj["credential_name"] = id
	}
	if obj["credential_info"] == nil {
		obj["credential_info"] = map[string]any{}
	}
}

// mergePatch 将普通字段覆盖到 dst，并按字段合并 credential_values；仅 Update 调用，无返回值。
// password 忽略；非对象凭据补丁忽略，空/非字符串/旧连续星号或当前值回显掩码保留旧值，普通字段允许 null 和零。
func mergePatch(dst, body map[string]any) {
	for k, v := range body {
		if k != "password" && k != "credential_values" {
			dst[k] = v
		}
	}
	incoming, ok := body["credential_values"].(map[string]any)
	if !ok {
		return
	}
	cur, _ := dst["credential_values"].(map[string]any)
	if cur == nil {
		cur = map[string]any{}
	}
	for k, v := range incoming {
		if credentialfields.Sensitive(k) {
			secret, _ := v.(string)
			if strings.TrimSpace(secret) == "" || strings.Contains(secret, "**") {
				continue
			}
			// 五字节秘密的回显只有一个星号，不能只检查连续 **。
			// 只与当前存储值的实际掩码比较，避免把其他含单个 * 的真实新 Key 一并忽略。
			if current, ok := cur[k].(string); ok && secret == maskSecret(current) {
				continue
			}
		}
		cur[k] = v
	}
	dst["credential_values"] = cur
}

// Redact 构造供应商响应；showValues 为 false 时省略对象凭据，为 true 时沿用旧字段掩码。
// 服务及兼容响应适配器调用；不修改 obj，非对象 credential_values 保持旧行为，不引入递归脱敏语义变更。
func Redact(obj map[string]any, showValues bool) map[string]any {
	vals, ok := obj["credential_values"].(map[string]any)
	if !ok {
		return obj
	}
	out := make(map[string]any, len(obj))
	for k, v := range obj {
		if k != "credential_values" {
			out[k] = v
		}
	}
	if showValues {
		masked := make(map[string]any, len(vals))
		for k, v := range vals {
			if secret, ok := v.(string); ok && credentialfields.Sensitive(k) {
				masked[k] = maskSecret(secret)
			} else {
				masked[k] = v
			}
		}
		out["credential_values"] = masked
	}
	return out
}

// maskSecret 沿用旧字节掩码：短秘密（含空串）变为五个星号，长秘密保留首尾各两字节。
// Redact 调用；输入只读，不对既有掩码规则做额外推测或格式迁移。
func maskSecret(v string) string {
	if len(v) <= 4 {
		return "*****"
	}
	return v[:2] + strings.Repeat("*", len(v)-4) + v[len(v)-2:]
}

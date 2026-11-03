package searchconfig

// Provider 描述已有搜索探测适配器；公开字段保持旧UI契约，Endpoint仅供服务端选择默认上游，不序列化。
type Provider struct {
	Name     string `json:"provider_name"`
	Label    string `json:"ui_friendly_name"`
	Endpoint string `json:"-"`
}

// Providers 返回当前有探测实现的供应商定义副本，供目录和执行配置共用；不读取配置、凭据或访问上游。
// 调用方可修改返回切片，不影响后续请求；增加供应商必须同步实现协议解析和真实探测回归。
func Providers() []Provider {
	return []Provider{
		{"tavily", "Tavily", "https://api.tavily.com/search"},
		{"perplexity", "Perplexity", "https://api.perplexity.ai/search"},
		{"serper", "Serper", "https://google.serper.dev/search"},
	}
}

// ProviderEndpoint 按已规范化的精确名称返回探测默认URL；未知/空值返回空串，由调用方维持原400校验。
// 供makeSearchProbeConfig调用，不做大小写或空白规范化，不读写状态。
func ProviderEndpoint(name string) string {
	for _, p := range Providers() {
		if p.Name == name {
			return p.Endpoint
		}
	}
	return ""
}

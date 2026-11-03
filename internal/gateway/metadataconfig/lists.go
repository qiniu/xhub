package metadataconfig

import "fmt"

// Reader 仅允许读取配置目录，便于对真实存储失败与字典转换独立测试；不提供写入或预算能力。
type Reader interface {
	ListKV(string) ([]map[string]any, error)
}

// ReadTagCatalog 读取与/tag/new、update、delete共用的tag命名空间，返回前端约定的名称字典。
// st为非nil读取器；正常空库返回{}，存储或损坏/重名记录返回错误而非静默丢失配置。仅浅拷贝顶层，不修改存储行或解释额度/模型字段。
func ReadTagCatalog(st Reader) (map[string]any, error) {
	rows, err := st.ListKV("tag")
	if err != nil {
		return nil, err
	}
	out := make(map[string]any, len(rows))
	for _, row := range rows {
		name, _ := row["name"].(string)
		if name == "" {
			name, _ = row["id"].(string)
		}
		if name == "" {
			return nil, fmt.Errorf("tag record has no name")
		}
		if _, exists := out[name]; exists {
			return nil, fmt.Errorf("duplicate tag name")
		}
		item := make(map[string]any, len(row)+1)
		for k, v := range row {
			if k != "password" {
				item[k] = v
			}
		}
		item["name"] = name
		out[name] = item
	}
	return out, nil
}

// CustomerList 读取customer/end_user两个独立空间并投影旧列表四字段；st非nil，返回非nil数组或错误。
// 管理列表调用；任一读取失败不返回部分成功，同ID两行保持独立，不把元数据预算应用到真实账号。
func CustomerList(st Reader) ([]map[string]any, error) {
	out := []map[string]any{}
	for _, kind := range []string{"customer", "end_user"} {
		rows, err := st.ListKV(kind)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			item, err := copyObject(row)
			if err != nil {
				return nil, err
			}
			Normalize("customer", item)
			out = append(out, map[string]any{"user_id": item["user_id"], "spend": item["spend"], "blocked": item["blocked"], "max_budget": item["max_budget"]})
		}
	}
	return out, nil
}

package config

import "reflect"

// CloneModels 复制配置目录及其中的容器，供网关在模型锁内建立请求私有视图。
// 参数 models 为已加载的 YAML/JSON 配置树；返回保持类型、nil、数字精度的独立目录，不修改输入。
// 配置树不含循环引用；标量和 Rate 等值结构直接复制。不序列化，非法值仍留给原有校验拒绝。
// 本方法不加锁、不读取存储，不包含实时权限、预算或命名凭据；调用方负责同步源目录。
func CloneModels(models []ModelEntry) []ModelEntry {
	if models == nil {
		return nil
	}
	out := make([]ModelEntry, len(models))
	for i, model := range models {
		out[i] = model
		out[i].LiteLLMParams = cloneConfigValue(reflect.ValueOf(model.LiteLLMParams)).Interface().(map[string]any)
		out[i].ModelInfo = cloneConfigValue(reflect.ValueOf(model.ModelInfo)).Interface().(map[string]any)
	}
	return out
}

// cloneConfigValue 递归复制配置树中的 map、slice 和 interface，保留 YAML/JSON 及内部类型化切片的具体类型。
// 参数 v 为有效反射值，返回同类型值；仅由 CloneModels 递归调用，无外部副作用。
// 标量（包括 json.Number）和不可序列化值原样保留，不能通过 JSON 往返把大整数变成 float64 或吞掉校验错误。
func cloneConfigValue(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(cloneConfigValue(v.Elem()))
		return out
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out.SetMapIndex(iter.Key(), cloneConfigValue(iter.Value()))
		}
		return out
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(cloneConfigValue(v.Index(i)))
		}
		return out
	default:
		return v
	}
}

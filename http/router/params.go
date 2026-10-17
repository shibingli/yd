package router

// Param is a captured path parameter. | Param 是一个已捕获的路径参数。
type Param struct {
	// Key is the parameter name. | Key 为参数名。
	Key string
	// Value is the captured raw value (no percent-decoding, the original encoding is kept). | Value 为捕获到的原始值（不做百分号解码，保留原始编码）。
	Value string
}

// Params holds all path parameters captured for a single request. | Params 持有单次请求捕获到的全部路径参数。
//
// Params is owned by the pooled Ctx and is only valid for the lifetime of the current request;
// copy the strings yourself if you need to keep them after the request ends. | Params 由池化的
// Ctx 持有，仅在当前请求的生命周期内有效；若需在请求结束后继续持有，请自行复制字符串。
type Params struct {
	ps []Param
	// shared marks a read-only view borrowed from the route cache. Append clones before the
	// first mutation so a handler can never corrupt another request's cached captures. | shared
	// 标记从路由缓存借用的只读视图。Append 在首次修改前克隆，处理器因此无法破坏其它请求的缓存捕获。
	shared bool
}

// Len returns the number of captured parameters. | Len 返回已捕获参数的个数。
func (p *Params) Len() int { return len(p.ps) }

// Reset clears the captured parameters but keeps the underlying capacity, for object pool reuse.
// A borrowed cache view stays marked as shared: its backing array still belongs to the cache,
// so the next Append must copy before writing. | Reset 清空已捕获参数但保留底层容量，供对象池复用。
// 借用的缓存视图仍保持共享标记：其底层数组属于缓存，下一次 Append 必须先复制再写入。
func (p *Params) Reset() {
	p.ps = p.ps[:0]
}

// Append appends one parameter capture. | Append 追加一个参数捕获。
func (p *Params) Append(key, value string) {
	if p.shared {
		// Copy the cached view before the first mutation. | 首次修改前复制缓存视图。
		owned := make([]Param, len(p.ps), len(p.ps)+1)
		copy(owned, p.ps)
		p.ps = owned
		p.shared = false
	}
	p.ps = append(p.ps, Param{Key: key, Value: value})
}

// Truncate rolls the parameter count back to n, used to roll back when backtracking matching fails. | Truncate 把参数数量回退到 n，供回溯匹配失败时回滚。
func (p *Params) Truncate(n int) {
	if n < 0 {
		n = 0
	}
	if n > len(p.ps) {
		return
	}
	p.ps = p.ps[:n]
}

// Get returns the value of the named parameter, or an empty string when it does not exist. | Get 返回指定名称参数的值，不存在时返回空字符串。
func (p *Params) Get(name string) string {
	for i := range p.ps {
		if p.ps[i].Key == name {
			return p.ps[i].Value
		}
	}
	return ""
}

// Has reports whether the named parameter exists (its value may be empty). | Has 判断指定名称的参数是否存在（值允许为空）。
func (p *Params) Has(name string) bool {
	for i := range p.ps {
		if p.ps[i].Key == name {
			return true
		}
	}
	return false
}

// Each iterates over the parameters in capture order. | Each 按捕获顺序遍历参数。
func (p *Params) Each(fn func(key, value string)) {
	for i := range p.ps {
		fn(p.ps[i].Key, p.ps[i].Value)
	}
}

// Keys returns all parameter names (a new slice, in capture order). | Keys 返回全部参数名（新切片，按捕获顺序）。
func (p *Params) Keys() []string {
	keys := make([]string, len(p.ps))
	for i := range p.ps {
		keys[i] = p.ps[i].Key
	}
	return keys
}

// Values returns all parameter values (a new slice, in capture order). | Values 返回全部参数值（新切片，按捕获顺序）。
func (p *Params) Values() []string {
	vals := make([]string, len(p.ps))
	for i := range p.ps {
		vals[i] = p.ps[i].Value
	}
	return vals
}

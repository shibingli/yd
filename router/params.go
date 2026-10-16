package router

import "sync"

// Param 是一个已捕获的路径参数。
type Param struct {
	// Key 为参数名。
	Key string
	// Value 为捕获到的原始值（不做百分号解码，保留原始编码）。
	Value string
}

// Params 持有单次请求捕获到的全部路径参数。
//
// Params 由 sync.Pool 复用，仅在当前请求的生命周期内有效；
// 若需在请求结束后继续持有，请自行复制字符串。
type Params struct {
	ps []Param
}

// Len 返回已捕获参数的个数。
func (p *Params) Len() int { return len(p.ps) }

// Reset 清空已捕获参数但保留底层容量，供对象池复用。
func (p *Params) Reset() { p.ps = p.ps[:0] }

// Append 追加一个参数捕获。
func (p *Params) Append(key, value string) {
	p.ps = append(p.ps, Param{Key: key, Value: value})
}

// Truncate 把参数数量回退到 n，供回溯匹配失败时回滚。
func (p *Params) Truncate(n int) {
	if n < 0 {
		n = 0
	}
	if n > len(p.ps) {
		return
	}
	p.ps = p.ps[:n]
}

// Get 返回指定名称参数的值，不存在时返回空字符串。
func (p *Params) Get(name string) string {
	for i := range p.ps {
		if p.ps[i].Key == name {
			return p.ps[i].Value
		}
	}
	return ""
}

// Has 判断指定名称的参数是否存在（值允许为空）。
func (p *Params) Has(name string) bool {
	for i := range p.ps {
		if p.ps[i].Key == name {
			return true
		}
	}
	return false
}

// Each 按捕获顺序遍历参数。
func (p *Params) Each(fn func(key, value string)) {
	for i := range p.ps {
		fn(p.ps[i].Key, p.ps[i].Value)
	}
}

// Keys 返回全部参数名（新切片，按捕获顺序）。
func (p *Params) Keys() []string {
	keys := make([]string, len(p.ps))
	for i := range p.ps {
		keys[i] = p.ps[i].Key
	}
	return keys
}

// Values 返回全部参数值（新切片，按捕获顺序）。
func (p *Params) Values() []string {
	vals := make([]string, len(p.ps))
	for i := range p.ps {
		vals[i] = p.ps[i].Value
	}
	return vals
}

var paramsPool = sync.Pool{
	New: func() any {
		return &Params{ps: make([]Param, 0, 8)}
	},
}

// acquireParams 从对象池取一个空的 Params。
func acquireParams() *Params {
	p := paramsPool.Get().(*Params)
	p.Reset()
	return p
}

// releaseParams 把 Params 归还对象池。
func releaseParams(p *Params) {
	if p == nil {
		return
	}
	// 防止异常大的参数切片长期占用内存
	if cap(p.ps) > 64 {
		return
	}
	p.Reset()
	paramsPool.Put(p)
}

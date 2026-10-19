package render

import "github.com/shibingli/yd/core/errx"

// Sentinel errors for template loading. | 模板加载相关的哨兵错误。
var (
	// ErrReadTemplate reports an unreadable template file. | ErrReadTemplate 表示模板文件不可读。
	ErrReadTemplate = errx.New("cannot read template | 无法读取模板文件")
	// ErrParseTemplate reports a template syntax error. | ErrParseTemplate 表示模板语法错误。
	ErrParseTemplate = errx.New("cannot parse template | 模板解析失败")
	// ErrWalkTemplates reports a failure while walking the template root. | ErrWalkTemplates 表示遍历模板根目录失败。
	ErrWalkTemplates = errx.New("cannot walk templates | 遍历模板目录失败")
	// ErrExecuteTemplate reports a failure while executing a template. | ErrExecuteTemplate 表示模板执行失败。
	ErrExecuteTemplate = errx.New("cannot execute template | 模板执行失败")
	// ErrCloneTemplate reports a failure while cloning the template set for one execution. | ErrCloneTemplate 表示克隆模板集失败（用于单次执行）。
	ErrCloneTemplate = errx.New("cannot clone template set | 无法克隆模板集")
	// ErrAssetNotFound reports a missing asset for the asset helper. | ErrAssetNotFound 表示 asset 辅助函数找不到资源。
	ErrAssetNotFound = errx.New("asset not found | 资源不存在")
	// ErrDictOddArgs reports an odd argument count for the dict helper. | ErrDictOddArgs 表示 dict 辅助函数的参数个数为奇数。
	ErrDictOddArgs = errx.New("dict requires an even number of arguments | dict 需要偶数个参数")
	// ErrDictKeyNotString reports a non-string key for the dict helper. | ErrDictKeyNotString 表示 dict 辅助函数的键不是字符串。
	ErrDictKeyNotString = errx.New("dict keys must be strings | dict 的键必须是字符串")
	// ErrJSONMarshal reports a JSON encoding failure in templates. | ErrJSONMarshal 表示模板内 JSON 编码失败。
	ErrJSONMarshal = errx.New("cannot encode value as JSON | 无法将值编码为 JSON")
	// ErrHtmxOddArgs reports an odd argument count for the hx helper. | ErrHtmxOddArgs 表示 hx 辅助函数的参数个数为奇数。
	ErrHtmxOddArgs = errx.New("hx requires an even number of arguments | hx 需要偶数个参数")
	// ErrHtmxKeyNotString reports a non-string key for the hx helper. | ErrHtmxKeyNotString 表示 hx 辅助函数的键不是字符串。
	ErrHtmxKeyNotString = errx.New("hx keys must be strings | hx 的键必须是字符串")
	// ErrHtmxInvalidKey reports an attribute name the hx helper cannot emit safely. | ErrHtmxInvalidKey 表示 hx 辅助函数无法安全输出的属性名。
	ErrHtmxInvalidKey = errx.New("hx key is not a plain attribute name | hx 的键不是普通属性名")
	// ErrInvalidFunc reports a template function with an unusable signature. | ErrInvalidFunc 表示模板函数签名不可用。
	ErrInvalidFunc = errx.New("invalid template function | 非法的模板函数")
)

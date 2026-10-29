// This file implements the YAML node diff and merge primitives used by the managed configuration
// sync. It deliberately knows nothing about sync modes or report text.
//
// 本文件实现受管理配置同步使用的 YAML 节点差异与合并原语，刻意不涉及同步模式或报告文案。

package cli

import (
	"bytes"
	"sort"
	"strings"

	"github.com/shibingli/ydx/errs"
	"go.yaml.in/yaml/v3"
)

// configDiff describes how one YAML mapping differs from another. | configDiff 描述一个 YAML 映射
// 与另一个映射的差异。
type configDiff struct {
	// Overrides is the mapping of values from the current side that differ. | Overrides 是当前侧不同
	// 值的映射。
	Overrides *yaml.Node
	// Added lists paths present only in the current side. | Added 列出仅存在于当前侧的路径。
	Added []string
	// Removed lists paths present only in the base side. | Removed 列出仅存在于基准侧的路径。
	Removed []string
	// Changed lists paths present on both sides with different values. | Changed 列出两侧都存在但值
	// 不同的路径。
	Changed []string
}

// diffConfig compares two YAML documents. The first document is the base side and the second is the
// current side. | diffConfig 比较两个 YAML 文档；第一个是基准侧，第二个是当前侧。
func diffConfig(baseDoc, currentDoc *yaml.Node) (*configDiff, error) {
	baseRoot, err := requireConfigMapping(baseDoc, syncConfigBaselineRel)
	if err != nil {
		return nil, err
	}
	currentRoot, err := requireConfigMapping(currentDoc, syncConfigBaseRel)
	if err != nil {
		return nil, err
	}
	diff := &configDiff{Overrides: newConfigMapping()}
	if err := diffConfigMapping(baseRoot, currentRoot, nil, diff); err != nil {
		return nil, err
	}
	diff.Added = sortedConfigPaths(diff.Added)
	diff.Removed = sortedConfigPaths(diff.Removed)
	diff.Changed = sortedConfigPaths(diff.Changed)
	return diff, nil
}

// diffConfigMapping walks two mappings recursively. | diffConfigMapping 递归遍历两个映射。
func diffConfigMapping(base, current *yaml.Node, keys []string, diff *configDiff) error {
	baseMap, _, err := configMapping(base)
	if err != nil {
		return err
	}
	currentMap, currentOrder, err := configMapping(current)
	if err != nil {
		return err
	}
	for _, key := range currentOrder {
		baseValue, ok := baseMap[key]
		currentValue := currentMap[key]
		path := appendConfigKey(keys, key)
		if !ok {
			diff.Added = append(diff.Added, configPath(path))
			insertConfigOverride(diff.Overrides, path, currentValue)
			continue
		}
		baseResolved := resolveConfigNode(baseValue)
		currentResolved := resolveConfigNode(currentValue)
		if baseResolved.Kind == yaml.MappingNode && currentResolved.Kind == yaml.MappingNode {
			if err := diffConfigMapping(baseResolved, currentResolved, path, diff); err != nil {
				return err
			}
			continue
		}
		if configNodeEqual(baseResolved, currentResolved) {
			continue
		}
		diff.Changed = append(diff.Changed, configPath(path))
		insertConfigOverride(diff.Overrides, path, currentValue)
	}
	for key := range baseMap {
		if _, ok := currentMap[key]; !ok {
			diff.Removed = append(diff.Removed, configPath(appendConfigKey(keys, key)))
		}
	}
	return nil
}

// mergeLocalConfig merges override nodes into a local YAML document. Local values always win and a
// value conflict is reported by path only. | mergeLocalConfig 把覆盖节点合并进本地 YAML 文档。
// 本地值始终优先，值冲突只按路径报告。
func mergeLocalConfig(localDoc *yaml.Node, overrides *yaml.Node) ([]byte, []string, []string, bool, error) {
	localRoot, err := requireConfigMapping(localDoc, syncConfigLocalRel)
	if err != nil {
		return nil, nil, nil, false, err
	}
	overrideRoot, err := requireConfigMapping(overrides, syncConfigLocalRel)
	if err != nil {
		return nil, nil, nil, false, err
	}
	var conflicts, moved []string
	changed, err := mergeConfigMapping(localRoot, overrideRoot, nil, &moved, &conflicts)
	if err != nil {
		return nil, nil, nil, false, err
	}
	if !changed {
		return nil, sortedConfigPaths(moved), sortedConfigPaths(conflicts), false, nil
	}
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(localDoc); err != nil {
		return nil, nil, nil, false, errs.WrapDetail(err, ErrSyncConfig, "encode %[1]s | 编码 %[1]s", syncConfigLocalRel)
	}
	if err := encoder.Close(); err != nil {
		return nil, nil, nil, false, errs.WrapDetail(err, ErrSyncConfig, "close %[1]s | 关闭 %[1]s", syncConfigLocalRel)
	}
	return buffer.Bytes(), sortedConfigPaths(moved), sortedConfigPaths(conflicts), true, nil
}

// mergeConfigMapping merges override mappings into local. | mergeConfigMapping 把覆盖映射合并进本地
// 映射。
func mergeConfigMapping(local, override *yaml.Node, keys []string, moved, conflicts *[]string) (bool, error) {
	localMap, _, err := configMapping(local)
	if err != nil {
		return false, err
	}
	overrideMap, overrideOrder, err := configMapping(override)
	if err != nil {
		return false, err
	}
	changed := false
	for _, key := range overrideOrder {
		path := appendConfigKey(keys, key)
		localValue, ok := localMap[key]
		overrideValue := overrideMap[key]
		if !ok {
			appendConfigPair(local, key, overrideValue)
			collectConfigLeafPaths(resolveConfigNode(overrideValue), path, moved)
			changed = true
			continue
		}
		localResolved := resolveConfigNode(localValue)
		overrideResolved := resolveConfigNode(overrideValue)
		if localResolved.Kind == yaml.MappingNode && overrideResolved.Kind == yaml.MappingNode {
			childChanged, mergeErr := mergeConfigMapping(localResolved, overrideResolved, path, moved, conflicts)
			if mergeErr != nil {
				return false, mergeErr
			}
			if childChanged {
				changed = true
			}
			continue
		}
		if configNodeEqual(localResolved, overrideResolved) {
			continue
		}
		*conflicts = append(*conflicts, configPath(path))
	}
	return changed, nil
}

// collectConfigLeafPaths records the leaf paths of an added value so the report names keys rather
// than only their section. | collectConfigLeafPaths 记录新增值的叶子路径，使报告给出具体键名而不是
// 只给出配置段。
func collectConfigLeafPaths(node *yaml.Node, keys []string, out *[]string) {
	node = resolveConfigNode(node)
	if node == nil {
		*out = append(*out, configPath(keys))
		return
	}
	if node.Kind != yaml.MappingNode {
		*out = append(*out, configPath(keys))
		return
	}
	mapping, order, err := configMapping(node)
	if err != nil || len(order) == 0 {
		*out = append(*out, configPath(keys))
		return
	}
	for _, key := range order {
		collectConfigLeafPaths(mapping[key], appendConfigKey(keys, key), out)
	}
}

// parseConfigDocument parses one YAML document and requires a top-level mapping. | parseConfigDocument
// 解析一个 YAML 文档，并要求顶层是映射。
func parseConfigDocument(data []byte, rel string) (*yaml.Node, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return newConfigDocument(false), nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, errs.WrapDetail(err, ErrSyncConfig, "parse %[1]s | 解析 %[1]s", rel)
	}
	if doc.Kind == 0 {
		return newConfigDocument(false), nil
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 {
		return nil, errs.Detail(ErrSyncConfig, "%[1]s must contain one YAML document | %[1]s 必须包含单个 YAML 文档", rel)
	}
	root := resolveConfigNode(doc.Content[0])
	if root == nil || root.Kind != yaml.MappingNode {
		return nil, errs.Detail(ErrSyncConfig, "%[1]s top-level must be a mapping | %[1]s 顶层必须是映射", rel)
	}
	return &doc, nil
}

// requireConfigMapping returns the top-level mapping of a document. | requireConfigMapping 返回文档的
// 顶层映射。
func requireConfigMapping(doc *yaml.Node, rel string) (*yaml.Node, error) {
	root := configRoot(doc)
	if root == nil || root.Kind != yaml.MappingNode {
		return nil, errs.Detail(ErrSyncConfig, "%[1]s top-level must be a mapping | %[1]s 顶层必须是映射", rel)
	}
	return root, nil
}

// configRoot returns the resolved root node of a document. | configRoot 返回文档解析后的根节点。
func configRoot(doc *yaml.Node) *yaml.Node {
	if doc == nil {
		return nil
	}
	if doc.Kind == yaml.DocumentNode {
		if len(doc.Content) == 0 {
			return nil
		}
		return resolveConfigNode(doc.Content[0])
	}
	return resolveConfigNode(doc)
}

// resolveConfigNode follows alias nodes to their target. | resolveConfigNode 沿别名节点找到目标。
func resolveConfigNode(node *yaml.Node) *yaml.Node {
	for i := 0; node != nil && node.Kind == yaml.AliasNode && i < 64; i++ {
		node = node.Alias
	}
	return node
}

// configMapping indexes a mapping by key and keeps its original key order. | configMapping 按键索引
// 一个映射，并保留原始键顺序。
func configMapping(node *yaml.Node) (map[string]*yaml.Node, []string, error) {
	node = resolveConfigNode(node)
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, nil, errs.Detail(ErrSyncConfig, "expected a YAML mapping | 期望 YAML 映射")
	}
	index := make(map[string]*yaml.Node, len(node.Content)/2)
	order := make([]string, 0, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := resolveConfigNode(node.Content[i])
		if key == nil || key.Kind != yaml.ScalarNode {
			return nil, nil, errs.Detail(ErrSyncConfig, "YAML mapping keys must be scalars | YAML 映射的键必须是标量")
		}
		if _, ok := index[key.Value]; !ok {
			order = append(order, key.Value)
		}
		index[key.Value] = node.Content[i+1]
	}
	return index, order, nil
}

// configNodeEqual compares two YAML nodes semantically, ignoring comments, styles and positions. |
// configNodeEqual 语义比较两个 YAML 节点，忽略注释、样式与位置。
func configNodeEqual(a, b *yaml.Node) bool {
	a = resolveConfigNode(a)
	b = resolveConfigNode(b)
	if a == nil || b == nil {
		return a == b
	}
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case yaml.DocumentNode:
		if len(a.Content) == 0 || len(b.Content) == 0 {
			return len(a.Content) == len(b.Content)
		}
		return configNodeEqual(a.Content[0], b.Content[0])
	case yaml.MappingNode:
		am, _, err := configMapping(a)
		if err != nil {
			return false
		}
		bm, _, err := configMapping(b)
		if err != nil || len(am) != len(bm) {
			return false
		}
		for key, av := range am {
			bv, ok := bm[key]
			if !ok || !configNodeEqual(av, bv) {
				return false
			}
		}
		return true
	case yaml.SequenceNode:
		if len(a.Content) != len(b.Content) {
			return false
		}
		for i := range a.Content {
			if !configNodeEqual(a.Content[i], b.Content[i]) {
				return false
			}
		}
		return true
	default:
		return a.Tag == b.Tag && a.Value == b.Value
	}
}

// insertConfigOverride stores one value at a nested path in the override tree. | insertConfigOverride
// 在覆盖树中按嵌套路径保存一个值。
func insertConfigOverride(root *yaml.Node, keys []string, value *yaml.Node) {
	if root == nil || len(keys) == 0 {
		return
	}
	current := root
	for i, key := range keys {
		if i == len(keys)-1 {
			setConfigMappingValue(current, key, value)
			return
		}
		child := configMappingValue(current, key)
		child = resolveConfigNode(child)
		if child == nil || child.Kind != yaml.MappingNode {
			setConfigMappingValue(current, key, newConfigMapping())
			// setConfigMappingValue clones its value, so re-read the stored child instead of
			// mutating the detached temporary. | setConfigMappingValue 会克隆值，因此必须重新读取
			// 已存储的子节点，而不是继续修改那个未挂载的临时节点。
			child = resolveConfigNode(configMappingValue(current, key))
		}
		current = child
	}
}

// configMappingValue returns one mapping value by key. | configMappingValue 按键返回一个映射值。
func configMappingValue(mapping *yaml.Node, key string) *yaml.Node {
	mapping = resolveConfigNode(mapping)
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if keyNode := resolveConfigNode(mapping.Content[i]); keyNode != nil && keyNode.Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

// setConfigMappingValue replaces or appends one mapping value. | setConfigMappingValue 替换或追加
// 一个映射值。
func setConfigMappingValue(mapping *yaml.Node, key string, value *yaml.Node) {
	mapping = resolveConfigNode(mapping)
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if keyNode := resolveConfigNode(mapping.Content[i]); keyNode != nil && keyNode.Value == key {
			mapping.Content[i+1] = cloneConfigNode(value)
			return
		}
	}
	mapping.Content = append(mapping.Content, configKeyNode(key), cloneConfigNode(value))
}

// appendConfigPair appends one cloned key/value pair to a local mapping. | appendConfigPair 向本地
// 映射追加一对克隆的键与值。
func appendConfigPair(mapping *yaml.Node, key string, value *yaml.Node) {
	mapping = resolveConfigNode(mapping)
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return
	}
	mapping.Content = append(mapping.Content, configKeyNode(key), cloneConfigNode(value))
}

// configKeyNode creates a plain string mapping key. | configKeyNode 创建一个普通字符串映射键。
func configKeyNode(key string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
}

// cloneConfigNode deep-copies a YAML node, resolving aliases and dropping anchors. | cloneConfigNode
// 深拷贝一个 YAML 节点，解析别名并丢弃锚点。
func cloneConfigNode(node *yaml.Node) *yaml.Node {
	node = resolveConfigNode(node)
	if node == nil {
		return nil
	}
	cloned := *node
	cloned.Anchor = ""
	cloned.Alias = nil
	cloned.Content = make([]*yaml.Node, len(node.Content))
	for i, child := range node.Content {
		cloned.Content[i] = cloneConfigNode(child)
	}
	return &cloned
}

// newConfigMapping returns an empty YAML mapping. | newConfigMapping 返回空 YAML 映射。
func newConfigMapping() *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
}

// newConfigDocument returns a document with an empty mapping; withHeader adds the generated-file
// banner used for a newly created local override. | newConfigDocument 返回包含空映射的文档；
// withHeader 为新建的本地覆盖文件添加生成标记。
func newConfigDocument(withHeader bool) *yaml.Node {
	root := newConfigMapping()
	if withHeader {
		root.HeadComment = "Generated by yd sync from configs/config.yaml overrides. | 由 yd sync 从 configs/config.yaml 的覆盖项生成。\n" +
			"This file overrides configs/config.yaml and is never committed. | 本文件覆盖 configs/config.yaml，且不入库。"
	}
	return &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}
}

// appendConfigKey returns a new path slice with key appended. | appendConfigKey 返回追加 key 的新
// 路径切片。
func appendConfigKey(keys []string, key string) []string {
	next := make([]string, len(keys)+1)
	copy(next, keys)
	next[len(keys)] = key
	return next
}

// configPath renders a key path for reports; only key names are included. | configPath 把键路径
// 渲染为报告文本；只包含键名。
func configPath(keys []string) string {
	return strings.Join(keys, ".")
}

// sortedConfigPaths sorts and de-duplicates report paths. | sortedConfigPaths 排序并去重报告路径。
func sortedConfigPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(paths))
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

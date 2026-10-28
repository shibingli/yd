package dictionary

import (
	"context"
	"time"
)

// Store persists dictionary types and their items. | Store 持久化数据字典类型及其字典项。
//
// Every method takes the tenant explicitly: the tenant is the hard boundary and no query may cross
// it. The store owns the transactional rules that must be atomic (default-item exclusivity, the
// cascade of a type deletion) and reports the domain sentinels defined in this package. |
// 每个方法都显式接收租户：租户是硬边界，任何查询都不得跨越它。存储负责必须原子的规则
// （默认项互斥、类型删除的级联），并返回本包定义的领域哨兵。
type Store interface {
	// CreateType inserts one dictionary type. | CreateType 插入一个数据字典类型。
	CreateType(ctx context.Context, t Type) error
	// UpdateType replaces the editable fields of a type. | UpdateType 覆盖类型的可编辑字段。
	UpdateType(ctx context.Context, t Type) error
	// GetType reads one type inside the tenant. | GetType 读取租户内的一个类型。
	GetType(ctx context.Context, tenantID, id string) (Type, error)
	// GetTypeByCode reads one type by its immutable code inside the tenant. | GetTypeByCode 按不可变编码读取租户内的一个类型。
	GetTypeByCode(ctx context.Context, tenantID, code string) (Type, error)
	// ListTypes returns one page of types and its total. | ListTypes 返回一页类型与总数。
	ListTypes(ctx context.Context, tenantID string, filter Filter) ([]Type, int, error)
	// DeleteType deletes one type and every item it owns in one transaction. | DeleteType 在一个事务内删除一个类型及其全部字典项。
	DeleteType(ctx context.Context, tenantID, id string) error

	// CreateItem inserts one item. It clears the other defaults in the same transaction when the
	// item is the default, and re-checks maxItems under the type row lock so concurrent writers
	// cannot exceed the ceiling. A non-positive maxItems disables the limit. |
	// CreateItem 插入一个字典项；当该项为默认项时在同一事务内清除其余默认标记，并在类型行锁下重新
	// 检查 maxItems，使并发写入不能突破上限。maxItems 非正数时不限制。
	CreateItem(ctx context.Context, item Item, maxItems int) error
	// UpdateItem replaces the editable fields of an item. | UpdateItem 覆盖字典项的可编辑字段。
	UpdateItem(ctx context.Context, item Item) error
	// GetItem reads one item inside the tenant and its type. | GetItem 读取租户内某个类型的一个字典项。
	GetItem(ctx context.Context, tenantID, typeID, id string) (Item, error)
	// GetItemByValue reads one item by its immutable value. | GetItemByValue 按不可变值读取一个字典项。
	GetItemByValue(ctx context.Context, tenantID, typeID, value string) (Item, error)
	// ListItems returns one page of items and its total. | ListItems 返回一页字典项与总数。
	ListItems(ctx context.Context, tenantID, typeID string, filter ItemFilter) ([]Item, int, error)
	// DeleteItem deletes one item. | DeleteItem 删除一个字典项。
	DeleteItem(ctx context.Context, tenantID, typeID, id string) error
	// SetItemStatus changes one item's status and clears its default flag when it is disabled. |
	// SetItemStatus 修改字典项状态，并在停用时清除默认标记。
	SetItemStatus(ctx context.Context, tenantID, typeID, itemID string, status Status, at time.Time) error

	// Resolve loads enabled types and their enabled items for the given codes in one consistent
	// read. Codes without an enabled type are absent from the result. | Resolve 在一次一致读取中加载
	// 给定编码的启用类型及其启用字典项；没有启用类型的编码不出现在结果中。
	Resolve(ctx context.Context, tenantID string, codes []string) (map[string]Snapshot, error)
}

package gormx

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"gorm.io/gorm/schema"
)

// Fixed quantities of the naming strategy. | 命名策略的固定量。
const (
	// TablePrefix is the prefix of every table the framework names: the structural type User lands in yd_users,
	// never in users, so a framework table can never collide with a table the application writes itself. GORM
	// applies the prefix only to tables it names by itself, which is why a table whose name is pinned by a
	// TableName() method carries the very same prefix by hand (see security/auth/gormstore and its yd_auth_* tables). |
	// TablePrefix 是框架命名的每一张表的前缀：结构体 User 落在 yd_users，而不是 users，因此框架的表永远不会
	// 与使用方自己写的表撞名。GORM 只把它施加于自己命名的表，因此由 TableName() 固定表名的表必须手写同一个
	// 前缀（见 security/auth/gormstore 及其 yd_auth_* 系列表）。
	TablePrefix = "yd_"
	// IndexPrefix is the prefix of every index name this strategy derives. | IndexPrefix 是本策略派生的每一个索引名的前缀。
	IndexPrefix = "yd_idx_"
	// IdentifierMaxLength is the longest identifier this strategy emits, counted in bytes: PostgreSQL refuses a
	// name beyond 63 bytes, and the strategy must not hand the database a name it has to reject. | IdentifierMaxLength
	// 是本策略产出的标识符上限，按字节计：PostgreSQL 拒绝超过 63 字节的名字，本策略不得把一个数据库必然会
	// 拒绝的名字交给它。
	IdentifierMaxLength = 63
	// indexHashLength is how many hex characters of the name hash a truncated index name ends with. | indexHashLength 是截断后的索引名结尾携带的名称散列十六进制字符数。
	indexHashLength = 8
)

// NamingStrategy 是框架的 GORM 命名策略：表名前缀 yd_，以及索引名的 yd_idx_ 形态。
//
// NamingStrategy is the framework's GORM naming strategy: the yd_ table prefix and the yd_idx_ shape of an index
// name. It embeds GORM's stock strategy, so every rule it does not state itself (snake case, pluralisation, column
// naming, the fk/uni/chk constraint forms) stays exactly as GORM defines it. | 它内嵌 GORM 原生策略，
// 因此除自己写明的那两条之外的每一条规则（蛇形命名、复数化、列名、fk/uni/chk 约束名形态）都与 GORM 的定义
// 完全一致。
type NamingStrategy struct {
	schema.NamingStrategy
}

// DefaultNamingStrategy returns the framework naming strategy, ready to be handed to gorm.Config.NamingStrategy. |
// DefaultNamingStrategy 返回框架命名策略，可直接交给 gorm.Config.NamingStrategy。
func DefaultNamingStrategy() NamingStrategy {
	return NamingStrategy{schema.NamingStrategy{
		TablePrefix:         TablePrefix,
		IdentifierMaxLength: IdentifierMaxLength,
	}}
}

// IndexName 返回框架为一列派生出的索引名。
//
// The form is IndexPrefix + the logical table name + "_" + column, where the logical table name is the physical
// table name with TablePrefix removed: the table yd_auth_users over tenant_id yields yd_idx_auth_users_tenant_id.
// Spelling it idx_yd_auth_users_tenant_id would not start with yd_ at all, which is exactly the requirement it
// would fail; spelling it yd_idx_yd_auth_users_tenant_id would repeat the prefix. The chosen form starts with yd_
// and still names the table exactly once, so one look at an index tells which table it belongs to. |
// 形态是 IndexPrefix + 逻辑表名 + "_" + 列名，其中逻辑表名是物理表名去掉 TablePrefix：表 yd_auth_users 上
// 的 tenant_id 得到 yd_idx_auth_users_tenant_id。写成 idx_yd_auth_users_tenant_id 根本不以 yd_ 开头，
// 恰好违背了它要满足的那条要求；写成 yd_idx_yd_auth_users_tenant_id 则把前缀重复了一遍。所选形态以 yd_ 开头，
// 并且只写一次表名，因此看一眼索引就知道它属于哪张表。
//
// The column goes through the embedded strategy's column naming, so the derived name matches the column the same
// strategy would create. A table that does not carry the prefix (one pinned by an explicit TableName()) keeps its
// name as it is and still gets the yd_idx_ prefix. | 列名经过内嵌策略的列名规则，因此派生名与同一策略建出的列
// 名一致。不带前缀的表（表名由 TableName() 显式固定的那种）保持原名，索引依旧带 yd_idx_ 前缀。
//
// A name longer than IdentifierMaxLength is truncated with a hash suffix — see truncate. | 超过 IdentifierMaxLength
// 的名字按 truncate 的规则截断并追加散列。
func (n NamingStrategy) IndexName(table, column string) string {
	prefix := n.TablePrefix
	if prefix == "" {
		prefix = TablePrefix
	}
	return n.truncate(IndexPrefix + strings.TrimPrefix(table, prefix) + "_" + n.ColumnName(table, column))
}

// truncate 把超出 IdentifierMaxLength 的名字截短，并以名称散列收尾：结果长度恰好是上限，且完整名字不同，
// 结果就不同。
//
// A plain cut would map every long name sharing its first bytes onto one index name, so two different indexes
// would silently become one and the collision would surface only when the database refused the duplicate. The
// hash is taken over the whole untruncated name, so "same head, different tail" stays distinguishable; eight hex
// characters of SHA-256 (32 bits) take a quarter of the budget PostgreSQL's 63 bytes leave for it, which keeps
// accidental collisions out of reach for any realistic number of indexes. | 直接截断会把所有前若干字节相同的
// 长名字映射成同一个索引名，两个不同的索引于是无声地变成一个，直到数据库拒绝这个重名才暴露。散列取自截断前的
// 完整名字，因此「头部相同、尾部不同」依然可区分；SHA-256 的八个十六进制字符（32 位）只占 PostgreSQL 那
// 63 字节留给它的预算的四分之一，任何现实规模的索引数量都远够不到意外碰撞。
//
// Lengths are counted in bytes, the unit PostgreSQL counts identifiers in; every name this strategy produces is
// ASCII, so bytes and characters coincide. | 长度按字节计算，与 PostgreSQL 数标识符的单位一致；本策略产出的
// 名字都是 ASCII，字节与字符同数。
func (n NamingStrategy) truncate(name string) string {
	limit := n.IdentifierMaxLength
	if limit <= 0 {
		limit = IdentifierMaxLength
	}
	if len(name) <= limit {
		return name
	}
	// 再荒谬的上限也必须放得下分隔符与散列：宁可超出调用方给的极小值，也不允许切片越界。
	// Even an absurd limit must fit the separator and the hash: exceeding a tiny caller-supplied value beats a slice panic.
	if limit < indexHashLength+1 {
		limit = indexHashLength + 1
	}
	sum := sha256.Sum256([]byte(name))
	return name[:limit-indexHashLength-1] + "_" + hex.EncodeToString(sum[:])[:indexHashLength]
}

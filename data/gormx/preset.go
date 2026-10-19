package gormx

import (
	"net/url"
	"strings"
)

// sqliteKeyPragma is the SQLite query parameter carrying a PRAGMA statement: each value runs one
// PRAGMA on every new connection, so the parameter may repeat. | sqliteKeyPragma 是承载 PRAGMA
// 语句的 SQLite 查询参数：每个取值都会在每条新连接上执行一次 PRAGMA，因此该参数可以重复出现。
const sqliteKeyPragma = "_pragma"

// dsnParam is one parameter the preset layer may add to a DSN. | dsnParam 是预置层可能补进连接串的一个参数。
//
// value holds the decoded value; percent-escaping happens only when the parameter is written out,
// so the same table can be asserted on both the DSN text and the value a driver decodes. | value
// 存的是解码后的取值，只在写出时才做百分号转义，因此同一张表既能断言连接串原文，也能断言驱动解码后的取值。
type dsnParam struct {
	key   string
	value string
}

// mysqlPresetSQLMode is the strict sql_mode the preset enforces. | mysqlPresetSQLMode 是预置强制启用的严格 sql_mode。
//
// STRICT_TRANS_TABLES turns silent truncation into an error, NO_ZERO_DATE / NO_ZERO_IN_DATE reject
// the zero dates that break time round-tripping, and ERROR_FOR_DIVISION_BY_ZERO turns a silent NULL
// into an error. The quotes are part of the value because MySQL expects a quoted string. |
// STRICT_TRANS_TABLES 把静默截断变成错误，NO_ZERO_DATE / NO_ZERO_IN_DATE 拒绝会破坏时间往返的零日期，
// ERROR_FOR_DIVISION_BY_ZERO 把静默的 NULL 变成错误。引号是取值的一部分，MySQL 要求这里是一个带引号的字符串。
const mysqlPresetSQLMode = "'STRICT_TRANS_TABLES,NO_ZERO_DATE,NO_ZERO_IN_DATE,ERROR_FOR_DIVISION_BY_ZERO'"

// mysqlPresetParams lists the MySQL defaults in append order. | mysqlPresetParams 按追加顺序列出 MySQL 的预置参数。
//
// The dimension order follows the design document (§2.6.2): time zone, time handling, nullability
// strictness, character set. | 维度顺序与设计文档（§2.6.2）一致：时区、时间配置、空值严格性、字符集。
//
// Connection pooling is deliberately absent: the framework already enables PrepareStmt in Open, and
// a DSN key would be the wrong place to override it. | 这里刻意不含并发/持久性设置：
// 框架已在 Open 中默认开启 PrepareStmt，用连接串参数覆盖它会放错地方。
var mysqlPresetParams = []dsnParam{
	{key: "loc", value: "UTC"},                      // Time zone the driver builds time.Time in. | 驱动构造 time.Time 使用的时区。
	{key: "time_zone", value: "'+00:00'"},           // Session time zone on the server, quoted for MySQL. | 服务端会话时区，按 MySQL 的要求带引号。
	{key: "parseTime", value: "True"},               // Scan DATE/DATETIME/TIMESTAMP into time.Time. | 把 DATE/DATETIME/TIMESTAMP 扫描为 time.Time。
	{key: "sql_mode", value: mysqlPresetSQLMode},    // Nullability and truncation strictness. | 空值严格性与截断严格性。
	{key: "charset", value: "utf8mb4"},              // Client character set, applied as SET NAMES. | 客户端字符集，以 SET NAMES 下发。
	{key: "collation", value: "utf8mb4_unicode_ci"}, // Collation, independent of server version defaults. | 排序规则，不随服务端版本默认值漂移。
}

// postgresPresetParams lists the PostgreSQL defaults in append order. | postgresPresetParams 按追加顺序列出 PostgreSQL 的预置参数。
//
// Time handling and nullability strictness need no key: the driver maps time.Time natively and
// PostgreSQL's type system rejects the invalid values on its own, so the preset adds nothing rather
// than add a key that does nothing. | 时间配置与空值严格性无需在此加键：驱动天然映射 time.Time，
// PostgreSQL 的类型系统本就会拒绝非法取值，因此预置宁可不加，也不加空转的键。
var postgresPresetParams = []dsnParam{
	{key: "TimeZone", value: "UTC"},         // Session time zone, a GUC (case-insensitive server side). | 会话时区，GUC（服务端不区分大小写）。
	{key: "client_encoding", value: "UTF8"}, // Client character set. | 客户端字符集。
}

// sqlitePresetParams lists the SQLite defaults in append order. | sqlitePresetParams 按追加顺序列出 SQLite 的预置参数。
//
// Each _pragma value runs one PRAGMA per connection: journal_mode(WAL) with synchronous(NORMAL) is
// the durability/concurrency pair, busy_timeout(5000) makes a writer wait for the lock instead of
// failing at once, and foreign_keys(1) rejects dangling references. | 每个 _pragma 取值都会在每条连接上
// 执行一次 PRAGMA：journal_mode(WAL) 与 synchronous(NORMAL) 是持久性/并发的一对，
// busy_timeout(5000) 让写者等待锁而不是立即失败，foreign_keys(1) 拒绝悬空引用。
//
// Character set needs no key: SQLite stores text as UTF-8 and its API is UTF-8 native. | 字符集无需加键：
// SQLite 以 UTF-8 存储文本，其接口本身就是 UTF-8。
//
// _time_format must never be added here. On github.com/glebarez/go-sqlite v1.21.2 applyQueryParams
// returns early once it sees that key (sqlite.go:893-900), so the _txlock branch below it
// (sqlite.go:902) is never reached and a caller's explicit _txlock=immediate is silently dropped;
// write transactions then begin as deferred, and the read-then-write pattern every ORM produces fails
// with SQLITE_BUSY_SNAPSHOT (517) without the busy handler ever being consulted. The key buys nothing
// in exchange: writeTimeFormats["sqlite"] is parseTimeFormats[0], which is also what formatTime uses
// when the key is absent, so the stored text is byte-identical with or without it. | 这里**不得**加
// _time_format：在 github.com/glebarez/go-sqlite v1.21.2 上，applyQueryParams 一旦看到该键就提前返回
// （sqlite.go:893-900），其后的 _txlock 分支（sqlite.go:902）永远走不到，调用方显式写的
// _txlock=immediate 会被静默丢弃；写事务退化为延迟事务，ORM 必然产生的「先读后写」模式随即以
// SQLITE_BUSY_SNAPSHOT（517）失败，且根本不调用 busy handler。而且该键换不来任何东西：
// writeTimeFormats["sqlite"] 就是 parseTimeFormats[0]，也正是该键缺席时 formatTime 使用的默认格式，
// 因此有无该键落库文本逐字节相同。
//
// _loc is understood by the mattn/go-sqlite3 driver family and ignored by github.com/glebarez/go-sqlite,
// which is the driver this package opens: an unknown query key is simply dropped once the query string
// is parsed, so the key is inert here rather than harmful. Time correctness on that driver comes from
// its default write format, which already carries the offset, and _loc stays in the table so that
// every SQLite driver gets the same preset shape. | _loc 由 mattn/go-sqlite3 一系驱动识别，
// 而本包使用的 github.com/glebarez/go-sqlite 会忽略它——解析查询串之后未知键即被丢弃，
// 因此该键在这里是惰性的、而非有害的。该驱动的时间正确性来自它自带的默认写出格式（本就带偏移量），
// 保留 _loc 是为了让各 SQLite 驱动拿到同样的预置形态。
var sqlitePresetParams = []dsnParam{
	{key: "_loc", value: "UTC"},                          // Time zone, for drivers that read it. | 时区，供识别该键的驱动使用。
	{key: sqliteKeyPragma, value: "foreign_keys(1)"},     // Nullability: reject dangling foreign keys. | 空值：拒绝悬空外键。
	{key: sqliteKeyPragma, value: "journal_mode(WAL)"},   // Concurrency: write-ahead logging. | 并发：预写式日志。
	{key: sqliteKeyPragma, value: "synchronous(NORMAL)"}, // Durability paired with WAL. | 与 WAL 配对的持久性级别。
	{key: sqliteKeyPragma, value: "busy_timeout(5000)"},  // Wait up to 5s for a locked database. | 数据库被锁时最多等待 5 秒。
}

// PresetDSN fills the driver defaults for time zone, time handling, nullability strictness and
// character set into dsn and returns the result. | PresetDSN 按驱动把默认的时区、时间配置、
// 空值严格性与字符集参数补进 dsn 并返回。
//
// The rules are: | 规则如下：
//
//   - explicit parameters win: a key the caller already wrote keeps its value, the preset only
//     fills in what is absent; | 显式参数优先：调用方已写出的键保留原值，预置只补齐缺失的键；
//   - idempotent: presetting an already preset DSN adds nothing; | 幂等：对已预置的连接串再预置不再新增参数；
//   - faithful: the path, the existing query string, PostgreSQL's keyword/value form and SQLite's
//     file: URI form are preserved verbatim, and new parameters are appended in a fixed order so
//     that a result can be asserted and compared by eye; | 形态保真：路径、既有查询串、
//     PostgreSQL 的 key=value 形态与 SQLite 的 file: URI 形态都原样保留，新增参数按固定顺序追加，
//     结果可断言、可肉眼比对；
//   - parameter names are compared the way each driver compares them: MySQL system variables and
//     PostgreSQL GUC names are case-insensitive, so both drivers compare names case-insensitively,
//     while SQLite's _pragma / _loc keys are matched exactly by the driver and only the PRAGMA name
//     inside a _pragma value is compared case-insensitively. | 参数名按各驱动的真实语义比对：
//     MySQL 的系统变量与 PostgreSQL 的 GUC 名都不区分大小写，这两个驱动整体按不区分大小写比对；
//     SQLite 的 _pragma / _loc 键由驱动精确匹配，故按键精确比对，
//     只有 _pragma 取值内部的 PRAGMA 名按不区分大小写比对。
//
// An empty DSN is returned unchanged because there is nothing to preset; an empty driver name means
// the framework default SQLite, the same reading dialectorFor applies; any other driver name is
// returned unchanged, because this layer presets and does not validate. | 空连接串原样返回，
// 因为没有可预置的内容；空驱动名按框架默认的 SQLite 处理，与 dialectorFor 的口径一致；
// 其余驱动名一律原样返回，因为预置层只做预置、不做校验。
//
// Behavior change to be aware of: a MySQL DSN that carried loc=Local now gets the same time semantics
// only if the caller keeps writing loc=Local, since the preset fills in loc=UTC. Local time still
// round-trips as an instant, but it is no longer the default reading, which is what keeps multiple
// replicas and multiple time zones consistent — the same choice the framework makes for its logs. |
// 需要留意的一处行为变更：先前写 loc=Local 的 MySQL 连接串，只有在调用方继续显式写 loc=Local 时才保持原语义，
// 因为预置补的是 loc=UTC。本地时间仍能作为瞬时点往返，但不再是默认读法；这正是多副本、多时区保持一致的前提，
// 与框架日志一律输出 UTC 的既有选择一致。
func PresetDSN(driver, dsn string) string {
	if strings.TrimSpace(dsn) == "" {
		return dsn
	}
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "", DriverSQLite:
		return presetSQLiteDSN(dsn)
	case DriverMySQL:
		return presetMySQLDSN(dsn)
	case DriverPostgres:
		return presetPostgresDSN(dsn)
	default:
		return dsn
	}
}

// presetMySQLDSN appends the MySQL defaults to a go-sql-driver/mysql DSN. | presetMySQLDSN 把 MySQL
// 默认参数追加到 go-sql-driver/mysql 形态的连接串上。
func presetMySQLDSN(dsn string) string {
	query := ""
	hasQuery := false
	if q := mysqlQueryIndex(dsn); q >= 0 {
		query, hasQuery = dsn[q+1:], true
	}
	keys := queryKeys(query, true)
	_, hasCharset := keys["charset"]
	_, hasCollation := keys["collation"]
	missing := missingParams(mysqlPresetParams, keys, true)
	if hasCharset != hasCollation {
		// charset and collation form one SET NAMES statement. Pairing an explicit latin1 charset
		// with the preset utf8mb4_unicode_ci collation (or the reverse) makes MySQL reject every
		// connection with error 1253. A utf8mb4 half is compatible with the preset, so only an
		// explicit non-utf8mb4 value suppresses the other preset. | charset 与 collation 组成同一条
		// SET NAMES 语句。把显式的 latin1 与预置的 utf8mb4_unicode_ci 配在一起（或反过来）会让
		// MySQL 以 1253 拒绝每条连接。utf8mb4 的一半与预置兼容，因此只有显式的非 utf8mb4 取值才
		// 会压掉另一半预置。
		compatible := false
		if hasCharset {
			compatible = strings.EqualFold(queryValue(query, "charset"), "utf8mb4")
		} else {
			compatible = strings.HasPrefix(strings.ToLower(queryValue(query, "collation")), "utf8mb4")
		}
		if !compatible {
			filtered := missing[:0]
			for _, p := range missing {
				if p.key == "charset" || p.key == "collation" {
					continue
				}
				filtered = append(filtered, p)
			}
			missing = filtered
		}
	}
	return appendQueryDSN(dsn, hasQuery, missing)
}

// presetPostgresDSN appends the PostgreSQL defaults to either DSN form the driver accepts. | presetPostgresDSN
// 把 PostgreSQL 默认参数追加到驱动接受的两种连接串形态上。
func presetPostgresDSN(dsn string) string {
	if isPostgresURL(dsn) {
		query := ""
		hasQuery := false
		if q := strings.IndexByte(dsn, '?'); q >= 0 {
			query, hasQuery = dsn[q+1:], true
		}
		keys := queryKeys(query, true)
		return appendQueryDSN(dsn, hasQuery, missingParams(postgresPresetParams, keys, true))
	}
	keys := postgresKeywordKeys(dsn)
	return appendKeywordDSN(dsn, missingParams(postgresPresetParams, keys, true))
}

// presetSQLiteDSN appends the SQLite defaults to a github.com/glebarez/go-sqlite DSN. | presetSQLiteDSN
// 把 SQLite 默认参数追加到 github.com/glebarez/go-sqlite 形态的连接串上。
func presetSQLiteDSN(dsn string) string {
	query := ""
	hasQuery := false
	if q := sqliteQueryIndex(dsn); q >= 0 {
		query, hasQuery = dsn[q+1:], true
	}
	keys := queryKeys(query, false)
	pragmas := queryPragmaNames(query)

	missing := make([]dsnParam, 0, len(sqlitePresetParams))
	for _, p := range sqlitePresetParams {
		if p.key == sqliteKeyPragma {
			// Judged per PRAGMA name, not per parameter name: a DSN may carry several _pragma
			// values and only the ones the caller named must be left alone. | 按 PRAGMA 名判定，
			// 而不是按参数名判定：连接串可以带多个 _pragma 取值，只有调用方点名的那些才必须原样保留。
			if _, ok := pragmas[strings.ToLower(pragmaName(p.value))]; ok {
				continue
			}
			missing = append(missing, p)
			continue
		}
		if _, ok := keys[p.key]; !ok {
			missing = append(missing, p)
		}
	}
	return appendQueryDSN(dsn, hasQuery, missing)
}

// mysqlQueryIndex returns the index of the '?' that starts a MySQL DSN's query string, or -1. |
// mysqlQueryIndex 返回 MySQL 连接串中查询串起点 '?' 的下标，没有则返回 -1。
//
// It mirrors go-sql-driver/mysql's own split: the database name follows the last '/', and the query
// string starts at the first '?' after it, so a '/' or a '?' inside a password cannot be mistaken
// for either boundary. | 与 go-sql-driver/mysql 自身的切分方式一致：库名跟在最后一个 '/' 之后，
// 查询串从其后的第一个 '?' 开始，因此口令里的 '/' 或 '?' 都不会被误当作边界。
func mysqlQueryIndex(dsn string) int {
	slash := strings.LastIndexByte(dsn, '/')
	if slash < 0 {
		return -1
	}
	if q := strings.IndexByte(dsn[slash+1:], '?'); q >= 0 {
		return slash + 1 + q
	}
	return -1
}

// sqliteQueryIndex returns the index of the '?' that starts a SQLite DSN's query string, or -1. |
// sqliteQueryIndex 返回 SQLite 连接串中查询串起点 '?' 的下标，没有则返回 -1。
//
// github.com/glebarez/go-sqlite only treats a '?' at index 1 or later as the separator, so an
// index-0 '?' is not one here either. | github.com/glebarez/go-sqlite 只把下标不小于 1 的 '?'
// 当作分隔符，这里同样不把下标 0 的 '?' 当作查询串起点。
func sqliteQueryIndex(dsn string) int {
	if q := strings.IndexByte(dsn, '?'); q >= 1 {
		return q
	}
	return -1
}

// isPostgresURL reports whether dsn uses the postgres:// URL form. | isPostgresURL 判断连接串是否使用
// postgres:// 的 URL 形态。
//
// The prefixes are matched case-sensitively because pgx matches them that way; everything else is
// read as the keyword/value form. | 前缀按大小写敏感比对，与 pgx 保持一致；其余一律按关键字/值形态解析。
func isPostgresURL(dsn string) bool {
	return strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://")
}

// queryKeys collects the parameter names appearing in a query string. | queryKeys 收集查询串中出现的参数名。
//
// Names are not percent-decoded: the comparison only cares about the plain ASCII names this layer
// presets, and decoding would blur where one parameter ends and the next begins. fold lowercases the
// collected names so a caller can compare them case-insensitively. | 参数名不做百分号解码：
// 比对只关心本层预置的纯 ASCII 参数名，解码只会让参数之间的边界变得含糊。fold 为 true 时把收集到的
// 参数名统一小写，便于按不区分大小写比对。
// queryValue returns the first value of name in a query string, percent-decoded and lowercased.
// | queryValue 返回查询串中 name 的第一个取值，已百分号解码并转为小写。
func queryValue(query, name string) string {
	for _, pair := range strings.Split(query, "&") {
		key, value, ok := strings.Cut(pair, "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), name) {
			continue
		}
		if decoded, err := url.QueryUnescape(value); err == nil {
			value = decoded
		}
		return strings.ToLower(strings.TrimSpace(value))
	}
	return ""
}

func queryKeys(query string, fold bool) map[string]struct{} {
	keys := make(map[string]struct{})
	for _, pair := range strings.Split(query, "&") {
		key, _, _ := strings.Cut(pair, "=")
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if fold {
			key = strings.ToLower(key)
		}
		keys[key] = struct{}{}
	}
	return keys
}

// queryPragmaNames collects the PRAGMA names already named by _pragma values, lowercased because
// SQLite compares PRAGMA names case-insensitively. | queryPragmaNames 收集 _pragma 取值中已出现的
// PRAGMA 名，统一小写，因为 SQLite 对 PRAGMA 名不区分大小写。
//
// Each value is percent-decoded first, because that is the value the driver executes: a caller who
// wrote _pragma=journal_mode%3DDELETE named journal_mode and must not get a second journal_mode
// appended behind it. A value whose escapes do not decode is judged as written. | 每个取值先做
// 百分号解码，因为那才是驱动实际执行的取值：写出 _pragma=journal_mode%3DDELETE 的调用方，
// 点名的就是 journal_mode，不能再往它后面追加一个 journal_mode。转义无法解码的取值按原样判定。
func queryPragmaNames(query string) map[string]struct{} {
	names := make(map[string]struct{})
	for _, pair := range strings.Split(query, "&") {
		key, value, _ := strings.Cut(pair, "=")
		if strings.TrimSpace(key) != sqliteKeyPragma {
			continue
		}
		if decoded, err := url.QueryUnescape(value); err == nil {
			value = decoded
		}
		if name := strings.ToLower(pragmaName(value)); name != "" {
			names[name] = struct{}{}
		}
	}
	return names
}

// pragmaName extracts the PRAGMA name from a _pragma value. | pragmaName 从 _pragma 的取值中取出 PRAGMA 名。
//
// Both "journal_mode(WAL)" and "journal_mode=WAL" yield "journal_mode", and a bare "journal_mode"
// yields itself. | "journal_mode(WAL)" 与 "journal_mode=WAL" 都得到 "journal_mode"，
// 裸写的 "journal_mode" 得到它本身。
func pragmaName(value string) string {
	value, _, _ = strings.Cut(value, "(")
	value, _, _ = strings.Cut(value, "=")
	return strings.TrimSpace(value)
}

// missingParams returns the params of a preset table that keys does not already cover, keeping the
// table order so the appended text stays stable. | missingParams 返回预置表中 keys 尚未覆盖的参数，
// 保持表内顺序，使追加出的文本稳定不变。
//
// fold selects whether parameter names are compared case-insensitively, matching how the driver
// compares them; keys must have been collected with the same fold setting. | fold 决定参数名是否按
// 不区分大小写比对，与驱动的比对方式一致；keys 必须用相同的 fold 设置收集。
func missingParams(params []dsnParam, keys map[string]struct{}, fold bool) []dsnParam {
	missing := make([]dsnParam, 0, len(params))
	for _, p := range params {
		key := p.key
		if fold {
			key = strings.ToLower(key)
		}
		if _, ok := keys[key]; ok {
			continue
		}
		missing = append(missing, p)
	}
	return missing
}

// appendQueryDSN appends params to a DSN that carries its parameters in a query string. | appendQueryDSN
// 把参数追加到以查询串承载参数的连接串上。
//
// The separator follows the text at hand: '?' when no query string exists yet, nothing when the DSN
// already ends with '?' or '&', and '&' otherwise. | 分隔符依既有文本而定：还没有查询串时用 '?'，
// 连接串已以 '?' 或 '&' 结尾时不补分隔符，其余情况用 '&'。
//
// Every value goes through url.QueryEscape, because MySQL's time_zone='+00:00' and sql_mode='A,B'
// contain the characters that break a DSN once written raw: an unescaped '+' decodes as a space, and
// fields like ',' only survive because this layer escapes them and the driver unescapes them back
// (go-sql-driver/mysql calls url.QueryUnescape, the SQLite driver and pgx's URL form call
// url.ParseQuery). | 每个取值都经 url.QueryEscape 编码：MySQL 的 time_zone='+00:00' 与 sql_mode='A,B'
// 含有原样写出就会破坏连接串的字符——未转义的 '+' 会被解码成空格；而 ',' 这类字段之所以能原样抵达，
// 正是因为本层转义、驱动再反转义（go-sql-driver/mysql 调 url.QueryUnescape，
// SQLite 驱动与 pgx 的 URL 形态调 url.ParseQuery）。
func appendQueryDSN(dsn string, hasQuery bool, params []dsnParam) string {
	if len(params) == 0 {
		return dsn
	}
	sep := "&"
	switch {
	case !hasQuery:
		sep = "?"
	case strings.HasSuffix(dsn, "?") || strings.HasSuffix(dsn, "&"):
		sep = ""
	}

	var b strings.Builder
	b.Grow(len(dsn) + 32*len(params))
	b.WriteString(dsn)
	for _, p := range params {
		b.WriteString(sep)
		b.WriteString(p.key)
		b.WriteByte('=')
		b.WriteString(url.QueryEscape(p.value))
		sep = "&"
	}
	return b.String()
}

// appendKeywordDSN appends params to a libpq keyword/value DSN, separated by a space. | appendKeywordDSN
// 把参数追加到 libpq 关键字/值形态的连接串上，以空格分隔。
func appendKeywordDSN(dsn string, params []dsnParam) string {
	if len(params) == 0 {
		return dsn
	}
	var b strings.Builder
	b.Grow(len(dsn) + 32*len(params))
	b.WriteString(dsn)
	for _, p := range params {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(p.key)
		b.WriteByte('=')
		b.WriteString(quoteKeywordValue(p.value))
	}
	return b.String()
}

// keywordValueSpecials holds the bytes that force a libpq keyword/value value into single quotes. |
// keywordValueSpecials 列出会让 libpq 关键字/值形态的取值必须加单引号的字节。
const keywordValueSpecials = " \t\n\r\v\f'\\"

// keywordValueEscaper escapes the two bytes libpq accepts inside single quotes. | keywordValueEscaper
// 转义 libpq 在单引号内接受的两种字节。
var keywordValueEscaper = strings.NewReplacer(`\`, `\\`, `'`, `\'`)

// quoteKeywordValue quotes a libpq keyword/value value when it needs it: an empty value, or one that
// contains whitespace, a single quote or a backslash, is wrapped in single quotes with an inner
// backslash or single quote escaped by a backslash. | quoteKeywordValue 在必要时为 libpq 关键字/值形态的
// 取值加引号：取值为空，或含空白、单引号、反斜杠时，用单引号包住，并把内部的单引号与反斜杠用反斜杠转义。
//
// Every value this layer presets is a bare word, so quoting never triggers today; the rule is kept so
// that a value added later cannot silently produce an unparsable DSN. | 本层预置的取值目前都是裸词，
// 因此还不会触发加引号；保留该规则，是为了将来新增取值时不会悄悄产出无法解析的连接串。
func quoteKeywordValue(v string) string {
	if v != "" && !strings.ContainsAny(v, keywordValueSpecials) {
		return v
	}
	return "'" + keywordValueEscaper.Replace(v) + "'"
}

// postgresKeywordKeys collects the parameter names of a libpq keyword/value DSN, lowercased because an
// unrecognized key travels to the server as a startup parameter and GUC names are case-insensitive
// there. | postgresKeywordKeys 收集 libpq 关键字/值形态连接串里的参数名，统一小写：
// 未被识别的键会作为启动参数发给服务端，而那里的 GUC 名不区分大小写。
//
// The scan mirrors the sequence pgx's own parser walks: a value may be single-quoted, so a space
// inside it does not end the parameter, and a backslash escapes the byte that follows it. | 扫描顺序与
// pgx 自身解析器走过的顺序一致：取值可以用单引号包住，其中的空格不会结束该参数；反斜杠会转义紧随其后的字节。
func postgresKeywordKeys(dsn string) map[string]struct{} {
	keys := make(map[string]struct{})
	for i, n := 0, len(dsn); i < n; {
		for i < n && asciiSpace(dsn[i]) {
			i++
		}
		if i >= n {
			break
		}
		start := i
		for i < n && dsn[i] != '=' && !asciiSpace(dsn[i]) {
			i++
		}
		key := strings.TrimSpace(dsn[start:i])
		for i < n && asciiSpace(dsn[i]) {
			i++
		}
		if i < n && dsn[i] == '=' {
			i = skipKeywordValue(dsn, i+1)
		}
		if key != "" {
			keys[strings.ToLower(key)] = struct{}{}
		}
	}
	return keys
}

// skipKeywordValue returns the index just past the value starting at i. | skipKeywordValue 返回 i 处取值之后的下标。
//
// A single-quoted value ends at its closing quote and an unquoted one at the next whitespace; in both
// forms a backslash escapes the byte that follows it. | 单引号取值在闭合单引号处结束，
// 未加引号的取值在下一个空白处结束；两种形态下反斜杠都转义紧随其后的一个字节。
func skipKeywordValue(dsn string, i int) int {
	n := len(dsn)
	if i >= n {
		return n
	}
	if dsn[i] == '\'' {
		for i++; i < n; i++ {
			switch dsn[i] {
			case '\\':
				i++
			case '\'':
				return i + 1
			}
		}
		return n
	}
	for i < n && !asciiSpace(dsn[i]) {
		if dsn[i] == '\\' {
			i++
		}
		i++
	}
	if i > n {
		return n
	}
	return i
}

// asciiSpace reports whether b is one of the whitespace bytes libpq and pgx treat as separators. |
// asciiSpace 判断 b 是否为 libpq 与 pgx 视作分隔符的空白字节。
func asciiSpace(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	default:
		return false
	}
}

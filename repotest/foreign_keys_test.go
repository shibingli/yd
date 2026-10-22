package repotest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/shibingli/yd/messaging"
	"github.com/shibingli/ydx/regexpx"
	"gorm.io/gorm/schema"
)

// fkTagPattern matches the GORM tags that make AutoMigrate create a database foreign key. The caller
// prefixes the gorm payload with ";" so a relation tag in the first position is matched too. |
// fkTagPattern 匹配会让 AutoMigrate 创建数据库外键的 GORM 标签。调用方会给 gorm 载荷前置 ";"，
// 因此位于首位的关联标签同样会被命中。
var fkTagPattern = regexpx.MustCompile(`(?i)(^|;)\s*(constraint|foreignkey|references|many2many|polymorphic)\s*:`)

// gormTagPattern extracts the payload of each gorm struct tag. |
// gormTagPattern 提取每个 gorm 结构标签的载荷。
var gormTagPattern = regexpx.MustCompile(`(?i)gorm:"([^"]*)"`)

// fkSQLPattern matches the unambiguous SQL shape of a foreign-key clause: both FOREIGN KEY and
// REFERENCES are followed by a parenthesised column list. Prose such as "references an external host"
// therefore does not match. | fkSQLPattern 匹配外键子句的明确 SQL 形态：FOREIGN KEY 与 REFERENCES 后面都跟着
// 括号列清单，因此「references an external host」这类普通文案不会命中。
var fkSQLPattern = regexpx.MustCompile(`(?i)\bforeign\s+key\s*\(|\breferences\s+[\x60"]?[a-z_][a-z0-9_]*[\x60"]?\s*\(`)

// fkDDLPattern is the looser form used only in DDL or .sql context, where PostgreSQL allows
// REFERENCES without an explicit column list. | fkDDLPattern 是仅在 DDL 或 .sql 语境中使用的宽松形式：
// PostgreSQL 允许省略 REFERENCES 后面的列清单。
var fkDDLPattern = regexpx.MustCompile(`(?i)\bforeign\s+key\b|\breferences\s+[\x60"]?[a-z_][a-z0-9_]*`)

// TestForeignKeyPatterns pins the detector itself: a relation tag in the first position must be
// caught, while ordinary prose must not be. | TestForeignKeyPatterns 固定检测器自身的行为：位于首位的
// 关联标签必须命中，普通文案不得误报。
func TestForeignKeyPatterns(t *testing.T) {
	tagCases := []struct {
		payload string
		want    bool
	}{
		{"foreignKey:UserID", true},
		{"constraint:OnDelete:CASCADE", true},
		{"references:User", true},
		{"many2many:user_groups", true},
		{"polymorphic:Owner", true},
		{"column:user_id;foreignKey:UserID", true},
		{"column:user_id; constraint:OnUpdate:CASCADE", true},
		{"column:user_id;type:varchar(64);index", false},
		{"column:references_count;type:int", false},
	}
	for _, tc := range tagCases {
		if got := fkTagPattern.MatchString(";" + tc.payload); got != tc.want {
			t.Errorf("fkTagPattern(%q) = %v, want %v", tc.payload, got, tc.want)
		}
	}
	sqlCases := []struct {
		query string
		want  bool
	}{
		{"ALTER TABLE child ADD CONSTRAINT fk FOREIGN KEY (parent_id) REFERENCES parent (id)", true},
		{`CREATE TABLE child (parent_id BIGINT, FOREIGN KEY (parent_id) REFERENCES "parent" ("id"))`, true},
		{"the sign-in page references an external host", false},
		{"schema declares 0 foreign key columns", false},
	}
	for _, tc := range sqlCases {
		if got := fkSQLPattern.MatchString(tc.query); got != tc.want {
			t.Errorf("fkSQLPattern(%q) = %v, want %v", tc.query, got, tc.want)
		}
	}
	if !fkDDLPattern.MatchString("CREATE TABLE child (parent_id BIGINT REFERENCES parent)") {
		t.Fatal("fkDDLPattern must catch PostgreSQL REFERENCES without a column list")
	}
	if matches := gormTagPattern.FindAllStringSubmatch("x gorm:\"column:id;foreignKey:UserID\" y", -1); len(matches) != 1 ||
		len(matches[0]) < 2 || matches[0][1] != "column:id;foreignKey:UserID" {
		t.Fatalf("gormTagPattern extraction = %v", matches)
	}
}

// TestNoForeignKeyDefinitions enforces the repository rule that only primary keys and indexes
// describe storage: every relation is maintained by application code, never by the database. |
// TestNoForeignKeyDefinitions 强制执行仓库规则：存储只使用主键与索引描述，所有关系由应用代码维护，
// 绝不交给数据库。
func TestNoForeignKeyDefinitions(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()
	for _, file := range scanGoFiles(t, root) {
		if file.rel == "repotest/foreign_keys_test.go" {
			continue
		}
		parsed, err := parser.ParseFile(fset, file.absPath, file.src, parser.ParseComments)
		if err != nil {
			t.Fatalf("%s: parse: %v", file.rel, err)
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.StructType:
				if n.Fields == nil {
					return true
				}
				for _, field := range n.Fields.List {
					if field.Tag == nil {
						continue
					}
					tagText := field.Tag.Value
					if unquoted, unquoteErr := strconv.Unquote(tagText); unquoteErr == nil {
						tagText = unquoted
					}
					for _, match := range gormTagPattern.FindAllStringSubmatch(tagText, -1) {
						if len(match) < 2 || !fkTagPattern.MatchString(";"+match[1]) {
							continue
						}
						t.Errorf("%s: struct tag declares a database relation: %s | 结构标签声明了数据库关系：%s",
							file.rel, field.Tag.Value, field.Tag.Value)
					}
				}
			case *ast.BasicLit:
				if n.Kind != token.STRING {
					return true
				}
				if fkSQLPattern.MatchString(n.Value) {
					t.Errorf("%s: string literal declares a SQL foreign key: %s | 字符串字面量声明了 SQL 外键：%s",
						file.rel, n.Value, n.Value)
				}
			}
			return true
		})
	}

	for _, file := range scanTemplateFiles(t, root) {
		for i, line := range file.lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") ||
				strings.HasPrefix(trimmed, "*") || strings.HasPrefix(trimmed, "{{/*") {
				continue
			}
			if strings.Contains(line, `gorm:"`) {
				for _, match := range gormTagPattern.FindAllStringSubmatch(line, -1) {
					if len(match) < 2 || !fkTagPattern.MatchString(";"+match[1]) {
						continue
					}
					t.Errorf("%s:%d declares a GORM relation tag: %s | 声明了 GORM 关系标签：%s",
						file.rel, i+1, trimmed, trimmed)
				}
			}
			upper := strings.ToUpper(line)
			if (strings.Contains(upper, "CREATE TABLE") || strings.Contains(upper, "ALTER TABLE")) &&
				fkDDLPattern.MatchString(line) {
				t.Errorf("%s:%d declares a SQL foreign key: %s | 声明了 SQL 外键：%s",
					file.rel, i+1, trimmed, trimmed)
			}
		}
	}

	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			t.Errorf("walk %s: %v", path, err)
			return nil
		}
		if info.IsDir() {
			if skippedDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(path), ".sql") {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Errorf("read %s: %v", path, readErr)
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			t.Errorf("rel %s: %v", path, relErr)
			return nil
		}
		for i, line := range strings.Split(string(body), "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "--") || strings.HasPrefix(trimmed, "#") {
				continue
			}
			if fkDDLPattern.MatchString(line) {
				t.Errorf("%s:%d declares a SQL foreign key: %s | 声明了 SQL 外键：%s",
					filepath.ToSlash(rel), i+1, trimmed, trimmed)
			}
		}
		return nil
	})
}

// TestModelsHaveNoDatabaseRelationships parses every exported GORM model and asserts that GORM
// inferred no associations from it. A relation field would make AutoMigrate create a foreign key
// even without a relation tag, so the static tag scan alone is not enough. |
// TestModelsHaveNoDatabaseRelationships 解析每个导出的 GORM 模型并断言 GORM 没有推断出任何关联。
// 关联字段即使没有标签也会让 AutoMigrate 建外键，因此仅扫描标签还不够。
func TestModelsHaveNoDatabaseRelationships(t *testing.T) {
	models := []any{
		&messaging.Conversation{}, &messaging.Member{}, &messaging.Message{},
		&messaging.MessageType{}, &messaging.Tag{}, &messaging.MessageTag{},
		&messaging.Attachment{}, &messaging.FriendRequest{}, &messaging.Friendship{},
		&messaging.FriendNote{}, &messaging.Block{}, &messaging.AuditEntry{},
	}
	for _, model := range models {
		parsed, err := schema.Parse(model, &sync.Map{}, schema.NamingStrategy{})
		if err != nil {
			t.Fatalf("%T: schema parse: %v", model, err)
		}
		if n := len(parsed.Relationships.Relations); n != 0 {
			t.Errorf("%T declares %d GORM relationship(s): relations must be maintained in application code, not by the database | %T 声明了 %d 个 GORM 关系：关系必须由应用代码维护，不能交给数据库",
				model, n, model, n)
		}
	}
}

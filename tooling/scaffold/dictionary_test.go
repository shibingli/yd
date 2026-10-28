package scaffold_test

import (
	"strings"
	"testing"

	"github.com/shibingli/yd/tooling/scaffold"
)

// TestGeneratedDictionaryWiring asserts that every layer of the data-dictionary feature reaches the
// generated project. | TestGeneratedDictionaryWiring 断言数据字典的每一层都进入生成项目。
func TestGeneratedDictionaryWiring(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("dict-app"))

	cases := []struct {
		file string
		want []string
	}{
		{
			file: "internal/router/router.go",
			want: []string{
				`/mgr/dictionaries`,
				`/mgr/dictionaries/new`,
				`/mgr/dictionaries/:id/items/:item_id/edit`,
				`/api/portal/v1/dictionaries`,
				`/api/portal/v1/dictionaries/:code`,
				`/api/v1/dictionaries/:id/items/:item_id`,
				`/mgr/system-dictionaries`,
				`/api/v1/system-dictionaries`,
				`authz.Require(authz.PermDictionaryResolve)`,
				`authz.Require(authz.PermDictionaryRead)`,
				`authz.Require(authz.PermDictionaryManage)`,
				`authz.Require(authz.PermDictionarySystemManage)`,
			},
		},
		{
			file: "internal/repository/store.go",
			want: []string{
				`dictionarygorm.Migrate`,
				`EnsureBuiltins`,
				`DictionaryStore()`,
				`yd_dict_types`,
				`yd_dict_items`,
			},
		},
		{
			file: "internal/service/dictionary.go",
			want: []string{
				`cache.New[dictionary.Snapshot]`,
				`dictionary.cache_ttl`,
				`dictionary.cache_negative_ttl`,
				`dictionaryNamespace`,
				`ConfigDictionarySystemTenant`,
			},
		},
		{
			file: "internal/controller/controller.go",
			want: []string{
				`NewDictionaryService`,
				`DictionaryService()`,
				`CanManageSystemDictionaries`,
				`systemTenantID`,
				`dictionary.ErrCodeTaken`,
				`dictionary.ErrInvalidExtra`,
			},
		},
		{
			file: "internal/controller/dictionary_controller.go",
			want: []string{
				`dictionaryPageData`,
				`respondDictionaryPage`,
			},
		},
		{
			file: "internal/controller/dictionary_item_controller.go",
			want: []string{
				`NewDictionaryItemDrawer`,
				`SetDictionaryItemStatus`,
			},
		},
		{
			file: "internal/controller/dictionary_system_controller.go",
			want: []string{
				`SystemDictionaries`,
				`SaveSystemDictionary`,
			},
		},
		{
			file: "internal/controller/dictionary_view.go",
			want: []string{
				`dictionaryHref`,
				`dictionaryTypeView`,
				`dictionaryLongField`,
			},
		},
		{
			file: "internal/controller/dictionary_api.go",
			want: []string{
				`APIPortalDictionaries`,
				`APICreateDictionaryItem`,
			},
		},
		{
			file: "internal/controller/dictionary_system_api.go",
			want: []string{
				`APISystemDictionaries`,
				`APICreateSystemDictionaryItem`,
				`/api/v1/system-dictionaries`,
			},
		},
		{
			file: "internal/controller/dictionary_api_types.go",
			want: []string{
				`dictionaryBatchResponse`,
				`dictionarySnapshotViews`,
				`dictionaryExtraInput`,
			},
		},
		{
			file: "web/templates/mgr/partials/sidebar.html",
			want: []string{
				`can .Perm.DictionaryRead`,
				`/mgr/dictionaries`,
				`nav.dictionaries`,
			},
		},
		{
			file: "web/templates/mgr/pages/dictionaries.html",
			want: []string{
				`yd-workbench`,
				`data-yd-drawer-open`,
				`confirm.delete_dictionary`,
				`BasePath`,
				`dictionary.scope.tenant`,
			},
		},
		{
			file: "web/i18n/zh-CN.yaml",
			want: []string{
				"dictionaries:",
				"dictionary:",
				"数据字典",
				"system_title",
				"scope:",
			},
		},
		{
			file: "web/i18n/en.yaml",
			want: []string{
				"dictionaries:",
				"dictionary:",
				"Data dictionaries",
				"system_title",
				"scope:",
			},
		},
		{
			file: "configs/config.yaml",
			want: []string{
				"dictionary:",
				"cache_ttl",
				"cache_negative_ttl",
				"system_tenant",
			},
		},
		{
			file: "docs/dictionary.md",
			want: []string{
				"数据字典",
				"/api/portal/v1/dictionaries",
				"/mgr/dictionaries",
				"/mgr/system-dictionaries",
			},
		},
	}
	for _, tc := range cases {
		body := pr.read(tc.file)
		if strings.TrimSpace(body) == "" {
			t.Errorf("%s is missing from the generated project | 生成项目缺少 %s", tc.file, tc.file)
			continue
		}
		for _, want := range tc.want {
			if !strings.Contains(body, want) {
				t.Errorf("%s does not contain %q | %s 缺少 %q", tc.file, want, tc.file, want)
			}
		}
	}
}

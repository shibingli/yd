package envx_test

import (
	"testing"

	"github.com/shibingli/yd/core/envx"
)

// TestNormalize pins the canonical runtime environment names and the short
// aliases dev/prod. | TestNormalize 固定运行环境的规范名与 dev/prod 短别名。
func TestNormalize(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"development", envx.Development},
		{"dev", envx.Development},
		{" DEV ", envx.Development},
		{"Development", envx.Development},
		{"production", envx.Production},
		{"prod", envx.Production},
		{" Prod ", envx.Production},
		{"Production", envx.Production},
		{"staging", "staging"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := envx.Normalize(tc.in); got != tc.want {
			t.Errorf("Normalize(%q) = %q，期望 %q", tc.in, got, tc.want)
		}
	}
}

// TestIsProductionAndDevelopment pins the two predicates, including the
// deliberate refusal to treat staging as development. | TestIsProductionAndDevelopment
// 固定两个判定，并明确 staging 不算开发环境。
func TestIsProductionAndDevelopment(t *testing.T) {
	for _, v := range []string{"production", "prod", "PROD", " Production "} {
		if !envx.IsProduction(v) {
			t.Errorf("IsProduction(%q) = false，期望 true", v)
		}
		if envx.IsDevelopment(v) {
			t.Errorf("IsDevelopment(%q) = true，期望 false", v)
		}
	}
	for _, v := range []string{"development", "dev", "DEV", " Development "} {
		if !envx.IsDevelopment(v) {
			t.Errorf("IsDevelopment(%q) = false，期望 true", v)
		}
		if envx.IsProduction(v) {
			t.Errorf("IsProduction(%q) = true，期望 false", v)
		}
	}
	for _, v := range []string{"staging", "", "test"} {
		if envx.IsProduction(v) || envx.IsDevelopment(v) {
			t.Errorf("%q 不应被判为 production 或 development", v)
		}
	}
}

package authz_test

import (
	"errors"
	"testing"

	"github.com/shibingli/yd/security/authz"
)

// TestNameDerivations 要求四种语言里写出的名字都能相互推导，且每一种都能解析回同一个标识。
//
// This is the load-bearing property of the package: Go passes the constant, a template reads the
// data key, a script reads the JS key, and none of the three may be a second definition. | 这是本包
// 承重的性质：Go 传常量，模板读数据键，脚本读 JS 键，三者都不允许成为第二份定义。
func TestNameDerivations(t *testing.T) {
	cases := []struct {
		token    string
		constant string
		dataKey  string
		jsKey    string
	}{
		{authz.PermUserCreate, "PermUserCreate", "UserCreate", "USER_CREATE"},
		{authz.PermTenantRead, "PermTenantRead", "TenantRead", "TENANT_READ"},
		{authz.PermPermissionManage, "PermPermissionManage", "PermissionManage", "PERMISSION_MANAGE"},
		{"invoice:approve", "PermInvoiceApprove", "InvoiceApprove", "INVOICE_APPROVE"},
		{"invoice-line:reopen", "PermInvoiceLineReopen", "InvoiceLineReopen", "INVOICE_LINE_REOPEN"},
	}
	for _, tc := range cases {
		t.Run(tc.token, func(t *testing.T) {
			if got := authz.ConstantName(tc.token); got != tc.constant {
				t.Errorf("ConstantName(%q) = %q, want %q", tc.token, got, tc.constant)
			}
			if got := authz.DataKey(tc.token); got != tc.dataKey {
				t.Errorf("DataKey(%q) = %q, want %q", tc.token, got, tc.dataKey)
			}
			if got := authz.JSKey(tc.token); got != tc.jsKey {
				t.Errorf("JSKey(%q) = %q, want %q", tc.token, got, tc.jsKey)
			}
		})
	}

	// Roles are named the same way, except that a role name is one word.
	if got := authz.RoleConstantName(authz.RoleAuditor); got != "RoleAuditor" {
		t.Errorf("RoleConstantName(admin) = %q", got)
	}
	if got := authz.RoleDataKey(authz.RoleAdmin); got != "Admin" {
		t.Errorf("RoleDataKey(admin) = %q", got)
	}
	if got := authz.RoleJSKey(authz.RoleAdmin); got != "ADMIN" {
		t.Errorf("RoleJSKey(admin) = %q", got)
	}
}

// TestResolveAtRegistration requires a registered capability to resolve through any of the four spellings. | TestResolveAtRegistration 要求注册过的能力可以用四种写法中的任何一种被解析出来。
func TestResolveAtRegistration(t *testing.T) {
	const token = "shipment:dispatch"
	if err := authz.Register(authz.Permission{Name: token, Description: "Dispatch a shipment | 发运"}); err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	if err := authz.RegisterRole(authz.Role{Name: "shipper", Description: "Shipper | 发货员"}); err != nil {
		t.Fatalf("RegisterRole failed: %v", err)
	}

	refs := []string{
		token,
		authz.ConstantName(token),
		authz.DataKey(token),
		authz.JSKey(token),
	}
	for _, ref := range refs {
		got, err := authz.ResolvePermission(ref)
		if err != nil {
			t.Errorf("ResolvePermission(%q) failed: %v", ref, err)
			continue
		}
		if got != token {
			t.Errorf("ResolvePermission(%q) = %q, want %q", ref, got, token)
		}
	}

	roleRefs := []string{"shipper", "RoleShipper", "Shipper", "SHIPPER"}
	for _, ref := range roleRefs {
		got, err := authz.ResolveRole(ref)
		if err != nil {
			t.Errorf("ResolveRole(%q) failed: %v", ref, err)
			continue
		}
		if got != "shipper" {
			t.Errorf("ResolveRole(%q) = %q", ref, got)
		}
	}
}

// TestResolveRejectsUnknownAndEmpty 要求解析失败一律是哨兵，且空引用绝不意外命中。
//
// A decision helper resolves on every call, so the failure path matters as much as the success path:
// an empty reference must be an error and not the empty capability of some registry entry. | 判定辅助函数
// 每次调用都要解析，因此失败路径与成功路径同等重要：空引用必须是错误，而不是某个注册表条目的空能力。
func TestResolveRejectsUnknownAndEmpty(t *testing.T) {
	perms := []string{"", "no:such:token", "nope:whatever", "user:creat"}
	for _, ref := range perms {
		if _, err := authz.ResolvePermission(ref); !errors.Is(err, authz.ErrUnknownPermission) {
			t.Errorf("ResolvePermission(%q) = %v, want %v", ref, err, authz.ErrUnknownPermission)
		}
	}
	roles := []string{"", "nobody", "RoleNobody", "Administrator"}
	for _, ref := range roles {
		if _, err := authz.ResolveRole(ref); !errors.Is(err, authz.ErrUnknownRole) {
			t.Errorf("ResolveRole(%q) = %v, want %v", ref, err, authz.ErrUnknownRole)
		}
	}
}

// TestResolvePermissionAcceptsTheOtherCaseOfAConstant requires a constant name with the wrong case not to be mistaken for the real one. | TestResolvePermissionAcceptsTheOtherCaseOfAConstant 要求常量名的大小写写错时不被误认。
func TestResolvePermissionAcceptsTheOtherCaseOfAConstant(t *testing.T) {
	if _, err := authz.ResolvePermission("permUserCreate"); !errors.Is(err, authz.ErrUnknownPermission) {
		t.Errorf("a lowercase constant name resolved: %v", err)
	}
}

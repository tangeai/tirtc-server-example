package admin

import "testing"

func TestPermissionDefinitionsAreCompleteAndUnique(t *testing.T) {
	if len(PermissionDefinitions) != len(AllPermissions) {
		t.Fatalf("definitions=%d permissions=%d", len(PermissionDefinitions), len(AllPermissions))
	}
	seen := map[string]bool{}
	for _, definition := range PermissionDefinitions {
		if definition.Code == "" || definition.Name == "" || definition.Group == "" || definition.Description == "" {
			t.Fatalf("incomplete permission definition: %+v", definition)
		}
		if seen[definition.Code] {
			t.Fatalf("duplicate permission definition: %s", definition.Code)
		}
		seen[definition.Code] = true
	}
	for _, code := range AllPermissions {
		if !seen[code] {
			t.Fatalf("permission %s has no definition", code)
		}
	}
}

func TestDefaultRolesOnlyReferenceRegisteredPermissionsAndMenus(t *testing.T) {
	permissions := make(map[string]bool, len(AllPermissions))
	for _, code := range AllPermissions {
		permissions[code] = true
	}
	menus := make(map[string]bool, len(defaultMenus))
	for _, menu := range defaultMenus {
		menus[menu.Code] = true
	}
	seenRoles := map[string]bool{}
	for _, role := range defaultRoles {
		if role.Code == "" || role.Name == "" || role.Remark == "" || seenRoles[role.Code] {
			t.Fatalf("invalid default role: %+v", role)
		}
		seenRoles[role.Code] = true
		for _, permission := range role.Permissions {
			if !permissions[permission] {
				t.Errorf("role %s references unknown permission %s", role.Code, permission)
			}
		}
		for _, menu := range role.Menus {
			if !menus[menu] {
				t.Errorf("role %s references unknown menu %s", role.Code, menu)
			}
		}
	}
	for _, code := range []string{"super_admin", "operations_admin", "technical_support", "auditor"} {
		if !seenRoles[code] {
			t.Errorf("missing default role %s", code)
		}
	}
}

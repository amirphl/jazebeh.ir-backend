package authorization

import "testing"

func TestACLRequestPermissions(t *testing.T) {
	for _, tc := range []struct {
		path string
		want PermissionKey
	}{
		{"/api/v1/admin/access-control/requests", PermissionACLManage},
		{"/api/v1/admin/access-control/requests/123/decision", PermissionACLApprove},
	} {
		got, ok := PermissionForRoute("POST", tc.path)
		if !ok || got != tc.want {
			t.Errorf("%s: got %q, mapped=%v; want %q", tc.path, got, ok, tc.want)
		}
	}
}

func TestUploadCSVStatusRouteHasShortLinkPermission(t *testing.T) {
	permission, ok := PermissionForRoute("GET", "/api/v1/admin/short-links/upload-csv/a-job-id")
	if !ok || permission != PermissionShortLinkManage {
		t.Fatalf("status route permission = %q, mapped=%t", permission, ok)
	}
}

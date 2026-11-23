package authorization

import "testing"

func TestUploadCSVStatusRouteHasShortLinkPermission(t *testing.T) {
	permission, ok := PermissionForRoute("GET", "/api/v1/admin/short-links/upload-csv/a-job-id")
	if !ok || permission != PermissionShortLinkManage {
		t.Fatalf("status route permission = %q, mapped=%t", permission, ok)
	}
}

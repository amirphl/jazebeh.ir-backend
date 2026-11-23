package authorization

import "strings"

// RoutePermission links an HTTP method + path prefix to a permission bucket.
// PathPrefix should be an API prefix (no trailing slash required).
type RoutePermission struct {
	Method      string
	PathPrefix  string
	Permission  PermissionKey
	Description string
}

// RoutePermissionRegistry centralizes the API → permission bucket mapping.
// Keep entries at “feature action” granularity (read/write/approve, etc.).
var RoutePermissionRegistry = []RoutePermission{
	// Campaign page prices (must come before generic admin campaigns GET prefix)
	{"GET", "/api/v1/admin/campaigns/page-prices", PermissionPlatformBasePriceRead, "List page prices"},
	{"PUT", "/api/v1/admin/campaigns/page-prices", PermissionPlatformBasePriceEdit, "Update page price"},

	// Campaign admin
	{"GET", "/api/v1/admin/campaigns", PermissionCampaignRead, "List/get campaigns"},
	{"POST", "/api/v1/admin/campaigns/approve", PermissionCampaignApprove, "Approve campaigns"},
	{"POST", "/api/v1/admin/campaigns/reject", PermissionCampaignApprove, "Reject campaigns"},
	{"POST", "/api/v1/admin/campaigns/reschedule", PermissionCampaignApprove, "Reschedule campaigns"},
	{"POST", "/api/v1/admin/campaigns/cancel", PermissionCampaignApprove, "Cancel campaigns"},
	// Payments admin
	{"POST", "/api/v1/admin/payments/charge-wallet", PermissionPaymentChargeWallet, "Charge wallet (admin)"},
	{"POST", "/api/v1/admin/payments/charge-wallet/preview", PermissionPaymentChargeWallet, "Preview wallet charge impact (admin)"},
	{"GET", "/api/v1/admin/payments/transactions", PermissionPaymentRead, "List filtered transactions"},
	{"GET", "/api/v1/admin/payments/deposit-receipts", PermissionPaymentReceiptReview, "List deposit receipts"},
	{"GET", "/api/v1/admin/payments/deposit-receipts/", PermissionPaymentReceiptReview, "Get deposit receipt file"}, // path prefix covers /deposit-receipts/:uuid/file
	{"POST", "/api/v1/admin/payments/deposit-receipts/status", PermissionPaymentReceiptReview, "Review deposit receipt"},
	{"POST", "/api/v1/admin/payments/transactions/invoice", PermissionPaymentInvoiceAttach, "Attach invoice to transaction"},

	// Customer management
	{"GET", "/api/v1/admin/customer-management", PermissionUserList, "List customers & shares/discounts"},
	{"POST", "/api/v1/admin/customer-management/active-status", PermissionUserWrite, "Change customer active status"},

	// Short-links
	{"POST", "/api/v1/admin/short-links", PermissionShortLinkManage, "Upload/download short-links"},
	{"POST", "/api/v1/admin/short-links/upload-csv", PermissionShortLinkManage, "Upload short-links CSV"},
	{"GET", "/api/v1/admin/short-links/upload-csv/", PermissionShortLinkManage, "Get short-links CSV upload status"},
	{"POST", "/api/v1/admin/short-links/download", PermissionShortLinkManage, "Export short-links"},
	{"POST", "/api/v1/admin/short-links/download-with-clicks", PermissionShortLinkManage, "Export short-links with clicks"},
	{"POST", "/api/v1/admin/short-links/download-with-clicks-range", PermissionShortLinkManage, "Export short-links with clicks range"},
	{"POST", "/api/v1/admin/short-links/download-with-clicks-by-scenario-name", PermissionShortLinkManage, "Export short-links by scenario"},

	// Line numbers
	{"GET", "/api/v1/admin/line-numbers/report", PermissionLineNumberReport, "Line number report/export"},
	{"GET", "/api/v1/admin/line-numbers", PermissionLineNumberRead, "List line numbers"},
	{"POST", "/api/v1/admin/line-numbers", PermissionLineNumberWrite, "Create line number"},
	{"PUT", "/api/v1/admin/line-numbers", PermissionLineNumberWrite, "Batch update line numbers"},

	// Tickets (support)
	{"GET", "/api/v1/admin/tickets", PermissionTicketRead, "List tickets"},
	{"POST", "/api/v1/admin/tickets/reply", PermissionTicketReply, "Reply to tickets"},

	// Media
	{"POST", "/api/v1/admin/media/upload", PermissionMediaWrite, "Upload media"},
	{"GET", "/api/v1/admin/media", PermissionMediaRead, "Download/preview media"},

	// Platform settings & price factors
	{"GET", "/api/v1/admin/platform-settings", PermissionPlatformSettingsRead, "List platform settings"},
	{"PUT", "/api/v1/admin/platform-settings/status", PermissionPlatformSettingsWrite, "Change platform setting status"},
	{"PUT", "/api/v1/admin/platform-settings/metadata", PermissionPlatformSettingsWrite, "Update platform setting metadata"},
	{"POST", "/api/v1/admin/segment-price-factors", PermissionPlatformBasePriceEdit, "Create segment price factor"},
	{"GET", "/api/v1/admin/segment-price-factors", PermissionPlatformBasePriceRead, "List segment price factors"},
	{"GET", "/api/v1/admin/segment-price-factors/level3-options", PermissionPlatformBasePriceRead, "List level3 options"},
	{"GET", "/api/v1/admin/platform-base-prices", PermissionPlatformBasePriceRead, "List platform base prices"},
	{"PUT", "/api/v1/admin/platform-base-prices", PermissionPlatformBasePriceEdit, "Update platform base price"},

	// Access control (maker-checker)
	{"POST", "/api/v1/admin/access-control/requests", PermissionACLManage, "Create ACL change request"},
	{"POST", "/api/v1/admin/access-control/requests/", PermissionACLApprove, "Approve/reject ACL change request"}, // path prefix covers /requests/:uuid/decision
}

// PermissionForRoute returns the permission bucket for the given method/path if any.
func PermissionForRoute(method, path string) (PermissionKey, bool) {
	m := strings.ToUpper(method)
	for _, rp := range RoutePermissionRegistry {
		if rp.Method == m && strings.HasPrefix(path, rp.PathPrefix) {
			return rp.Permission, true
		}
	}
	return "", false
}

package businessflow

import (
	"context"
	"encoding/json"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
	"github.com/amirphl/Yamata-no-Orochi/utils"
)

func logAdminAction(
	ctx context.Context,
	auditRepo repository.AuditLogRepository,
	action, description string,
	success bool,
	customerID *uint,
	metadata map[string]any,
	err error,
) {
	if auditRepo == nil {
		return
	}

	if metadata == nil {
		metadata = map[string]any{}
	}

	if adminID, ok := adminIDFromContext(ctx); ok {
		metadata["admin_id"] = adminID
	}
	if customerID != nil && *customerID != 0 {
		metadata["target_customer_id"] = *customerID
	}
	if endpoint := stringFromCtx(ctx, utils.EndpointKey); endpoint != "" {
		metadata["endpoint"] = endpoint
	}
	if err != nil {
		metadata["error"] = err.Error()
	}

	metaBytes, _ := json.Marshal(metadata)

	audit := &models.AuditLog{
		Action:      action,
		Description: utils.ToPtr(description),
		Success:     utils.ToPtr(success),
		Metadata:    metaBytes,
	}
	if err != nil {
		audit.ErrorMessage = utils.ToPtr(err.Error())
	}
	if customerID != nil && *customerID != 0 {
		audit.CustomerID = customerID
	}
	if ip := stringFromCtx(ctx, utils.IPAddressKey); ip != "" {
		audit.IPAddress = &ip
	}
	if ua := stringFromCtx(ctx, utils.UserAgentKey); ua != "" {
		audit.UserAgent = &ua
	}
	if reqID := stringFromCtx(ctx, utils.RequestIDKey); reqID != "" {
		audit.RequestID = &reqID
	}

	_ = auditRepo.Save(ctx, audit)
}

func adminIDFromContext(ctx context.Context) (uint, bool) {
	if ctx == nil {
		return 0, false
	}
	switch v := ctx.Value(utils.AdminIDKey).(type) {
	case uint:
		return v, true
	case int:
		if v > 0 {
			return uint(v), true
		}
	}
	return 0, false
}

func stringFromCtx(ctx context.Context, key utils.ContextKey) string {
	if ctx == nil {
		return ""
	}
	if val, ok := ctx.Value(key).(string); ok {
		return val
	}
	return ""
}

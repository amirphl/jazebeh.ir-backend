package handlers

import (
	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/app/middleware"
	businessflow "github.com/amirphl/Yamata-no-Orochi/business_flow"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

// AccessControlHandlerInterface defines endpoints for ACL change requests.
type AccessControlHandlerInterface interface {
	CreateRequest(c fiber.Ctx) error
	ApproveRequest(c fiber.Ctx) error
}

type AccessControlHandler struct {
	flow businessflow.AccessControlFlow
}

func NewAccessControlHandler(flow businessflow.AccessControlFlow) AccessControlHandlerInterface {
	return &AccessControlHandler{flow: flow}
}

// CreateRequest creates a pending ACL change request.
// @Summary Create ACL change request (maker)
// @Description Create a maker-checker access-control change request for another admin (roles/allow/deny overrides)
// @Tags Admin Access Control
// @Accept json
// @Produce json
// @Param request body dto.AdminACLChangeRequestCreate true "Requested roles/permissions for target admin"
// @Success 200 {object} dto.APIResponse{data=map[string]string} "Request created (uuid, status)"
// @Failure 400 {object} dto.APIResponse "Invalid body"
// @Failure 401 {object} dto.APIResponse "Admin authentication required"
// @Failure 500 {object} dto.APIResponse "Failed to create request"
// @Router /api/v1/admin/access-control/requests [post]
func (h *AccessControlHandler) CreateRequest(c fiber.Ctx) error {
	var req dto.AdminACLChangeRequestCreate
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).
			JSON(dto.APIResponse{Success: false, Message: "Invalid body", Error: dto.ErrorDetail{Code: "INVALID_BODY"}})
	}
	requesterID, ok := middleware.GetAdminIDFromContext(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).
			JSON(dto.APIResponse{Success: false, Message: "Admin required", Error: dto.ErrorDetail{Code: "ADMIN_AUTHENTICATION_REQUIRED"}})
	}

	modelReq := &models.ACLChangeRequest{
		TargetAdminID: req.TargetAdminID,
		BeforeRoles:   []string{},
		AfterRoles:    req.Roles,
		BeforeAllowed: []string{},
		AfterAllowed:  req.Allow,
		BeforeDenied:  []string{},
		AfterDenied:   req.Deny,
		Reason:        req.Reason,
	}

	created, err := h.flow.CreateRequest(c.Context(), requesterID, modelReq)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).
			JSON(dto.APIResponse{Success: false, Message: "Failed to create request", Error: dto.ErrorDetail{Code: "ACL_REQUEST_CREATE_FAILED", Details: map[string]any{"reason": err.Error()}}})
	}

	return c.JSON(dto.APIResponse{Success: true, Data: map[string]any{"uuid": created.UUID.String(), "status": created.Status}})
}

// ApproveRequest approves or rejects a pending request.
// @Summary Approve or reject ACL change request (checker)
// @Description Checker approves or rejects a pending ACL change request
// @Tags Admin Access Control
// @Accept json
// @Produce json
// @Param uuid path string true "Request UUID"
// @Param action query string false "approve|reject (default approve)"
// @Param request body dto.AdminACLChangeDecision false "Optional reason"
// @Success 200 {object} dto.APIResponse{data=map[string]string} "Request updated (uuid, status)"
// @Failure 400 {object} dto.APIResponse "Invalid UUID or body"
// @Failure 401 {object} dto.APIResponse "Admin authentication required"
// @Failure 403 {object} dto.APIResponse "Forbidden (self-approval or permission denied)"
// @Failure 404 {object} dto.APIResponse "Request not found"
// @Failure 409 {object} dto.APIResponse "Request not in pending state"
// @Failure 500 {object} dto.APIResponse "Approval failed"
// @Router /api/v1/admin/access-control/requests/{uuid}/decision [post]
func (h *AccessControlHandler) ApproveRequest(c fiber.Ctx) error {
	idStr := c.Params("uuid")
	u, err := uuid.Parse(idStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).
			JSON(dto.APIResponse{Success: false, Message: "Invalid uuid", Error: dto.ErrorDetail{Code: "INVALID_UUID"}})
	}
	action := c.Query("action", "approve")
	approve := action != "reject"

	var body dto.AdminACLChangeDecision
	_ = c.Bind().JSON(&body)

	adminID, ok := middleware.GetAdminIDFromContext(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).
			JSON(dto.APIResponse{Success: false, Message: "Admin required", Error: dto.ErrorDetail{Code: "ADMIN_AUTHENTICATION_REQUIRED"}})
	}

	updated, err := h.flow.Approve(c.Context(), adminID, u, approve, body.Reason)
	if err != nil {
		status := fiber.StatusForbidden
		code := "ACL_REQUEST_APPROVE_FAILED"
		switch err {
		case businessflow.ErrInvalidState:
			status = fiber.StatusConflict
			code = "ACL_REQUEST_NOT_PENDING"
		case businessflow.ErrForbidden:
			status = fiber.StatusForbidden
			code = "ACL_REQUEST_FORBIDDEN"
		case businessflow.ErrNotFound:
			status = fiber.StatusNotFound
			code = "ACL_REQUEST_NOT_FOUND"
		}
		return c.Status(status).
			JSON(dto.APIResponse{Success: false, Message: "Approval failed", Error: dto.ErrorDetail{Code: code, Details: map[string]any{"reason": err.Error()}}})
	}

	return c.JSON(dto.APIResponse{Success: true, Data: map[string]any{"uuid": updated.UUID.String(), "status": updated.Status}})
}

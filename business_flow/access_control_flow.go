package businessflow

import (
	"context"
	"encoding/json"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/google/uuid"
)

// AccessControlFlow handles maker-checker for admin ACL changes.
type AccessControlFlow interface {
	CreateRequest(ctx context.Context, requesterID uint, req *models.ACLChangeRequest) (*models.ACLChangeRequest, error)
	Approve(ctx context.Context, approverID uint, id uuid.UUID, approve bool, reason string) (*models.ACLChangeRequest, error)
}

type AccessControlFlowImpl struct {
	adminRepo repository.AdminRepository
	aclRepo   repository.ACLChangeRequestRepository
	auditRepo repository.AuditLogRepository
}

func NewAccessControlFlow(
	adminRepo repository.AdminRepository,
	aclRepo repository.ACLChangeRequestRepository,
	auditRepo repository.AuditLogRepository,
) AccessControlFlow {
	return &AccessControlFlowImpl{adminRepo: adminRepo, aclRepo: aclRepo, auditRepo: auditRepo}
}

func (f *AccessControlFlowImpl) CreateRequest(
	ctx context.Context,
	requesterID uint,
	req *models.ACLChangeRequest,
) (*models.ACLChangeRequest, error) {
	target, err := f.adminRepo.ByID(ctx, req.TargetAdminID)
	if err != nil {
		return nil, err
	}
	if target == nil {
		return nil, ErrNotFound
	}
	if !utils.IsTrue(target.IsActive) {
		return nil, ErrForbidden
	}
	req.UUID = uuid.New()
	req.RequestedByAdminID = requesterID
	req.Status = models.ACLChangeRequestStatusPending
	req.CreatedAt = time.Now().UTC()
	req.UpdatedAt = req.CreatedAt
	req.BeforeRoles = target.Roles
	req.BeforeAllowed = target.AllowedPermissions
	req.BeforeDenied = target.DeniedPermissions
	if err := f.aclRepo.Save(ctx, req); err != nil {
		return nil, err
	}
	f.logAudit(ctx, requesterID, models.AuditActionAdminACLRequestCreated, req, true, "")
	return req, nil
}

func (f *AccessControlFlowImpl) Approve(
	ctx context.Context,
	approverID uint,
	id uuid.UUID,
	approve bool,
	reason string,
) (*models.ACLChangeRequest, error) {
	req, err := f.aclRepo.ByUUID(ctx, id)
	if err != nil {
		return nil, err
	}
	if req == nil || req.Status != models.ACLChangeRequestStatusPending {
		return nil, ErrInvalidState
	}
	if req.RequestedByAdminID == approverID {
		return nil, ErrForbidden
	}

	now := time.Now().UTC()
	req.ApprovedByAdminID = &approverID
	req.UpdatedAt = now
	req.Reason = reason
	if approve {
		req.Status = models.ACLChangeRequestStatusApproved
		req.AppliedAt = &now
		// apply changes to target admin
		admin, err := f.adminRepo.ByID(ctx, req.TargetAdminID)
		if err != nil {
			return nil, err
		}
		admin.Roles = req.AfterRoles
		admin.AllowedPermissions = req.AfterAllowed
		admin.DeniedPermissions = req.AfterDenied
		if err := f.adminRepo.Save(ctx, admin); err != nil {
			return nil, err
		}
		f.logAudit(ctx, approverID, models.AuditActionAdminACLRequestApproved, req, true, reason)
	} else {
		req.Status = models.ACLChangeRequestStatusRejected
		f.logAudit(ctx, approverID, models.AuditActionAdminACLRequestRejected, req, false, reason)
	}
	if err := f.aclRepo.Save(ctx, req); err != nil {
		return nil, err
	}
	return req, nil
}

func (f *AccessControlFlowImpl) logAudit(
	ctx context.Context,
	adminID uint,
	action string,
	req *models.ACLChangeRequest,
	success bool,
	reason string,
) {
	if f.auditRepo == nil {
		return
	}
	meta := map[string]any{
		"request_uuid":       req.UUID.String(),
		"target_admin_id":    req.TargetAdminID,
		"requested_by_admin": req.RequestedByAdminID,
		"approved_by_admin":  req.ApprovedByAdminID,
		"roles_before":       req.BeforeRoles,
		"roles_after":        req.AfterRoles,
		"allow_before":       req.BeforeAllowed,
		"allow_after":        req.AfterAllowed,
		"deny_before":        req.BeforeDenied,
		"deny_after":         req.AfterDenied,
		"reason":             reason,
	}
	metaJSON, _ := json.Marshal(meta)
	log := &models.AuditLog{
		Action:    action,
		Metadata:  metaJSON,
		Success:   &success,
		CreatedAt: time.Now().UTC(),
	}
	_ = f.auditRepo.Save(ctx, log)
}

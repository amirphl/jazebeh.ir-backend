package handlers

import (
	"context"
	"strconv"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/app/middleware"
	businessflow "github.com/amirphl/Yamata-no-Orochi/business_flow"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/gofiber/fiber/v3"
)

// MultimediaAdminHandlerInterface defines admin multimedia handlers.
type MultimediaAdminHandlerInterface interface {
	Upload(c fiber.Ctx) error
	Download(c fiber.Ctx) error
	Preview(c fiber.Ctx) error
}

// MultimediaAdminHandler handles admin multimedia requests.
type MultimediaAdminHandler struct {
	flow businessflow.MultimediaAdminFlow
}

// NewMultimediaAdminHandler creates a new admin multimedia handler.
func NewMultimediaAdminHandler(flow businessflow.MultimediaAdminFlow) MultimediaAdminHandlerInterface {
	return &MultimediaAdminHandler{flow: flow}
}

func (h *MultimediaAdminHandler) ErrorResponse(c fiber.Ctx, statusCode int, message, errorCode string, details any) error {
	return c.Status(statusCode).JSON(dto.APIResponse{
		Success: false,
		Message: message,
		Error: dto.ErrorDetail{
			Code:    errorCode,
			Details: details,
		},
	})
}

func (h *MultimediaAdminHandler) SuccessResponse(c fiber.Ctx, statusCode int, message string, data any) error {
	return c.Status(statusCode).JSON(dto.APIResponse{
		Success: true,
		Message: message,
		Data:    data,
	})
}

// Upload handles multimedia upload (image/video/excel/pdf) for admins.
// @Summary Admin upload multimedia
// @Description Upload an image, video, excel, or pdf file for a customer (jpg/jpeg/png/gif/webp/mp4/mov/webm/mkv/xlsx/xls/xlsm/pdf,
// <=100MB)
// @Tags Admin Multimedia
// @Security BearerAuth
// @Accept mpfd
// @Produce json
// @Param customer_id formData int true "Customer ID"
// @Param file formData file true "Multimedia file (<=100MB)"
// @Success 201 {object} dto.APIResponse{data=dto.UploadMultimediaResponse} "Upload successful"
// @Failure 400 {object} dto.APIResponse "Invalid request or file"
// @Failure 401 {object} dto.APIResponse "Unauthorized"
// @Failure 404 {object} dto.APIResponse "Customer not found"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/admin/media/upload [post]
func (h *MultimediaAdminHandler) Upload(c fiber.Ctx) error {
	customerIDStr := c.FormValue("customer_id")
	if customerIDStr == "" {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "customer_id is required", "INVALID_CUSTOMER_ID", nil)
	}
	customerIDU64, err := strconv.ParseUint(customerIDStr, 10, 64)
	if err != nil || customerIDU64 == 0 {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "invalid customer_id", "INVALID_CUSTOMER_ID", nil)
	}

	fileHeader, err := c.FormFile("file")
	if err != nil || fileHeader == nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "file is required", "INVALID_FILE", nil)
	}

	file, err := fileHeader.Open()
	if err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "invalid file", "INVALID_FILE", err.Error())
	}
	defer file.Close()

	req := dto.UploadMultimediaRequest{
		CustomerID:       uint(customerIDU64),
		OriginalFilename: fileHeader.Filename,
		FileSize:         fileHeader.Size,
		ContentType:      fileHeader.Header.Get("Content-Type"),
		File:             file,
	}

	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/media/upload", 30*time.Second)
	defer cancel()
	result, err := h.flow.UploadMultimediaByAdmin(ctx, &req, metadata)
	if err != nil {
		if businessflow.IsCustomerNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Customer not found", "CUSTOMER_NOT_FOUND", nil)
		}
		if businessflow.IsAccountInactive(err) {
			return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer account is inactive", "ACCOUNT_INACTIVE", nil)
		}
		if be, ok := err.(*businessflow.BusinessError); ok {
			switch be.Code {
			case "INVALID_FILE":
				return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid file", be.Code, be.Error())
			case "INVALID_FILE_TYPE":
				return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid file type", be.Code, be.Error())
			case "FILE_TOO_LARGE":
				return h.ErrorResponse(c, fiber.StatusBadRequest, "File too large", be.Code, be.Error())
			case "INVALID_REQUEST":
				return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request", be.Code, be.Error())
			}
		}
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to upload multimedia", "UPLOAD_FAILED", nil)
	}

	return h.SuccessResponse(c, fiber.StatusCreated, "Upload successful", result)
}

// Download handles multimedia download for admins.
// @Summary Admin download multimedia
// @Description Download a multimedia file (image/video/excel/pdf) by uuid (admin access)
// @Tags Admin Multimedia
// @Security BearerAuth
// @Produce application/octet-stream
// @Param uuid path string true "Multimedia UUID"
// @Success 200 {string} string "Binary file"
// @Failure 400 {object} dto.APIResponse "Invalid request"
// @Failure 401 {object} dto.APIResponse "Unauthorized"
// @Failure 403 {object} dto.APIResponse "Forbidden"
// @Failure 404 {object} dto.APIResponse "Not found"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/admin/media/{uuid} [get]
func (h *MultimediaAdminHandler) Download(c fiber.Ctx) error {
	mediaUUID := c.Params("uuid")
	if mediaUUID == "" {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid uuid", "INVALID_UUID", nil)
	}

	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/media/{uuid}", 30*time.Second)
	defer cancel()
	filename, contentType, data, err := h.flow.DownloadMultimediaByAdmin(ctx, mediaUUID)
	if err != nil {
		if be, ok := err.(*businessflow.BusinessError); ok {
			switch be.Code {
			case "INVALID_UUID":
				return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid uuid", be.Code, be.Error())
			case "MULTIMEDIA_NOT_FOUND":
				return h.ErrorResponse(c, fiber.StatusNotFound, "Multimedia not found", be.Code, be.Error())
			case "INVALID_PATH":
				return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid file path", be.Code, be.Error())
			}
		}
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to download multimedia", "DOWNLOAD_FAILED", nil)
	}

	if contentType != "" {
		c.Set("Content-Type", contentType)
	}
	c.Set("Content-Disposition", "attachment; filename="+filename)
	return c.Send(data)
}

// Preview handles multimedia preview (thumbnail) for admins.
// @Summary Admin preview multimedia
// @Description Return a thumbnail image for multimedia (video -> frame image, image -> resized)
// @Tags Admin Multimedia
// @Security BearerAuth
// @Produce image/jpeg
// @Param uuid path string true "Multimedia UUID"
// @Success 200 {string} string "Thumbnail image"
// @Failure 400 {object} dto.APIResponse "Invalid request"
// @Failure 401 {object} dto.APIResponse "Unauthorized"
// @Failure 403 {object} dto.APIResponse "Forbidden"
// @Failure 404 {object} dto.APIResponse "Not found"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/admin/media/{uuid}/preview [get]
func (h *MultimediaAdminHandler) Preview(c fiber.Ctx) error {
	mediaUUID := c.Params("uuid")
	if mediaUUID == "" {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid uuid", "INVALID_UUID", nil)
	}

	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/media/{uuid}/preview", 30*time.Second)
	defer cancel()
	filename, contentType, data, err := h.flow.PreviewMultimediaByAdmin(ctx, mediaUUID)
	if err != nil {
		if be, ok := err.(*businessflow.BusinessError); ok {
			switch be.Code {
			case "INVALID_UUID":
				return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid uuid", be.Code, be.Error())
			case "MULTIMEDIA_NOT_FOUND":
				return h.ErrorResponse(c, fiber.StatusNotFound, "Multimedia not found", be.Code, be.Error())
			case "INVALID_PATH":
				return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid file path", be.Code, be.Error())
			case "VIDEO_PREVIEW_UNAVAILABLE":
				return h.ErrorResponse(c, fiber.StatusNotImplemented, "Video preview unavailable", be.Code, be.Error())
			case "VIDEO_PREVIEW_FAILED":
				return h.ErrorResponse(c, fiber.StatusBadRequest, "Video preview failed", be.Code, be.Error())
			case "PREVIEW_NOT_SUPPORTED":
				return h.ErrorResponse(c, fiber.StatusBadRequest, "Preview is not supported for this file type", be.Code, be.Error())
			}
		}
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to generate preview", "PREVIEW_FAILED", nil)
	}

	if contentType != "" {
		c.Set("Content-Type", contentType)
	}
	c.Set("Content-Disposition", "inline; filename="+filename)
	return c.Send(data)
}

func (h *MultimediaAdminHandler) createRequestContextWithTimeout(
	c fiber.Ctx,
	endpoint string,
	timeout time.Duration,
) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	ctx = context.WithValue(ctx, utils.RequestIDKey, c.Get("X-Request-ID"))
	ctx = context.WithValue(ctx, utils.UserAgentKey, c.Get("User-Agent"))
	ctx = context.WithValue(ctx, utils.IPAddressKey, c.IP())
	ctx = context.WithValue(ctx, utils.EndpointKey, endpoint)
	ctx = context.WithValue(ctx, utils.TimeoutKey, timeout)
	ctx = context.WithValue(ctx, utils.CancelFuncKey, cancel)
	if adminID, ok := middleware.GetAdminIDFromContext(c); ok {
		ctx = context.WithValue(ctx, utils.AdminIDKey, adminID)
	}
	return ctx, cancel
}

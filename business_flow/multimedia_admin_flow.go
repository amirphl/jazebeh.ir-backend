package businessflow

import (
	"context"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
	"github.com/google/uuid"
)

// MultimediaAdminFlow defines admin operations for multimedia retrieval.
type MultimediaAdminFlow interface {
	UploadMultimediaByAdmin(
		ctx context.Context,
		req *dto.UploadMultimediaRequest,
		metadata *ClientMetadata,
	) (*dto.UploadMultimediaResponse, error)
	DownloadMultimediaByAdmin(ctx context.Context, mediaUUID string) (string, string, []byte, error)
	PreviewMultimediaByAdmin(ctx context.Context, mediaUUID string) (string, string, []byte, error)
}

// MultimediaAdminFlowImpl implements MultimediaAdminFlow.
type MultimediaAdminFlowImpl struct {
	customerRepo   repository.CustomerRepository
	multimediaRepo repository.MultimediaAssetRepository
}

// NewMultimediaAdminFlow creates a new admin multimedia flow instance.
func NewMultimediaAdminFlow(
	customerRepo repository.CustomerRepository,
	multimediaRepo repository.MultimediaAssetRepository,
) MultimediaAdminFlow {
	return &MultimediaAdminFlowImpl{
		customerRepo:   customerRepo,
		multimediaRepo: multimediaRepo,
	}
}

func (f *MultimediaAdminFlowImpl) UploadMultimediaByAdmin(
	ctx context.Context,
	req *dto.UploadMultimediaRequest,
	metadata *ClientMetadata,
) (*dto.UploadMultimediaResponse, error) {
	if req == nil || req.File == nil {
		return nil, NewBusinessError("INVALID_REQUEST", "file is required", nil)
	}

	customer, err := getCustomer(ctx, f.customerRepo, req.CustomerID)
	if err != nil {
		return nil, err
	}

	if req.FileSize <= 0 {
		return nil, NewBusinessError("INVALID_FILE", "file size is required", nil)
	}
	if req.FileSize > maxMultimediaSize {
		return nil, NewBusinessError("FILE_TOO_LARGE", "file size exceeds 100MB", nil)
	}

	ext := strings.ToLower(filepath.Ext(req.OriginalFilename))
	mediaType, ok := allowedMultimediaExts[ext]
	if !ok {
		return nil, NewBusinessError(
			"INVALID_FILE_TYPE",
			fmt.Sprintf("allowed file types: %s", strings.Join(allowedMultimediaFormats, ", ")),
			nil,
		)
	}

	storedPath, size, mimeType, err := saveMultimediaToDisk(req.File, ext, mediaType)
	if err != nil {
		return nil, err
	}

	asset := models.MultimediaAsset{
		UUID:             uuid.New(),
		CustomerID:       customer.ID,
		OriginalFilename: req.OriginalFilename,
		StoredPath:       storedPath,
		SizeBytes:        size,
		MimeType:         mimeType,
		MediaType:        mediaType,
		Extension:        ext,
	}

	if err := f.multimediaRepo.Save(ctx, &asset); err != nil {
		_ = os.Remove(filepath.FromSlash(storedPath))
		return nil, err
	}

	return &dto.UploadMultimediaResponse{
		Message:          "Multimedia uploaded successfully by admin",
		UUID:             asset.UUID.String(),
		MediaType:        asset.MediaType,
		MimeType:         asset.MimeType,
		SizeBytes:        asset.SizeBytes,
		OriginalFilename: asset.OriginalFilename,
		CreatedAt:        asset.CreatedAt.Format(time.RFC3339),
	}, nil
}

func (f *MultimediaAdminFlowImpl) DownloadMultimediaByAdmin(ctx context.Context, mediaUUID string) (string, string, []byte, error) {
	if mediaUUID == "" {
		return "", "", nil, NewBusinessError("INVALID_UUID", "media uuid is required", nil)
	}

	asset, err := f.multimediaRepo.ByUUID(ctx, mediaUUID)
	if err != nil {
		return "", "", nil, err
	}
	if asset == nil {
		return "", "", nil, NewBusinessError("MULTIMEDIA_NOT_FOUND", "multimedia not found", nil)
	}

	cleanPath, err := sanitizeMultimediaPath(asset.StoredPath)
	if err != nil {
		return "", "", nil, err
	}

	data, err := os.ReadFile(cleanPath)
	if err != nil {
		if os.IsNotExist(err) {
			return generateWhiteImagePreview()
		}
		return "", "", nil, err
	}

	filename := filepath.Base(cleanPath)
	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(filename)))
	if contentType == "" {
		contentType = asset.MimeType
	}

	return filename, contentType, data, nil
}

func (f *MultimediaAdminFlowImpl) PreviewMultimediaByAdmin(ctx context.Context, mediaUUID string) (string, string, []byte, error) {
	if mediaUUID == "" {
		return "", "", nil, NewBusinessError("INVALID_UUID", "media uuid is required", nil)
	}

	asset, err := f.multimediaRepo.ByUUID(ctx, mediaUUID)
	if err != nil {
		return "", "", nil, err
	}
	if asset == nil {
		return "", "", nil, NewBusinessError("MULTIMEDIA_NOT_FOUND", "multimedia not found", nil)
	}

	cleanPath, err := sanitizeMultimediaPath(asset.StoredPath)
	if err != nil {
		return "", "", nil, err
	}

	if asset.MediaType == "video" {
		return previewOrWhiteImage(cleanPath, extractVideoThumbnail)
	}
	if asset.MediaType != "image" {
		return "", "", nil, NewBusinessError("PREVIEW_NOT_SUPPORTED", "preview is only supported for image and video files", nil)
	}
	return previewOrWhiteImage(cleanPath, generateImageThumbnail)
}

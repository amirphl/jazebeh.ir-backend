package businessflow

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	imagedraw "image/draw"
	"image/jpeg"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/google/uuid"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// MultimediaFlow defines operations for multimedia uploads.
type MultimediaFlow interface {
	UploadMultimedia(ctx context.Context, req *dto.UploadMultimediaRequest, metadata *ClientMetadata) (*dto.UploadMultimediaResponse, error)
	DownloadMultimedia(ctx context.Context, customerID uint, mediaUUID string) (string, string, []byte, error)
	PreviewMultimedia(ctx context.Context, customerID uint, mediaUUID string) (string, string, []byte, error)
}

// MultimediaFlowImpl implements MultimediaFlow.
type MultimediaFlowImpl struct {
	customerRepo   repository.CustomerRepository
	multimediaRepo repository.MultimediaAssetRepository
}

// NewMultimediaFlow creates a new multimedia flow instance.
func NewMultimediaFlow(customerRepo repository.CustomerRepository, multimediaRepo repository.MultimediaAssetRepository) MultimediaFlow {
	return &MultimediaFlowImpl{
		customerRepo:   customerRepo,
		multimediaRepo: multimediaRepo,
	}
}

const (
	maxMultimediaSize = int64(100 * 1024 * 1024) // 100MB
)

var allowedMultimediaFormats = []string{"jpg", "jpeg", "png", "gif", "webp", "mp4", "mov", "webm", "mkv", "xlsx", "xls", "xlsm", "pdf"}

var allowedMultimediaExts = map[string]string{
	".jpg":  "image",
	".jpeg": "image",
	".png":  "image",
	".gif":  "image",
	".webp": "image",
	".mp4":  "video",
	".mov":  "video",
	".webm": "video",
	".mkv":  "video",
	".xlsx": "excel",
	".xls":  "excel",
	".xlsm": "excel",
	".pdf":  "pdf",
}

func (f *MultimediaFlowImpl) UploadMultimedia(
	ctx context.Context,
	req *dto.UploadMultimediaRequest,
	metadata *ClientMetadata,
) (*dto.UploadMultimediaResponse, error) {
	if req == nil || req.File == nil {
		return nil, NewBusinessError("INVALID_REQUEST", "file is required", nil)
	}

	// Validate customer
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
		Message:          "Multimedia uploaded successfully",
		UUID:             asset.UUID.String(),
		MediaType:        asset.MediaType,
		MimeType:         asset.MimeType,
		SizeBytes:        asset.SizeBytes,
		OriginalFilename: asset.OriginalFilename,
		CreatedAt:        asset.CreatedAt.Format(time.RFC3339),
	}, nil
}

func (f *MultimediaFlowImpl) DownloadMultimedia(ctx context.Context, customerID uint, mediaUUID string) (string, string, []byte, error) {
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
	if asset.CustomerID != customerID {
		return "", "", nil, NewBusinessError("FORBIDDEN", "access denied", nil)
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

func (f *MultimediaFlowImpl) PreviewMultimedia(ctx context.Context, customerID uint, mediaUUID string) (string, string, []byte, error) {
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
	if asset.CustomerID != customerID {
		return "", "", nil, NewBusinessError("FORBIDDEN", "access denied", nil)
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

func saveMultimediaToDisk(reader io.Reader, ext, mediaType string) (string, int64, string, error) {
	head := make([]byte, 512)
	n, err := io.ReadFull(reader, head)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return "", 0, "", err
	}
	head = head[:n]

	detected := http.DetectContentType(head)
	if !isAllowedUploadedMime(detected, ext, mediaType) {
		return "", 0, "", NewBusinessError("INVALID_FILE_TYPE", "file content does not match expected media type", nil)
	}
	if detected == "application/octet-stream" {
		if fromExt := mime.TypeByExtension(ext); fromExt != "" {
			detected = fromExt
		}
	}

	dateDir := utils.UTCNow().Format("2006-01-02")
	baseDir := filepath.Join("data", "uploads", "multimedia", dateDir)
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return "", 0, "", err
	}

	filename := uuid.New().String() + ext
	fullPath := filepath.Join(baseDir, filename)
	dst, err := os.Create(fullPath)
	if err != nil {
		return "", 0, "", err
	}
	defer dst.Close()

	fullReader := io.MultiReader(bytes.NewReader(head), reader)
	limited := io.LimitReader(fullReader, maxMultimediaSize+1)
	written, err := io.Copy(dst, limited)
	if err != nil {
		_ = os.Remove(fullPath)
		return "", 0, "", err
	}
	if written > maxMultimediaSize {
		_ = os.Remove(fullPath)
		return "", 0, "", NewBusinessError("FILE_TOO_LARGE", "file size exceeds 100MB", nil)
	}

	return filepath.ToSlash(filepath.Join("data", "uploads", "multimedia", dateDir, filename)), written, detected, nil
}

func sanitizeMultimediaPath(path string) (string, error) {
	if path == "" {
		return "", NewBusinessError("INVALID_PATH", "path is empty", nil)
	}
	cleaned := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	if filepath.IsAbs(cleaned) {
		return "", NewBusinessError("INVALID_PATH", "absolute path not allowed", nil)
	}
	base := filepath.ToSlash(filepath.Clean(filepath.Join("data", "uploads", "multimedia")))
	if !strings.HasPrefix(cleaned, base) {
		return "", NewBusinessError("INVALID_PATH", "path is outside allowed directory", nil)
	}
	return filepath.FromSlash(cleaned), nil
}

func generateImageThumbnail(srcPath string) (string, string, []byte, error) {
	file, err := os.Open(srcPath)
	if err != nil {
		return "", "", nil, err
	}
	defer file.Close()

	img, _, err := image.Decode(file)
	if err != nil {
		return "", "", nil, err
	}

	thumb := resizeImage(img, 512)
	buf := &bytes.Buffer{}
	if err := jpeg.Encode(buf, thumb, &jpeg.Options{Quality: 75}); err != nil {
		return "", "", nil, err
	}

	return "preview.jpg", "image/jpeg", buf.Bytes(), nil
}

func previewOrWhiteImage(srcPath string, preview func(string) (string, string, []byte, error)) (string, string, []byte, error) {
	if _, err := os.Stat(srcPath); err != nil {
		if os.IsNotExist(err) {
			return generateWhiteImagePreview()
		}
		return "", "", nil, err
	}

	filename, contentType, data, err := preview(srcPath)
	if err != nil && os.IsNotExist(err) {
		return generateWhiteImagePreview()
	}
	return filename, contentType, data, err
}

func generateWhiteImagePreview() (string, string, []byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, 512, 512))
	imagedraw.Draw(img, img.Bounds(), &image.Uniform{C: color.White}, image.Point{}, imagedraw.Src)

	buf := &bytes.Buffer{}
	if err := jpeg.Encode(buf, img, &jpeg.Options{Quality: 75}); err != nil {
		return "", "", nil, err
	}
	return "preview.jpg", "image/jpeg", buf.Bytes(), nil
}

func extractVideoThumbnail(srcPath string) (string, string, []byte, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return "", "", nil, NewBusinessError("VIDEO_PREVIEW_UNAVAILABLE", "ffmpeg is not available", nil)
	}

	tmpDir, err := os.MkdirTemp("", "media-preview-")
	if err != nil {
		return "", "", nil, err
	}
	defer os.RemoveAll(tmpDir)

	outPath := filepath.Join(tmpDir, "preview.jpg")
	cmd := exec.Command(
		"ffmpeg",
		"-y",
		"-i", srcPath,
		"-ss", "00:00:01.000",
		"-vframes", "1",
		"-vf", "scale=512:-2",
		outPath,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", "", nil, NewBusinessError("VIDEO_PREVIEW_FAILED", string(output), err)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		return "", "", nil, err
	}

	return "preview.jpg", "image/jpeg", data, nil
}

func resizeImage(src image.Image, maxDim int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxDim && h <= maxDim {
		return src
	}

	var nw, nh int
	if w >= h {
		nw = maxDim
		nh = int(float64(h) * float64(maxDim) / float64(w))
	} else {
		nh = maxDim
		nw = int(float64(w) * float64(maxDim) / float64(h))
	}

	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	imagedraw.Draw(dst, dst.Bounds(), &image.Uniform{C: color.White}, image.Point{}, imagedraw.Src)
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, b, xdraw.Over, nil)
	return dst
}

func isAllowedUploadedMime(detected, ext, mediaType string) bool {
	if detected == "application/octet-stream" {
		return true
	}
	switch mediaType {
	case "image", "video":
		return strings.HasPrefix(detected, mediaType+"/")
	case "excel":
		return isExcelMime(detected, ext)
	case "pdf":
		return isPDFMime(detected, ext)
	default:
		return false
	}
}

func isExcelMime(mimeType, ext string) bool {
	mt := strings.ToLower(strings.TrimSpace(mimeType))
	ex := strings.ToLower(strings.TrimSpace(ext))

	if strings.Contains(mt, "spreadsheetml") || strings.Contains(mt, "ms-excel") {
		return true
	}
	if mt == "application/zip" || mt == "application/vnd.ms-office" || mt == "application/x-ole-storage" {
		return ex == ".xlsx" || ex == ".xlsm" || ex == ".xls"
	}
	return false
}

func isPDFMime(mimeType, ext string) bool {
	mt := strings.ToLower(strings.TrimSpace(mimeType))
	ex := strings.ToLower(strings.TrimSpace(ext))

	if mt == "application/pdf" {
		return true
	}
	if mt == "application/octet-stream" {
		return ex == ".pdf"
	}
	return false
}

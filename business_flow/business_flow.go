// Package businessflow contains the business logic for the application.
package businessflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/config"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/google/uuid"
)

// ClientMetadata holds all client-related information for audit logging and session tracking
type ClientMetadata struct {
	IPAddress  string            `json:"ip_address"`
	UserAgent  string            `json:"user_agent"`
	DeviceInfo map[string]string `json:"device_info,omitempty"`
	Location   *LocationInfo     `json:"location,omitempty"`
	RequestID  string            `json:"request_id,omitempty"`
	SessionID  string            `json:"session_id,omitempty"`
	Additional map[string]string `json:"additional,omitempty"`
}

// LocationInfo holds geographical location information
type LocationInfo struct {
	Country   string `json:"country,omitempty"`
	Region    string `json:"region,omitempty"`
	City      string `json:"city,omitempty"`
	Latitude  string `json:"latitude,omitempty"`
	Longitude string `json:"longitude,omitempty"`
}

// NewClientMetadata creates a new ClientMetadata instance with basic information
func NewClientMetadata(ipAddress, userAgent string) *ClientMetadata {
	return &ClientMetadata{
		IPAddress:  ipAddress,
		UserAgent:  userAgent,
		DeviceInfo: make(map[string]string),
		Additional: make(map[string]string),
	}
}

// AddDeviceInfo adds device information to the metadata
func (cm *ClientMetadata) AddDeviceInfo(key, value string) {
	if cm.DeviceInfo == nil {
		cm.DeviceInfo = make(map[string]string)
	}
	cm.DeviceInfo[key] = value
}

// AddAdditional adds additional custom information to the metadata
func (cm *ClientMetadata) AddAdditional(key, value string) {
	if cm.Additional == nil {
		cm.Additional = make(map[string]string)
	}
	cm.Additional[key] = value
}

// SetLocation sets location information
func (cm *ClientMetadata) SetLocation(location *LocationInfo) {
	cm.Location = location
}

// SetRequestID sets the request ID
func (cm *ClientMetadata) SetRequestID(requestID string) {
	cm.RequestID = requestID
}

// SetSessionID sets the session ID
func (cm *ClientMetadata) SetSessionID(sessionID string) {
	cm.SessionID = sessionID
}

// ToAuthCustomerDTO converts a customer model to AuthCustomerDTO for authentication responses
func ToAuthCustomerDTO(customer models.Customer) dto.AuthCustomerDTO {
	dto := dto.AuthCustomerDTO{
		ID:                      customer.ID,
		UUID:                    customer.UUID.String(),
		Email:                   customer.Email,
		RepresentativeFirstName: customer.RepresentativeFirstName,
		RepresentativeLastName:  customer.RepresentativeLastName,
		RepresentativeMobile:    customer.RepresentativeMobile,
		AccountType:             customer.AccountType.TypeName,
		CompanyName:             customer.CompanyName,
		IsActive:                customer.IsActive,
		IsEmailVerified:         customer.IsEmailVerified,
		IsMobileVerified:        customer.IsMobileVerified,
		CreatedAt:               customer.CreatedAt.Format(time.RFC3339),
		ReferrerAgencyID:        customer.ReferrerAgencyID,
	}

	return dto
}

func ToCustomerSessionDTO(session models.CustomerSession) dto.CustomerSessionDTO {
	return dto.CustomerSessionDTO{
		SessionToken: session.SessionToken,
		RefreshToken: session.RefreshToken,
		ExpiresIn:    int(time.Until(session.ExpiresAt).Seconds()),
		TokenType:    "Bearer",
		CreatedAt:    session.CreatedAt.Format(time.RFC3339),
	}
}

func ToAdminDTOModel(a models.Admin) dto.AdminDTO {
	return dto.AdminDTO{
		ID:        a.ID,
		UUID:      a.UUID.String(),
		Username:  a.Username,
		IsActive:  a.IsActive,
		CreatedAt: a.CreatedAt.Format(time.RFC3339),
	}
}

func ToAdminSessionDTO(accessToken, refreshToken string) dto.AdminSessionDTO {
	return dto.AdminSessionDTO{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    utils.AccessTokenTTLSeconds,
		TokenType:    "Bearer",
		CreatedAt:    utils.UTCNow().Format(time.RFC3339),
	}
}

func ToBotDTOModel(b models.Bot) dto.BotDTO {
	return dto.BotDTO{
		ID:        b.ID,
		UUID:      b.UUID.String(),
		Username:  b.Username,
		IsActive:  b.IsActive,
		CreatedAt: b.CreatedAt.Format(time.RFC3339),
	}
}

func ToBotSessionDTO(accessToken, refreshToken string) dto.BotSessionDTO {
	return dto.BotSessionDTO{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    utils.AccessTokenTTLSeconds,
		TokenType:    "Bearer",
		CreatedAt:    utils.UTCNow().Format(time.RFC3339),
	}
}

func ToLineNumberDTO(line models.LineNumber) dto.AdminLineNumberDTO {
	return dto.AdminLineNumberDTO{
		ID:          line.ID,
		UUID:        line.UUID.String(),
		Name:        line.Name,
		LineNumber:  line.LineNumber,
		Provider:    string(line.Provider),
		PriceFactor: line.PriceFactor,
		Priority:    line.Priority,
		IsActive:    line.IsActive,
		CreatedAt:   line.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   line.UpdatedAt.Format(time.RFC3339),
	}
}

func createAuditLog(
	ctx context.Context,
	auditRepo repository.AuditLogRepository,
	customer *models.Customer,
	action string,
	description string,
	success bool,
	errorDetails *string,
	metadata *ClientMetadata,
) error {
	var customerID *uint
	if customer != nil {
		customerID = &customer.ID
	}

	ipAddress := ""
	userAgent := ""
	if metadata != nil {
		ipAddress = metadata.IPAddress
		userAgent = metadata.UserAgent
	}

	auditLog := &models.AuditLog{
		CustomerID:   customerID,
		Action:       action,
		Description:  &description,
		Success:      &success,
		IPAddress:    &ipAddress,
		UserAgent:    &userAgent,
		Metadata:     json.RawMessage(`{}`),
		ErrorMessage: errorDetails,
	}

	if errorDetails != nil {
		// Create metadata with error details
		metadataMap := map[string]any{
			"error_details": *errorDetails,
		}
		metadataBytes, _ := json.Marshal(metadataMap)
		auditLog.Metadata = metadataBytes
	}

	// Extract request ID from context if available
	requestID := ctx.Value(utils.RequestIDKey)
	if requestID != nil {
		requestIDStr, ok := requestID.(string)
		if ok {
			auditLog.RequestID = &requestIDStr
		}
	}

	err := auditRepo.Save(ctx, auditLog)
	if err != nil {
		return err
	}

	return nil
}

func getCustomer(ctx context.Context, customerRepo repository.CustomerRepository, customerID uint) (models.Customer, error) {
	// Verify customer exists and is active
	customer, err := customerRepo.ByID(ctx, customerID)
	if err != nil {
		return models.Customer{}, err
	}
	if customer == nil {
		return models.Customer{}, ErrCustomerNotFound
	}
	if !utils.IsTrue(customer.IsActive) {
		return models.Customer{}, ErrAccountInactive
	}

	return *customer, nil
}

func getSystemUser(
	ctx context.Context,
	customerRepo repository.CustomerRepository,
	walletRepo repository.WalletRepository,
	sysCfg config.SystemConfig,
) (models.Customer, error) {
	wallet, err := getSystemWallet(ctx, walletRepo, sysCfg)
	if err != nil {
		return models.Customer{}, err
	}

	customer, err := customerRepo.ByID(ctx, wallet.CustomerID)
	if err != nil {
		return models.Customer{}, err
	}
	if customer == nil {
		return models.Customer{}, ErrSystemUserNotFound
	}
	if !utils.IsTrue(customer.IsActive) {
		return models.Customer{}, ErrAccountInactive
	}

	return *customer, nil
}

func getAgency(ctx context.Context, customerRepo repository.CustomerRepository, agencyID uint) (models.Customer, error) {
	agency, err := customerRepo.ByID(ctx, agencyID)
	if err != nil {
		return models.Customer{}, err
	}
	if agency == nil {
		return models.Customer{}, ErrAgencyNotFound
	}
	if agency.AccountType.TypeName != models.AccountTypeMarketingAgency {
		return models.Customer{}, ErrAgencyNotFound
	}
	if agency.IsActive != nil && !*agency.IsActive {
		return models.Customer{}, ErrAgencyInactive
	}

	return *agency, nil
}

func getCampaign(
	ctx context.Context,
	campaignRepo repository.CampaignRepository,
	campaignUUID string,
	customerID uint,
) (models.Campaign, error) {
	// Get existing campaign
	campaign, err := campaignRepo.ByUUID(ctx, campaignUUID)
	if err != nil {
		return models.Campaign{}, err
	}
	if campaign == nil {
		return models.Campaign{}, ErrCampaignNotFound
	}

	// Verify ownership
	if campaign.CustomerID != customerID {
		return models.Campaign{}, ErrCampaignAccessDenied
	}

	return *campaign, nil
}

func getCampaignByID(
	ctx context.Context,
	campaignRepo repository.CampaignRepository,
	campaignID uint,
	customerID uint,
) (models.Campaign, error) {
	campaign, err := campaignRepo.ByID(ctx, campaignID)
	if err != nil {
		return models.Campaign{}, err
	}
	if campaign == nil {
		return models.Campaign{}, ErrCampaignNotFound
	}
	if campaign.CustomerID != customerID {
		return models.Campaign{}, ErrCampaignAccessDenied
	}

	return *campaign, nil
}

// canUpdateCampaign checks if a campaign can be updated based on its current status
func canUpdateCampaign(status models.CampaignStatus) bool {
	// Only campaigns with 'initiated' or 'in-progress' status can be updated
	// Campaigns with 'waiting-for-approval', 'approved', 'rejected', 'running', 'executed', 'canceled', or 'cancelled-by-admin' status
	// cannot be updated
	return status == models.CampaignStatusInitiated || status == models.CampaignStatusInProgress
}

// canCancelCampaign checks if a campaign can be cancelled based on its current status.
func canCancelCampaign(status models.CampaignStatus) bool {
	return status == models.CampaignStatusWaitingForApproval || status == models.CampaignStatusApproved
}

func getWallet(ctx context.Context, walletRepo repository.WalletRepository, customerID uint) (models.Wallet, error) {
	wallet, err := walletRepo.ByCustomerID(ctx, customerID)
	if err != nil {
		return models.Wallet{}, err
	}
	if wallet == nil {
		return models.Wallet{}, ErrWalletNotFound
	}

	return *wallet, nil
}

func getSystemWallet(ctx context.Context, walletRepo repository.WalletRepository, sysCfg config.SystemConfig) (models.Wallet, error) {
	wallet, err := walletRepo.ByUUID(ctx, sysCfg.SystemWalletUUID)
	if err != nil {
		return models.Wallet{}, err
	}
	if wallet == nil {
		return models.Wallet{}, ErrSystemWalletNotFound
	}

	return *wallet, nil
}

func getTaxWallet(ctx context.Context, walletRepo repository.WalletRepository, sysCfg config.SystemConfig) (models.Wallet, error) {
	wallet, err := walletRepo.ByUUID(ctx, sysCfg.TaxWalletUUID)
	if err != nil {
		return models.Wallet{}, err
	}
	if wallet == nil {
		return models.Wallet{}, ErrTaxWalletNotFound
	}

	return *wallet, nil
}

func getLatestBalanceSnapshot(ctx context.Context, walletRepo repository.WalletRepository, walletID uint) (models.BalanceSnapshot, error) {
	latestBalance, err := walletRepo.GetCurrentBalance(ctx, walletID)
	if err != nil {
		return models.BalanceSnapshot{}, err
	}
	if latestBalance == nil {
		return models.BalanceSnapshot{}, ErrBalanceSnapshotNotFound
	}

	return *latestBalance, nil
}

func getLatestTaxWalletBalanceSnapshot(
	ctx context.Context,
	walletRepo repository.WalletRepository,
	walletID uint,
) (models.BalanceSnapshot, error) {
	taxBalance, err := walletRepo.GetCurrentBalance(ctx, walletID)
	if err != nil {
		return models.BalanceSnapshot{}, err
	}
	if taxBalance == nil {
		return models.BalanceSnapshot{}, ErrTaxWalletBalanceSnapshotNotFound
	}

	return *taxBalance, nil
}

func getLatestSystemWalletBalanceSnapshot(
	ctx context.Context,
	walletRepo repository.WalletRepository,
	walletID uint,
) (models.BalanceSnapshot, error) {
	systemBalance, err := walletRepo.GetCurrentBalance(ctx, walletID)
	if err != nil {
		return models.BalanceSnapshot{}, err
	}
	if systemBalance == nil {
		return models.BalanceSnapshot{}, ErrSystemWalletBalanceSnapshotNotFound
	}

	return *systemBalance, nil
}

func createWalletWithInitialSnapshot(
	ctx context.Context,
	walletRepo repository.WalletRepository,
	customerID uint,
	source string,
) (models.Wallet, error) {
	meta := map[string]any{
		"source":     source,
		"created_at": utils.UTCNow(),
	}
	b, err := json.Marshal(meta)
	if err != nil {
		return models.Wallet{}, err
	}

	// Create new wallet for customer
	wallet := models.Wallet{
		UUID:       uuid.New(),
		CustomerID: customerID,
		Metadata:   json.RawMessage(b),
	}

	if err := walletRepo.SaveWithInitialSnapshot(ctx, &wallet); err != nil {
		return models.Wallet{}, err
	}

	return wallet, nil
}

func ValidateShebaNumber(shebaNumber *string) (string, error) {
	if shebaNumber == nil || len(*shebaNumber) == 0 {
		return "", ErrShebaNumberRequired
	}
	shebaNumberStr := strings.TrimSpace(*shebaNumber)
	// validate prefix IR
	if !strings.HasPrefix(shebaNumberStr, "IR") {
		return "", ErrShebaNumberInvalid
	}
	// validate length exactly 26
	if len(shebaNumberStr) != 26 {
		return "", ErrShebaNumberInvalid
	}
	// validate digits are numbers
	for _, s := range shebaNumberStr[2:] {
		if !unicode.IsDigit(s) {
			return "", ErrShebaNumberInvalid
		}
	}

	return shebaNumberStr, nil
}

func redisKey(cacheConfig config.CacheConfig, suffix string) string {
	prefix := cacheConfig.RedisPrefix
	if prefix == "" {
		prefix = "yamata"
	}
	return fmt.Sprintf("%s:%s", prefix, suffix)
}

// computeClickRate safely calculates a click-through rate as clicks/totalSent.
// Returns nil when totalSent is <= 0 to distinguish "no data" from "0% CTR".
// A nil return should be serialised as absent/null by callers, not as zero.
func computeClickRate(clicks int64, totalSent float64) *float64 {
	if totalSent <= 0 {
		return nil
	}
	rate := float64(clicks) / totalSent
	return &rate
}

// parseAggregatedTotalSentFromMap extracts the "aggregatedTotalSent" field from a
// campaign statistics map (already parsed from JSON). Returns 0 when the key is
// absent, nil, or has an unrecognised type.
func parseAggregatedTotalSentFromMap(statsMap map[string]any) float64 {
	if statsMap == nil {
		return 0
	}
	v, ok := statsMap["aggregatedTotalSent"]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case json.Number:
		if f, err := n.Float64(); err == nil {
			return f
		}
	}
	return 0
}

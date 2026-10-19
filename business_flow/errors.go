// Package businessflow contains the core business logic and use cases for authentication workflows
package businessflow

import (
	"errors"
	"fmt"
)

// Business flow error constants
var (
	// Customer-related errors
	ErrCustomerNotFound        = errors.New("customer not found")
	ErrAccountInactive         = errors.New("account is inactive")
	ErrAccountTypeNotFound     = errors.New("account type not found")
	ErrIncorrectPassword       = errors.New("incorrect password")
	ErrMobileNumberNotVerified = errors.New("mobile number not verified")
	ErrEmailAlreadyExists      = errors.New("email already exists")
	ErrMobileAlreadyExists     = errors.New("mobile number already exists")
	ErrNationalIDAlreadyExists = errors.New("national ID already exists")
	ErrNationalIDRequired      = errors.New("national ID is required")
	ErrAgencyNotFound          = errors.New("agency not found")
	ErrJobCategoryRequired     = errors.New("job and category are required for marketing agencies")
	ErrAgencyInactive          = errors.New("agency is inactive")

	// Company/Business account errors
	ErrCompanyFieldsRequired = errors.New("company fields are required for business accounts")
	ErrShebaNumberRequired   = errors.New("sheba number is required for marketing agencies")
	ErrShebaNumberInvalid    = errors.New("sheba number is invalid")

	// Referrer agency errors
	ErrReferrerAgencyNotFound            = errors.New("referrer agency not found")
	ErrReferrerMustBeAgency              = errors.New("referrer must be a marketing agency")
	ErrReferrerAgencyInactive            = errors.New("referrer agency is inactive")
	ErrReferrerAgencyShebaNumberRequired = errors.New("referrer agency sheba number is required")

	// OTP-related errors
	ErrNoValidOTPFound      = errors.New("no valid OTP found")
	ErrInvalidOTPCode       = errors.New("invalid OTP code")
	ErrInvalidOTPType       = errors.New("invalid OTP type")
	ErrOTPExpired           = errors.New("OTP has expired")
	ErrCacheNotAvailable    = errors.New("cache not available")
	ErrAuthenticationFailed = errors.New("authentication failed")
	ErrRateLimitExceeded    = errors.New("rate limit exceeded")

	ErrAlreadyVerified = errors.New("already verified")

	// Campaign-related errors
	ErrCampaignNotFound                         = errors.New("campaign not found")
	ErrCampaignAccessDenied                     = errors.New("campaign access denied")
	ErrCampaignUpdateNotAllowed                 = errors.New("campaign update not allowed")
	ErrInsufficientCampaignCapacity             = errors.New("insufficient campaign capacity")
	ErrCampaignTitleRequired                    = errors.New("campaign title is required")
	ErrCampaignContentRequired                  = errors.New("campaign content is required")
	ErrCampaignLevel1Required                   = errors.New("campaign level 1 is required")
	ErrCampaignLevel2sRequired                  = errors.New("at least one campaign level 2 is required")
	ErrCampaignLevel3sRequired                  = errors.New("at least one campaign level 3 is required")
	ErrCampaignLineNumberRequired               = errors.New("campaign line number is required")
	ErrCampaignLineNumberNotActive              = errors.New("campaign line number is not active")
	ErrCampaignBudgetRequired                   = errors.New("campaign budget is required")
	ErrCampaignSexRequired                      = errors.New("campaign sex is required")
	ErrCampaignAdLinkRequired                   = errors.New("campaign ad link is required")
	ErrInvalidShortLinkDomain                   = errors.New("invalid short link domain")
	ErrAgencyCategoryJobRequired                = errors.New("category and job are required for marketing agencies")
	ErrScheduleTimeNotPresent                   = errors.New("schedule time is not present")
	ErrScheduleTimeTooSoon                      = errors.New("schedule time is too soon")
	ErrScheduleTimeTooCloseToCurrent            = errors.New("current schedule time is too close to allow reschedule")
	ErrScheduleTimeTooCloseToCancel             = errors.New("current schedule time is too close to allow cancel")
	ErrScheduleTimeMustBeUTC                    = errors.New("schedule time must be UTC")
	ErrCampaignCityRequired                     = errors.New("campaign city is required")
	ErrCampaignTagsRequired                     = errors.New("campaign tags is required")
	ErrCampaignAudienceGradesInvalid            = errors.New("campaign audience grades are invalid")
	ErrCampaignUpdateRequired                   = errors.New("at least one field must be provided for update")
	ErrCampaignUUIDRequired                     = errors.New("campaign UUID is required")
	ErrCampaignUUIDInvalid                      = errors.New("campaign UUID is invalid")
	ErrCampaignMediaNotFound                    = errors.New("campaign media not found")
	ErrCampaignTargetAudienceExcelMediaNotFound = errors.New("campaign target audience excel media not found")
	ErrCampaignTargetAudienceExcelFileInvalid   = errors.New("campaign target audience excel file is invalid")
	ErrCampaignLineNumberNotApplicable          = errors.New("campaign line number is not applicable for this platform")
	ErrCampaignPlatformSettingRequired          = errors.New("campaign platform settings is required")
	ErrCampaignPlatformSettingNotApplicable     = errors.New("campaign platform settings is not applicable for this platform")
	ErrCampaignPlatformSettingNotFound          = errors.New("campaign platform settings not found")
	ErrCampaignPlatformRequired                 = errors.New("campaign platform is required")
	ErrCampaignPlatformInvalid                  = errors.New("campaign platform is invalid")
	ErrCampaignBudgetOutOfRange                 = errors.New("campaign budget must be between 100000 and 160000000 tomans")
	ErrScheduleTimeOutsideWindow                = errors.New("schedule time must be between 08:00 and 21:00 Asia/Tehran")
	ErrCampaignRescheduleNotAllowed             = errors.New("campaign cannot be rescheduled in its current status")
	ErrCampaignTestStateNotAllowed              = errors.New("campaign state does not allow test sending")
	ErrCampaignTestRateLimited                  = errors.New("campaign test send is rate limited")
	ErrCampaignTestRecipientMissing             = errors.New("campaign test recipient is missing")
	ErrCampaignTestPlatformSettingsInvalid      = errors.New("campaign test platform settings are invalid")
	ErrCampaignTestCooldownUnavailable          = errors.New("campaign test cooldown is unavailable")
	ErrCampaignAudienceTargetingMethodInvalid   = errors.New("campaign audience targeting method is invalid")
	ErrSmartTargetingTagsRequired               = errors.New("at least one tag must be selected for Smart Targeting")
	ErrSmartTargetingTagInvalid                 = errors.New("one or more selected tags are unavailable for this bundle")
	ErrSmartTargetingTagCapacityInvalid         = errors.New("smart targeting tag capacity must be non-negative")
	ErrSmartTargetingSortInvalid                = errors.New("smart targeting sort field or direction is invalid")
	ErrSmartTargetingScoreUnavailable           = errors.New("bundle persona fit score is unavailable")
	ErrSmartTargetingSearchTooLong              = errors.New("smart targeting search must not exceed 200 characters")
	ErrSmartTargetingCountInvalid               = errors.New("automatic selection count must be between 1 and 10000")
	ErrSmartTargetingSampleSizeRequired         = errors.New("sample_size_per_tag is required for Smart Targeting Test campaigns")
	ErrSmartTargetingSampleSizeInvalid          = errors.New("sample_size_per_tag must be a positive integer")
	ErrSmartTargetingTestPreviewRequired        = errors.New("a current Smart Targeting Test sampling preview is required")
	ErrSmartTargetingTestNoSatisfiedTags        = errors.New("no selected Smart Targeting tag can satisfy sample_size_per_tag")
	ErrSmartTargetingTestAudienceCountOverflow  = errors.New("Smart Targeting Test audience count exceeds the supported range")
	ErrCampaignCostOverflow                     = errors.New("campaign cost exceeds the supported range")

	ErrCampaignNotWaitingForApproval          = errors.New("campaign is not waiting for approval")
	ErrCampaignNotApproved                    = errors.New("campaign is not approved")
	ErrCampaignNotRunning                     = errors.New("campaign is not running")
	ErrFreezeTransactionNotFound              = errors.New("freeze transaction not found for campaign")
	ErrMultipleFreezeTransactionsFound        = errors.New("multiple freeze transactions found for campaign")
	ErrCampaignDebitTransactionNotFound       = errors.New("campaign debit transaction not found")
	ErrMultipleCampaignDebitTransactionsFound = errors.New("multiple campaign debit transactions found")

	// Payment-related errors
	ErrWalletNotFound           = errors.New("wallet not found")
	ErrAmountTooLow             = errors.New("amount is too low")
	ErrAmountNotMultiple        = errors.New("amount must be a multiple of 10000")
	ErrAtipayTokenEmpty         = errors.New("atipay token is empty")
	ErrInsufficientFunds        = errors.New("insufficient funds")
	ErrInvalidLanguage          = errors.New("invalid language")
	ErrReferrerAgencyIDRequired = errors.New("referrer agency ID is required")
	ErrAgencyDiscountNotFound   = errors.New("agency discount not found")

	// Payment callback errors
	ErrCallbackRequestNil             = errors.New("callback request is nil")
	ErrReservationNumberRequired      = errors.New("reservation number is required")
	ErrReferenceNumberRequired        = errors.New("reference number is required")
	ErrStatusRequired                 = errors.New("status is required")
	ErrStateRequired                  = errors.New("state is required")
	ErrPaymentRequestNotFound         = errors.New("payment request not found")
	ErrPaymentRequestAlreadyProcessed = errors.New("payment request already processed")
	ErrPaymentRequestExpired          = errors.New("payment request expired")
	ErrTransactionNotFound            = errors.New("transaction not found")
	ErrTransactionUUIDInvalid         = errors.New("transaction uuid is invalid")
	ErrInvoiceIssueRequestRateLimited = errors.New("invoice issue request is rate limited")
	ErrInvoiceIssueAlreadyAssigned    = errors.New("invoice is already assigned to transaction")
	ErrInvoiceUUIDRequired            = errors.New("invoice uuid is required")
	ErrInvoiceUUIDInvalid             = errors.New("invoice uuid is invalid")
	ErrInvoiceUUIDMismatch            = errors.New("invoice uuid does not match existing value")

	// Balance snapshot errors
	ErrBalanceSnapshotNotFound = errors.New("balance snapshot not found")

	// Tax and system user errors
	ErrSystemUserNotFound            = errors.New("system user not found")
	ErrSystemUserShebaNumberNotFound = errors.New("system user sheba number not found")

	// Tax and System wallet errors
	ErrTaxWalletNotFound                   = errors.New("tax wallet not found")
	ErrTaxWalletBalanceSnapshotNotFound    = errors.New("tax wallet balance snapshot not found")
	ErrSystemWalletNotFound                = errors.New("system wallet not found")
	ErrSystemWalletBalanceSnapshotNotFound = errors.New("system wallet balance snapshot not found")

	// Filter errors
	ErrInvalidPage           = errors.New("page must be at least 1")
	ErrInvalidPageSize       = errors.New("page size must be between 1 and 100")
	ErrStartDateAfterEndDate = errors.New("start date cannot be after end date")

	// Agency discount errors
	ErrDiscountRateOutOfRange              = errors.New("discount rate must be between 0 and 0.5")
	ErrAgencyCannotCreateDiscountForItself = errors.New("agency cannot create discount for itself")
	ErrCustomerNotUnderAgency              = errors.New("customer is not under this agency")
	ErrAgencyCannotListDiscountsForItself  = errors.New("agency cannot list discounts for itself")

	// Admin and captcha related errors
	ErrAdminNotFound               = errors.New("admin not found")
	ErrAdminInactive               = errors.New("admin account is inactive")
	ErrInvalidCaptcha              = errors.New("invalid captcha")
	ErrAdminTwoFactorNotConfigured = errors.New("admin two-factor mobile is not configured")

	// Bot related errors
	ErrBotNotFound = errors.New("bot not found")
	ErrBotInactive = errors.New("bot account is inactive")

	// Line number related errors
	ErrLineNumberValueRequired = errors.New("line number value is required")
	ErrPriceFactorInvalid      = errors.New("price factor must be greater than zero")
	ErrLineNumberAlreadyExists = errors.New("line number already exists")
	ErrLineNumberNotFound      = errors.New("line number not found")
	ErrLineNumberNotActive     = errors.New("line number is not active")

	// Segment price factor errors
	ErrLevel3Required                    = errors.New("level3 is required")
	ErrSegmentPriceFactorNotFound        = errors.New("segment price factor not found")
	ErrSegmentPriceFactorPlatformInvalid = errors.New("segment price factor platform is invalid")
	ErrAudienceSpecPlatformRequired      = errors.New("audience spec platform is required")
	ErrAudienceSpecPlatformInvalid       = errors.New("audience spec platform is invalid")

	// Ticket related errors
	ErrTicketNotFound = errors.New("ticket not found")

	// Short link related errors
	ErrShortLinkNotFound = errors.New("short link not found")

	// Bundle related errors
	ErrBundleNotFound        = errors.New("bundle not found")
	ErrBundleAccessDenied    = errors.New("bundle access denied")
	ErrCampaignPhaseRequired = errors.New("phase is required")
	ErrCampaignPhaseInvalid  = errors.New("phase is invalid")

	// Crypto payments
	ErrCryptoRequestNotFound         = errors.New("crypto payment request not found")
	ErrCryptoRequestAlreadyFinalized = errors.New("crypto payment request already finalized")
	ErrCryptoUnsupportedCoin         = errors.New("unsupported crypto coin")
	ErrCryptoUnsupportedPlatform     = errors.New("unsupported crypto platform")
	ErrCryptoAddressProvisionFailed  = errors.New("failed to provision deposit address")
	ErrCryptoProviderError           = errors.New("crypto provider error")
	ErrCryptoDepositNotFound         = errors.New("crypto deposit not found")

	// Deposit receipts
	ErrDepositReceiptNotFound         = errors.New("deposit receipt not found")
	ErrDepositReceiptAlreadyFinalized = errors.New("deposit receipt already finalized")
	ErrDepositReceiptAlreadyApproved  = errors.New("deposit receipt already approved")
	ErrDepositReceiptAlreadyRejected  = errors.New("deposit receipt already rejected")
	ErrDepositReceiptInvalidStatus    = errors.New("deposit receipt status is invalid")
	ErrDepositReceiptInvoiceRequired  = errors.New("invoice uuid is required for approved deposit receipt")
	ErrDepositReceiptInvoiceInvalid   = errors.New("invoice uuid is invalid")
	ErrDepositReceiptInvoiceDuplicate = errors.New("invoice uuid is already linked to another deposit receipt payment")
	ErrDepositReceiptFileTooLarge     = errors.New("deposit receipt file too large")
	ErrDepositReceiptFileInvalidType  = errors.New("deposit receipt file type is not allowed")
	ErrDepositReceiptFileEmpty        = errors.New("deposit receipt file is empty")

	// Platform base prices
	ErrPlatformBasePriceNotFound  = errors.New("platform base price not found")
	ErrPlatformSettingsNameExists = errors.New("platform settings name already exists for this customer")

	// Page price
	ErrPagePriceNotFound = errors.New("page base price not found")

	ErrNotFound     = errors.New("not found")
	ErrInvalidState = errors.New("invalid state")
	ErrForbidden    = errors.New("forbidden")
)

type BusinessError struct {
	Code    string
	Message string
	Err     error
}

func (e *BusinessError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	return e.Message
}

func (e *BusinessError) Unwrap() error {
	return e.Err
}

func NewBusinessError(code, message string, err error) *BusinessError {
	return &BusinessError{
		Code:    code,
		Message: message,
		Err:     err,
	}
}

func NewBusinessErrorf(code, message string, err error, args ...any) *BusinessError {
	return &BusinessError{
		Code:    code,
		Message: fmt.Sprintf(message, args...),
		Err:     err,
	}
}

func IsCustomerNotFound(err error) bool {
	return errors.Is(err, ErrCustomerNotFound)
}

func IsAccountInactive(err error) bool {
	return errors.Is(err, ErrAccountInactive)
}

func IsAccountTypeNotFound(err error) bool {
	return errors.Is(err, ErrAccountTypeNotFound)
}

func IsIncorrectPassword(err error) bool {
	return errors.Is(err, ErrIncorrectPassword)
}

func IsMobileNumberNotVerified(err error) bool {
	return errors.Is(err, ErrMobileNumberNotVerified)
}

func IsEmailAlreadyExists(err error) bool {
	return errors.Is(err, ErrEmailAlreadyExists)
}

func IsMobileAlreadyExists(err error) bool {
	return errors.Is(err, ErrMobileAlreadyExists)
}

func IsNationalIDAlreadyExists(err error) bool {
	return errors.Is(err, ErrNationalIDAlreadyExists)
}

func IsNationalIDRequired(err error) bool {
	return errors.Is(err, ErrNationalIDRequired)
}

func IsAgencyNotFound(err error) bool {
	return errors.Is(err, ErrAgencyNotFound)
}

func IsJobCategoryRequired(err error) bool {
	return errors.Is(err, ErrJobCategoryRequired)
}

func IsAgencyInactive(err error) bool {
	return errors.Is(err, ErrAgencyInactive)
}

func IsCompanyFieldsRequired(err error) bool {
	return errors.Is(err, ErrCompanyFieldsRequired)
}

func IsShebaNumberRequired(err error) bool {
	return errors.Is(err, ErrShebaNumberRequired)
}

func IsShebaNumberInvalid(err error) bool {
	return errors.Is(err, ErrShebaNumberInvalid)
}

func IsCampaignNotFound(err error) bool {
	return errors.Is(err, ErrCampaignNotFound)
}

func IsCampaignAccessDenied(err error) bool {
	return errors.Is(err, ErrCampaignAccessDenied)
}

func IsCampaignUpdateNotAllowed(err error) bool {
	return errors.Is(err, ErrCampaignUpdateNotAllowed)
}

func IsInsufficientCampaignCapacity(err error) bool {
	return errors.Is(err, ErrInsufficientCampaignCapacity)
}

func IsCampaignTitleRequired(err error) bool {
	return errors.Is(err, ErrCampaignTitleRequired)
}

func IsCampaignContentRequired(err error) bool {
	return errors.Is(err, ErrCampaignContentRequired)
}

func IsCampaignLevel1Required(err error) bool {
	return errors.Is(err, ErrCampaignLevel1Required)
}

func IsCampaignLevel2sRequired(err error) bool {
	return errors.Is(err, ErrCampaignLevel2sRequired)
}

func IsCampaignLevel3sRequired(err error) bool {
	return errors.Is(err, ErrCampaignLevel3sRequired)
}

func IsCampaignLineNumberRequired(err error) bool {
	return errors.Is(err, ErrCampaignLineNumberRequired)
}

func IsCampaignLineNumberNotApplicable(err error) bool {
	return errors.Is(err, ErrCampaignLineNumberNotApplicable)
}

func IsCampaignLineNumberNotActive(err error) bool {
	return errors.Is(err, ErrCampaignLineNumberNotActive)
}

func IsCampaignBudgetRequired(err error) bool {
	return errors.Is(err, ErrCampaignBudgetRequired)
}

func IsCampaignBudgetOutOfRange(err error) bool {
	return errors.Is(err, ErrCampaignBudgetOutOfRange)
}

func IsCampaignSexRequired(err error) bool {
	return errors.Is(err, ErrCampaignSexRequired)
}

func IsCampaignAdLinkRequired(err error) bool {
	return errors.Is(err, ErrCampaignAdLinkRequired)
}

func IsInvalidShortLinkDomain(err error) bool {
	return errors.Is(err, ErrInvalidShortLinkDomain)
}

func IsAgencyCategoryJobRequired(err error) bool {
	return errors.Is(err, ErrAgencyCategoryJobRequired)
}

func IsScheduleTimeNotPresent(err error) bool {
	return errors.Is(err, ErrScheduleTimeNotPresent)
}

func IsScheduleTimeTooSoon(err error) bool {
	return errors.Is(err, ErrScheduleTimeTooSoon)
}

func IsScheduleTimeTooCloseToCurrent(err error) bool {
	return errors.Is(err, ErrScheduleTimeTooCloseToCurrent)
}

func IsScheduleTimeTooCloseToCancel(err error) bool {
	return errors.Is(err, ErrScheduleTimeTooCloseToCancel)
}

func IsScheduleTimeMustBeUTC(err error) bool {
	return errors.Is(err, ErrScheduleTimeMustBeUTC)
}

func IsScheduleTimeOutsideWindow(err error) bool {
	return errors.Is(err, ErrScheduleTimeOutsideWindow)
}

func IsCampaignRescheduleNotAllowed(err error) bool {
	return errors.Is(err, ErrCampaignRescheduleNotAllowed)
}

func IsCampaignCityRequired(err error) bool {
	return errors.Is(err, ErrCampaignCityRequired)
}

func IsCampaignTagsRequired(err error) bool {
	return errors.Is(err, ErrCampaignTagsRequired)
}

func IsCampaignAudienceGradesInvalid(err error) bool {
	return errors.Is(err, ErrCampaignAudienceGradesInvalid)
}

func IsCampaignUpdateRequired(err error) bool {
	return errors.Is(err, ErrCampaignUpdateRequired)
}

func IsCampaignUUIDRequired(err error) bool {
	return errors.Is(err, ErrCampaignUUIDRequired)
}

func IsCampaignPlatformRequired(err error) bool {
	return errors.Is(err, ErrCampaignPlatformRequired)
}

func IsCampaignPlatformInvalid(err error) bool {
	return errors.Is(err, ErrCampaignPlatformInvalid)
}

func IsCampaignPlatformSettingRequired(err error) bool {
	return errors.Is(err, ErrCampaignPlatformSettingRequired)
}

func IsCampaignPlatformSettingNotApplicable(err error) bool {
	return errors.Is(err, ErrCampaignPlatformSettingNotApplicable)
}

func IsCampaignMediaNotFound(err error) bool {
	return errors.Is(err, ErrCampaignMediaNotFound)
}

func IsCampaignTargetAudienceExcelMediaNotFound(err error) bool {
	return errors.Is(err, ErrCampaignTargetAudienceExcelMediaNotFound)
}

func IsCampaignTargetAudienceExcelFileInvalid(err error) bool {
	return errors.Is(err, ErrCampaignTargetAudienceExcelFileInvalid)
}

func IsCampaignPlatformSettingNotFound(err error) bool {
	return errors.Is(err, ErrCampaignPlatformSettingNotFound)
}

func IsCampaignTestStateNotAllowed(err error) bool {
	return errors.Is(err, ErrCampaignTestStateNotAllowed)
}

func IsCampaignTestRateLimited(err error) bool {
	return errors.Is(err, ErrCampaignTestRateLimited)
}

func IsCampaignTestRecipientMissing(err error) bool {
	return errors.Is(err, ErrCampaignTestRecipientMissing)
}

func IsCampaignTestPlatformSettingsInvalid(err error) bool {
	return errors.Is(err, ErrCampaignTestPlatformSettingsInvalid)
}

func IsCampaignTestCooldownUnavailable(err error) bool {
	return errors.Is(err, ErrCampaignTestCooldownUnavailable)
}

func IsCampaignNotWaitingForApproval(err error) bool {
	return errors.Is(err, ErrCampaignNotWaitingForApproval)
}

func IsCampaignNotApproved(err error) bool {
	return errors.Is(err, ErrCampaignNotApproved)
}

func IsFreezeTransactionNotFound(err error) bool {
	return errors.Is(err, ErrFreezeTransactionNotFound)
}

func IsMultipleFreezeTransactionsFound(err error) bool {
	return errors.Is(err, ErrMultipleFreezeTransactionsFound)
}

func IsCampaignDebitTransactionNotFound(err error) bool {
	return errors.Is(err, ErrCampaignDebitTransactionNotFound)
}

func IsMultipleCampaignDebitTransactionsFound(err error) bool {
	return errors.Is(err, ErrMultipleCampaignDebitTransactionsFound)
}

func IsReferrerAgencyNotFound(err error) bool {
	return errors.Is(err, ErrReferrerAgencyNotFound)
}

func IsReferrerMustBeAgency(err error) bool {
	return errors.Is(err, ErrReferrerMustBeAgency)
}

func IsReferrerAgencyInactive(err error) bool {
	return errors.Is(err, ErrReferrerAgencyInactive)
}

func IsReferrerAgencyShebaNumberRequired(err error) bool {
	return errors.Is(err, ErrReferrerAgencyShebaNumberRequired)
}

func IsNoValidOTPFound(err error) bool {
	return errors.Is(err, ErrNoValidOTPFound)
}

func IsInvalidOTPCode(err error) bool {
	return errors.Is(err, ErrInvalidOTPCode)
}

func IsInvalidOTPType(err error) bool {
	return errors.Is(err, ErrInvalidOTPType)
}

func IsOTPExpired(err error) bool {
	return errors.Is(err, ErrOTPExpired)
}

func IsAuthenticationFailed(err error) bool {
	return errors.Is(err, ErrAuthenticationFailed)
}

func IsRateLimitExceeded(err error) bool {
	return errors.Is(err, ErrRateLimitExceeded)
}

func IsAdminTwoFactorNotConfigured(err error) bool {
	return errors.Is(err, ErrAdminTwoFactorNotConfigured)
}

func IsCacheNotAvailable(err error) bool {
	return errors.Is(err, ErrCacheNotAvailable)
}

func IsAlreadyVerified(err error) bool {
	return errors.Is(err, ErrAlreadyVerified)
}

func IsWalletNotFound(err error) bool {
	return errors.Is(err, ErrWalletNotFound)
}

func IsAmountTooLow(err error) bool {
	return errors.Is(err, ErrAmountTooLow)
}

func IsAmountNotMultiple(err error) bool {
	return errors.Is(err, ErrAmountNotMultiple)
}

func IsAtipayTokenEmpty(err error) bool {
	return errors.Is(err, ErrAtipayTokenEmpty)
}

func IsInvalidLanguage(err error) bool {
	return errors.Is(err, ErrInvalidLanguage)
}

func IsInsufficientFunds(err error) bool {
	return errors.Is(err, ErrInsufficientFunds)
}

func IsReferrerAgencyIDRequired(err error) bool {
	return errors.Is(err, ErrReferrerAgencyIDRequired)
}

func IsAgencyDiscountNotFound(err error) bool {
	return errors.Is(err, ErrAgencyDiscountNotFound)
}

func IsCallbackRequestNil(err error) bool {
	return errors.Is(err, ErrCallbackRequestNil)
}

func IsReservationNumberRequired(err error) bool {
	return errors.Is(err, ErrReservationNumberRequired)
}

func IsReferenceNumberRequired(err error) bool {
	return errors.Is(err, ErrReferenceNumberRequired)
}

func IsStatusRequired(err error) bool {
	return errors.Is(err, ErrStatusRequired)
}

func IsStateRequired(err error) bool {
	return errors.Is(err, ErrStateRequired)
}

func IsPaymentRequestNotFound(err error) bool {
	return errors.Is(err, ErrPaymentRequestNotFound)
}

func IsPaymentRequestAlreadyProcessed(err error) bool {
	return errors.Is(err, ErrPaymentRequestAlreadyProcessed)
}

func IsPaymentRequestExpired(err error) bool {
	return errors.Is(err, ErrPaymentRequestExpired)
}

func IsTransactionNotFound(err error) bool {
	return errors.Is(err, ErrTransactionNotFound)
}

func IsTransactionUUIDInvalid(err error) bool {
	return errors.Is(err, ErrTransactionUUIDInvalid)
}

func IsInvoiceIssueRequestRateLimited(err error) bool {
	return errors.Is(err, ErrInvoiceIssueRequestRateLimited)
}
func IsInvoiceIssueAlreadyAssigned(err error) bool {
	return errors.Is(err, ErrInvoiceIssueAlreadyAssigned)
}

func IsInvoiceUUIDRequired(err error) bool {
	return errors.Is(err, ErrInvoiceUUIDRequired)
}

func IsInvoiceUUIDInvalid(err error) bool {
	return errors.Is(err, ErrInvoiceUUIDInvalid)
}

func IsInvoiceUUIDMismatch(err error) bool {
	return errors.Is(err, ErrInvoiceUUIDMismatch)
}

func IsBalanceSnapshotNotFound(err error) bool {
	return errors.Is(err, ErrBalanceSnapshotNotFound)
}

func IsSystemUserNotFound(err error) bool {
	return errors.Is(err, ErrSystemUserNotFound)
}

func IsSystemUserShebaNumberNotFound(err error) bool {
	return errors.Is(err, ErrSystemUserShebaNumberNotFound)
}

func IsTaxWalletNotFound(err error) bool {
	return errors.Is(err, ErrTaxWalletNotFound)
}

func IsTaxWalletBalanceSnapshotNotFound(err error) bool {
	return errors.Is(err, ErrTaxWalletBalanceSnapshotNotFound)
}

func IsSystemWalletNotFound(err error) bool {
	return errors.Is(err, ErrSystemWalletNotFound)
}

func IsSystemWalletBalanceSnapshotNotFound(err error) bool {
	return errors.Is(err, ErrSystemWalletBalanceSnapshotNotFound)
}

func IsInvalidPage(err error) bool {
	return errors.Is(err, ErrInvalidPage)
}

func IsInvalidPageSize(err error) bool {
	return errors.Is(err, ErrInvalidPageSize)
}

func IsStartDateAfterEndDate(err error) bool {
	return errors.Is(err, ErrStartDateAfterEndDate)
}

func IsDiscountRateOutOfRange(err error) bool {
	return errors.Is(err, ErrDiscountRateOutOfRange)
}

func IsAgencyCannotCreateDiscountForItself(err error) bool {
	return errors.Is(err, ErrAgencyCannotCreateDiscountForItself)
}

func IsCustomerNotUnderAgency(err error) bool {
	return errors.Is(err, ErrCustomerNotUnderAgency)
}

func IsAgencyCannotListDiscountsForItself(err error) bool {
	return errors.Is(err, ErrAgencyCannotListDiscountsForItself)
}

func IsAdminNotFound(err error) bool {
	return errors.Is(err, ErrAdminNotFound)
}

func IsAdminInactive(err error) bool {
	return errors.Is(err, ErrAdminInactive)
}

func IsInvalidCaptcha(err error) bool {
	return errors.Is(err, ErrInvalidCaptcha)
}

func IsBotNotFound(err error) bool {
	return errors.Is(err, ErrBotNotFound)
}

func IsBotInactive(err error) bool {
	return errors.Is(err, ErrBotInactive)
}

func IsLineNumberValueRequired(err error) bool {
	return errors.Is(err, ErrLineNumberValueRequired)
}

func IsPriceFactorInvalid(err error) bool {
	return errors.Is(err, ErrPriceFactorInvalid)
}

func IsLineNumberAlreadyExists(err error) bool {
	return errors.Is(err, ErrLineNumberAlreadyExists)
}

func IsLineNumberNotFound(err error) bool {
	return errors.Is(err, ErrLineNumberNotFound)
}

func IsLineNumberNotActive(err error) bool {
	return errors.Is(err, ErrLineNumberNotActive)
}

func IsLevel3Required(err error) bool {
	return errors.Is(err, ErrLevel3Required)
}

func IsSegmentPriceFactorNotFound(err error) bool {
	return errors.Is(err, ErrSegmentPriceFactorNotFound)
}

func IsSegmentPriceFactorPlatformInvalid(err error) bool {
	return errors.Is(err, ErrSegmentPriceFactorPlatformInvalid)
}

func IsAudienceSpecPlatformRequired(err error) bool {
	return errors.Is(err, ErrAudienceSpecPlatformRequired)
}

func IsAudienceSpecPlatformInvalid(err error) bool {
	return errors.Is(err, ErrAudienceSpecPlatformInvalid)
}

func IsTicketNotFound(err error) bool {
	return errors.Is(err, ErrTicketNotFound)
}

func IsShortLinkNotFound(err error) bool {
	return errors.Is(err, ErrShortLinkNotFound)
}

func IsCryptoRequestNotFound(err error) bool { return errors.Is(err, ErrCryptoRequestNotFound) }
func IsCryptoRequestAlreadyFinalized(err error) bool {
	return errors.Is(err, ErrCryptoRequestAlreadyFinalized)
}
func IsCryptoUnsupportedCoin(err error) bool     { return errors.Is(err, ErrCryptoUnsupportedCoin) }
func IsCryptoUnsupportedPlatform(err error) bool { return errors.Is(err, ErrCryptoUnsupportedPlatform) }
func IsCryptoAddressProvisionFailed(err error) bool {
	return errors.Is(err, ErrCryptoAddressProvisionFailed)
}
func IsCryptoProviderError(err error) bool   { return errors.Is(err, ErrCryptoProviderError) }
func IsCryptoDepositNotFound(err error) bool { return errors.Is(err, ErrCryptoDepositNotFound) }

func IsDepositReceiptNotFound(err error) bool { return errors.Is(err, ErrDepositReceiptNotFound) }
func IsDepositReceiptAlreadyApproved(err error) bool {
	return errors.Is(err, ErrDepositReceiptAlreadyApproved)
}
func IsDepositReceiptAlreadyRejected(err error) bool {
	return errors.Is(err, ErrDepositReceiptAlreadyRejected)
}
func IsDepositReceiptInvalidStatus(err error) bool {
	return errors.Is(err, ErrDepositReceiptInvalidStatus)
}
func IsDepositReceiptAlreadyFinalized(err error) bool {
	return errors.Is(err, ErrDepositReceiptAlreadyFinalized)
}
func IsDepositReceiptInvoiceRequired(err error) bool {
	return errors.Is(err, ErrDepositReceiptInvoiceRequired)
}
func IsDepositReceiptInvoiceInvalid(err error) bool {
	return errors.Is(err, ErrDepositReceiptInvoiceInvalid)
}
func IsDepositReceiptInvoiceDuplicate(err error) bool {
	return errors.Is(err, ErrDepositReceiptInvoiceDuplicate)
}
func IsDepositReceiptFileTooLarge(err error) bool {
	return errors.Is(err, ErrDepositReceiptFileTooLarge)
}
func IsDepositReceiptFileInvalidType(err error) bool {
	return errors.Is(err, ErrDepositReceiptFileInvalidType)
}
func IsDepositReceiptFileEmpty(err error) bool {
	return errors.Is(err, ErrDepositReceiptFileEmpty)
}

func IsPlatformBasePriceNotFound(err error) bool {
	return errors.Is(err, ErrPlatformBasePriceNotFound)
}

func IsPlatformSettingsNameExists(err error) bool {
	return errors.Is(err, ErrPlatformSettingsNameExists)
}

func IsPagePriceNotFound(err error) bool {
	return errors.Is(err, ErrPagePriceNotFound)
}

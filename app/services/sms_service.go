// Package services provides external service integrations and technical concerns like notifications and tokens
package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/observability"
	"github.com/amirphl/Yamata-no-Orochi/config"
	"github.com/amirphl/Yamata-no-Orochi/utils"
)

// SMSService handles SMS sending operations
type SMSService interface {
	SendOTP(ctx context.Context, recipient, message string, customerID *int64) error
	SendSMS(ctx context.Context, recipient, message string, customerID *int64) error
	SendBulk(ctx context.Context, recipients []string, message string, customerID *int64) error
}

// SMSServiceImpl implements SMSService
type SMSServiceImpl struct {
	config *config.SMSConfig
	client *http.Client
}

// SMSRequest represents the request payload for SMS API
type SMSRequest struct {
	SrcNum         string `json:"srcNum"`               // Format: 98**********
	Recipient      string `json:"recipient"`            // Format: 98**********
	Body           string `json:"body"`                 // Message content
	CustomerID     *int64 `json:"customerId,omitempty"` // Optional customer ID
	RetryCount     int    `json:"retryCount"`           // Number of retries
	Type           int    `json:"type"`                 // Always 1
	ValidityPeriod int    `json:"validityPeriod"`       // Validity in seconds
}

// SMSResponse represents individual message result from SMS API
type SMSResponse struct {
	MessageID  int64  `json:"messageId"`
	SrcNum     string `json:"srcNum"`
	Recipient  string `json:"recipient"`
	CustomerID *int64 `json:"customerId,omitempty"`
	Status     string `json:"status"`
	StatusCode int    `json:"statusCode"`
}

// NewSMSService creates a new SMS service instance
func NewSMSService(cfg *config.SMSConfig) SMSService {
	return &SMSServiceImpl{
		config: cfg,
		client: &http.Client{
			Timeout: cfg.Timeout,
		},
	}
}

// SendOTP sends an OTP message via SMS
func (s *SMSServiceImpl) SendOTP(ctx context.Context, recipient, message string, customerID *int64) error {
	return s.SendSMS(ctx, recipient, message, customerID)
}

// SendSMS sends an SMS message
func (s *SMSServiceImpl) SendSMS(ctx context.Context, recipient, message string, customerID *int64) error {
	return s.SendBulk(ctx, []string{recipient}, message, customerID)
}

// SendBulk sends an SMS message to multiple recipients in a single API call (batch)
func (s *SMSServiceImpl) SendBulk(ctx context.Context, recipients []string, message string, customerID *int64) error {
	if len(recipients) == 0 {
		return nil
	}
	requests := make([]SMSRequest, 0, len(recipients))
	for _, r := range recipients {
		requests = append(requests, SMSRequest{
			SrcNum:         s.config.SourceNumber,
			Recipient:      r,
			Body:           message,
			CustomerID:     customerID,
			RetryCount:     s.config.RetryCount,
			Type:           1,
			ValidityPeriod: s.config.ValidityPeriod,
		})
	}

	requestBody, err := json.Marshal(requests)
	if err != nil {
		return fmt.Errorf("failed to marshal SMS bulk request: %w", err)
	}

	url := fmt.Sprintf("https://%s/api/v3.0.1/send", s.config.ProviderDomain)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(requestBody))
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", s.config.APIKey)

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send SMS bulk request: %w", err)
	}
	defer resp.Body.Close()

	var results []SMSResponse
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return fmt.Errorf("failed to decode SMS bulk response: %w", err)
	}
	for _, r := range results {
		if r.StatusCode != 200 || r.Status != "ACCEPTED" {
			return fmt.Errorf("SMS delivery failed for %s: %s (%d)", r.Recipient, r.Status, r.StatusCode)
		}
	}
	return nil
}

// MockSMSService implements SMSService for testing
type MockSMSService struct {
	mu           sync.Mutex
	redact       bool
	otpOutbox    string
	SentMessages []MockSMSMessage
}

// NewMockOTPService writes simulated OTP messages to a private operator-only
// outbox rather than putting authentication codes in application logs.
func NewMockOTPService(path string) SMSService {
	return &MockSMSService{redact: true, otpOutbox: path, SentMessages: make([]MockSMSMessage, 0)}
}

// MockSMSMessage represents a mock SMS message
type MockSMSMessage struct {
	Recipient  string
	Message    string
	CustomerID *int64
	SentAt     time.Time
}

// NewMockSMSService creates a new mock SMS service
func NewMockSMSService(redact ...bool) SMSService {
	return &MockSMSService{
		redact:       len(redact) > 0 && redact[0],
		SentMessages: make([]MockSMSMessage, 0),
	}
}

// SendOTP sends a mock OTP message
func (m *MockSMSService) SendOTP(ctx context.Context, recipient, message string, customerID *int64) error {
	if m.otpOutbox != "" {
		m.mu.Lock()
		err := m.writeOTPOutbox(recipient, message)
		m.mu.Unlock()
		if err != nil {
			log.Printf("mock provider=sms action=otp_outbox error=%v", err)
		}
	}
	return m.send(ctx, []string{recipient}, message, customerID, "otp")
}

func (m *MockSMSService) writeOTPOutbox(recipient, message string) error {
	if err := os.MkdirAll(filepath.Dir(m.otpOutbox), 0700); err != nil {
		return err
	}
	if info, err := os.Lstat(m.otpOutbox); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("outbox is not a regular file")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	file, err := os.OpenFile(m.otpOutbox, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err = file.Chmod(0600); err != nil {
		return err
	}
	return json.NewEncoder(file).Encode(map[string]any{"recipient": recipient, "message": message, "time": time.Now().UTC()})
}

// SendSMS sends a mock SMS message
func (m *MockSMSService) SendSMS(ctx context.Context, recipient, message string, customerID *int64) error {
	return m.send(ctx, []string{recipient}, message, customerID, "send")
}

func (m *MockSMSService) SendBulk(ctx context.Context, recipients []string, message string, customerID *int64) error {
	return m.send(ctx, recipients, message, customerID, "send")
}

func (m *MockSMSService) send(ctx context.Context, recipients []string, message string, customerID *int64, action string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range recipients {
		mockMessage := MockSMSMessage{
			Recipient:  r,
			Message:    message,
			CustomerID: customerID,
			SentAt:     utils.UTCNow(),
		}
		if m.redact {
			fmt.Printf("mock provider=sms action=send recipient=***%s\n", maskMockRecipient(r))
		} else {
			fmt.Println("Mock SMS message sent:", mockMessage)
		}
		m.SentMessages = append(m.SentMessages, mockMessage)
	}
	observability.RecordMockOperation("sms", action, "accepted", len(recipients))
	return nil
}

func maskMockRecipient(recipient string) string {
	if len(recipient) <= 4 {
		return ""
	}
	return recipient[len(recipient)-4:]
}

// GetSentMessages returns all sent mock messages
func (m *MockSMSService) GetSentMessages() []MockSMSMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]MockSMSMessage(nil), m.SentMessages...)
}

// ClearSentMessages clears the sent messages list
func (m *MockSMSService) ClearSentMessages() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SentMessages = make([]MockSMSMessage, 0)
}

// Package services provides external service integrations and technical concerns like notifications and tokens
package services

import (
	"context"
	"fmt"
	"log"
	"strings"
)

// NotificationService handles sending notifications via SMS and email
type NotificationService interface {
	SendSMS(ctx context.Context, mobile, message string, customerID *int64) error
	SendSMSBulk(ctx context.Context, mobiles []string, message string, customerID *int64) error
	SendEmail(email, subject, message string) error
}

// NotificationServiceImpl implements NotificationService
type NotificationServiceImpl struct {
	smsService    SMSService
	emailProvider EmailProvider
}

// EmailProvider interface for email sending
type EmailProvider interface {
	SendEmail(email, subject, message string) error
}

// NewNotificationService creates a new notification service
func NewNotificationService(smsService SMSService, emailProvider EmailProvider) NotificationService {
	return &NotificationServiceImpl{
		smsService:    smsService,
		emailProvider: emailProvider,
	}
}

// SendSMS sends an SMS message to the specified mobile number
func (s *NotificationServiceImpl) SendSMS(ctx context.Context, mobile, message string, customerID *int64) error {
	if s.smsService == nil {
		return fmt.Errorf("SMS service not configured")
	}

	// Validate mobile format (convert from +989 format to 989 format)
	recipient := mobile
	if strings.HasPrefix(mobile, "+") {
		recipient = mobile[1:]
	}

	// Validate mobile format
	if len(recipient) != 12 || !strings.HasPrefix(recipient, "989") {
		return fmt.Errorf("invalid mobile number format: %s", mobile)
	}

	return s.smsService.SendSMS(ctx, recipient, message, customerID)
}

// SendSMSBulk sends a single message to multiple recipients
func (s *NotificationServiceImpl) SendSMSBulk(ctx context.Context, mobiles []string, message string, customerID *int64) error {
	if s.smsService == nil {
		return fmt.Errorf("SMS service not configured")
	}
	recipients := make([]string, 0, len(mobiles))
	for _, m := range mobiles {
		recipient := m
		if strings.HasPrefix(recipient, "+") {
			recipient = recipient[1:]
		}
		if len(recipient) == 12 && strings.HasPrefix(recipient, "989") {
			recipients = append(recipients, recipient)
		} else {
			log.Printf("invalid mobile format skipped in bulk: %s", m)
		}
	}
	if len(recipients) == 0 {
		return nil
	}
	return s.smsService.SendBulk(ctx, recipients, message, customerID)
}

// SendEmail sends an email to the specified email address
func (s *NotificationServiceImpl) SendEmail(email, subject, message string) error {
	if s.emailProvider == nil {
		return fmt.Errorf("email provider not configured")
	}

	// Improved email validation
	if err := validateEmail(email); err != nil {
		return fmt.Errorf("invalid email address: %w", err)
	}

	return s.emailProvider.SendEmail(email, subject, message)
}

type MockEmailProvider struct{ redact bool }

func NewMockEmailProvider(redact ...bool) EmailProvider {
	return &MockEmailProvider{redact: len(redact) > 0 && redact[0]}
}

func (p *MockEmailProvider) SendEmail(email, subject, message string) error {
	if p.redact {
		log.Print("mock provider=email action=send")
		return nil
	}
	log.Printf("Email sent to %s [%s]: %s", email, subject, message)
	return nil
}

type SMTPEmailProvider struct {
	host      string
	port      int
	username  string
	password  string
	fromEmail string
}

func NewSMTPEmailProvider(host string, port int, username, password, fromEmail string) EmailProvider {
	return &SMTPEmailProvider{
		host:      host,
		port:      port,
		username:  username,
		password:  password,
		fromEmail: fromEmail,
	}
}

func (p *SMTPEmailProvider) SendEmail(email, subject, message string) error {
	// Implementation would use net/smtp package or a library like gomail

	log.Printf("Sending email via SMTP to %s [%s]: %s", email, subject, message)

	// Placeholder implementation
	// In real implementation, configure SMTP and send email

	return nil
}

// validateEmail performs basic email validation
func validateEmail(email string) error {
	if len(email) == 0 {
		return fmt.Errorf("email cannot be empty")
	}

	if len(email) > 254 {
		return fmt.Errorf("email too long")
	}

	// Check for @ symbol
	atIndex := strings.Index(email, "@")
	if atIndex == -1 {
		return fmt.Errorf("missing @ symbol")
	}

	// Check local part (before @)
	localPart := email[:atIndex]
	if len(localPart) == 0 {
		return fmt.Errorf("local part cannot be empty")
	}
	if len(localPart) > 64 {
		return fmt.Errorf("local part too long")
	}

	// Check domain part (after @)
	domainPart := email[atIndex+1:]
	if len(domainPart) == 0 {
		return fmt.Errorf("domain part cannot be empty")
	}
	if len(domainPart) > 253 {
		return fmt.Errorf("domain part too long")
	}

	// Check for valid characters in local part
	for _, char := range localPart {
		if !isValidEmailChar(char) {
			return fmt.Errorf("invalid character in local part")
		}
	}

	// Check for valid characters in domain part
	for _, char := range domainPart {
		if !isValidDomainChar(char) {
			return fmt.Errorf("invalid character in domain part")
		}
	}

	// Check for at least one dot in domain
	if !strings.Contains(domainPart, ".") {
		return fmt.Errorf("domain must contain at least one dot")
	}

	return nil
}

// isValidEmailChar checks if a character is valid in email local part
func isValidEmailChar(char rune) bool {
	return (char >= 'a' && char <= 'z') ||
		(char >= 'A' && char <= 'Z') ||
		(char >= '0' && char <= '9') ||
		char == '.' || char == '!' || char == '#' || char == '$' ||
		char == '%' || char == '&' || char == '\'' || char == '*' ||
		char == '+' || char == '-' || char == '/' || char == '=' ||
		char == '?' || char == '^' || char == '_' || char == '`' ||
		char == '{' || char == '|' || char == '}' || char == '~'
}

// isValidDomainChar checks if a character is valid in domain part
func isValidDomainChar(char rune) bool {
	return (char >= 'a' && char <= 'z') ||
		(char >= 'A' && char <= 'Z') ||
		(char >= '0' && char <= '9') ||
		char == '.' || char == '-'
}

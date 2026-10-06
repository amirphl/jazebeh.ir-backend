package models

import "time"

// AsiaTechSMSMessage is the durable submission and DLR scheduling state for a
// sent SMS. The provider message ID is the only identifier accepted by getdlr.
type AsiaTechSMSMessage struct {
	ID                 uint      `gorm:"primaryKey"`
	SentSMSID          uint      `gorm:"not null;uniqueIndex"`
	ProviderMessageID  string    `gorm:"size:128;not null;uniqueIndex"`
	SourceAddress      string    `gorm:"size:64;not null"`
	DestinationAddress string    `gorm:"size:32;not null"`
	UDH                string    `gorm:"size:128;not null"`
	APIVersion         string    `gorm:"size:16;not null"`
	PartCount          int       `gorm:"not null;default:0"`
	UpstreamGateway    string    `gorm:"size:64"`
	OperatorGroup      string    `gorm:"size:16;not null;default:'other'"`
	SubmittedAt        time.Time `gorm:"not null;index"`
	NextPollAt         time.Time `gorm:"not null;index"`
	PollCount          int       `gorm:"not null;default:0"`
	LastPolledAt       *time.Time
	FinalizedAt        *time.Time `gorm:"index"`
	CreatedAt          time.Time  `gorm:"not null"`
	UpdatedAt          time.Time  `gorm:"not null"`
}

func (AsiaTechSMSMessage) TableName() string { return "asiatech_sms_messages" }

// AsiaTechDLRPoll stores immutable provider evidence; parts are child rows.
type AsiaTechDLRPoll struct {
	ID                   uint      `gorm:"primaryKey"`
	AsiaTechSMSMessageID uint      `gorm:"not null;index"`
	PolledAt             time.Time `gorm:"not null"`
	OverallStatusCode    int       `gorm:"not null"`
	OverallStatusText    string    `gorm:"size:64;not null"`
	IsFinal              bool      `gorm:"not null"`
	RawResponse          string    `gorm:"type:text;not null"`
}

func (AsiaTechDLRPoll) TableName() string { return "asiatech_sms_dlr_polls" }

type AsiaTechDLRPart struct {
	ID                 uint   `gorm:"primaryKey"`
	AsiaTechDLRPollID  uint   `gorm:"not null;index"`
	PartNumber         int    `gorm:"not null"`
	StatusCode         int    `gorm:"not null"`
	StatusText         string `gorm:"size:64;not null"`
	ProviderStatusAt   *time.Time
	ChargebackEligible bool `gorm:"not null"`
}

func (AsiaTechDLRPart) TableName() string { return "asiatech_sms_dlr_parts" }

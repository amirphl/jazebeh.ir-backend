package services

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMockOTPWritesPrivateOutbox(t *testing.T) {
	path := filepath.Join(t.TempDir(), "otp.jsonl")
	svc := NewMockOTPService(path)
	if err := svc.SendOTP(context.Background(), "989100000000", "code 123456", nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "123456") {
		t.Fatal("OTP not available to local operator")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("unsafe outbox permissions: %v %v", info, err)
	}
}

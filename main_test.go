package main

import "testing"

func TestRedactDatabaseLogMessage(t *testing.T) {
	input := "failed to connect to `user=app-user password=secret database=campaigns`: host=10.0.0.5 port=5432"
	got := redactDatabaseLogMessage(input)
	want := "failed to connect to `user=[redacted] password=[redacted] database=[redacted]`: host=[redacted] port=[redacted]"
	if got != want {
		t.Fatalf("redactDatabaseLogMessage() = %q, want %q", got, want)
	}
}

func TestRedactDatabaseLogMessageRedactsPostgresURL(t *testing.T) {
	input := "dial postgres://app-user:secret@db.example.test:5432/campaigns?sslmode=require failed"
	got := redactDatabaseLogMessage(input)
	if got != "dial [redacted database URL] failed" {
		t.Fatalf("redactDatabaseLogMessage() = %q", got)
	}
}

package utils

import (
	"encoding/json"
	"os"
	"testing"
)

func TestAutomatedShortLinkRequests(t *testing.T) {
	data, err := os.ReadFile("../external-shortlink/tests/fixtures/traffic.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name string
		UA   string
		IP   string
		Bot  bool
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			if got := IsAutomatedShortLinkRequest(tc.UA, tc.IP); got != tc.Bot {
				t.Fatalf("got %v, want %v", got, tc.Bot)
			}
		})
	}
}

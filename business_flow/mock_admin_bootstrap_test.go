package businessflow

import (
	"os/exec"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestMockAdminBootstrapPasswordHashCompatibleWithLogin(t *testing.T) {
	path, err := exec.LookPath("htpasswd")
	if err != nil {
		t.Skip("htpasswd is not installed")
	}
	cmd := exec.Command(path, "-niBC", "12", "bootstrap-test")
	cmd.Stdin = strings.NewReader("only-a-test-password\n")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("generate bcrypt hash: %v", err)
	}
	_, hash, ok := strings.Cut(strings.TrimSpace(string(out)), ":")
	if !ok {
		t.Fatal("unexpected htpasswd output")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte("only-a-test-password")); err != nil {
		t.Fatalf("admin login cannot verify generated hash: %v", err)
	}
}

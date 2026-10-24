package auth

import (
	"os"
	"testing"

	"github.com/emersion/hydroxide/config"
)

func TestHubBridgePassword(t *testing.T) {
	config.SetConfigHome(t.TempDir())
	store := `{"alice": {"email": "Alice@example.com", "bridge_password": "bridge-a"}}`
	p, err := config.Path("bridge-passwords.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(store), 0600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HYDROXIDE_HUB_PASSWORD", "")
	if got := hubBridgePassword("alice", "hub"); got != "" {
		t.Fatalf("unset env: got %q, want none", got)
	}

	t.Setenv("HYDROXIDE_HUB_PASSWORD", "hub")
	cases := map[[2]string]string{
		{"alice", "hub"}:             "bridge-a",
		{"ALICE", "hub"}:             "bridge-a",
		{"alice@example.com", "hub"}: "bridge-a",
		{"alice", "bridge-a"}:        "",
		{"alice", "wrong"}:           "",
		{"bob", "hub"}:               "",
	}
	for in, want := range cases {
		if got := hubBridgePassword(in[0], in[1]); got != want {
			t.Errorf("hubBridgePassword(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}

	m := NewManager(nil)
	if _, _, _, err := m.Auth("bob", "hub"); err != ErrUnauthorized {
		t.Errorf("unknown user with hub password: err = %v, want ErrUnauthorized", err)
	}
}

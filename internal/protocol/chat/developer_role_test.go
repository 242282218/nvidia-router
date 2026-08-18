package chat

import "testing"

// TestDeveloperRoleAccepted locks in support for the "developer" role. It is the
// standard replacement for "system" on OpenAI reasoning models, so rejecting it
// made those clients unusable through this router even though the role is part
// of the Chat Completions contract.
func TestDeveloperRoleAccepted(t *testing.T) {
	for _, role := range []string{"system", "user", "assistant", "tool", "developer"} {
		if _, ok := validRoles[role]; !ok {
			t.Fatalf("role %q is not accepted", role)
		}
	}
}

func TestUnknownRoleStillRejected(t *testing.T) {
	for _, role := range []string{"", "System", "root"} {
		if _, ok := validRoles[role]; ok {
			t.Fatalf("role %q should not be accepted", role)
		}
	}
}

func TestLegacyFunctionRoleAcceptedForCompatibility(t *testing.T) {
	if _, ok := validRoles["function"]; !ok {
		t.Fatal("legacy function role should be accepted and normalized")
	}
}

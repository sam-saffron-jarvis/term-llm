package tools

import "testing"

func TestFormatGuardianApprovalHumanReadable(t *testing.T) {
	t.Parallel()

	got := formatGuardianApproval(PolicyDecision{RiskLevel: "medium", UserAuthorization: "high"})
	want := "approved (medium risk; clearly user-authorized)"
	if got != want {
		t.Fatalf("formatGuardianApproval() = %q, want %q", got, want)
	}
}

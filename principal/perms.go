package principal

// Permission names, matching the role table in the web dashboard context
// (00 §5.1). Permissions are data in identity so roles can change without a
// deploy; these constants are only the names services check for.
const (
	BalancesView        = "balances.view"
	PayoutsInitiate     = "payouts.initiate"
	BeneficiariesManage = "beneficiaries.manage"
	FXConvert           = "fx.convert"
	ApprovalsDecide     = "approvals.decide"
	TeamView            = "team.view"
	TeamManage          = "team.manage"
	PolicyPropose       = "policy.propose"
	APIKeysManage       = "apikeys.manage"
	OnboardingManage    = "onboarding.manage"
	AuditView           = "audit.view"
)

// AllPerms lists every permission name, for validating role data.
func AllPerms() []string {
	return []string{
		BalancesView, PayoutsInitiate, BeneficiariesManage, FXConvert,
		ApprovalsDecide, TeamView, TeamManage, PolicyPropose, APIKeysManage,
		OnboardingManage, AuditView,
	}
}

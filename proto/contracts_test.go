package proto_test

// The service-to-service contracts the dashboard services build against in
// parallel. Later work may add RPCs and fields; it must never remove or rename
// one of these, so this test lists them and fails if one goes missing.

import (
	"sort"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/reflect/protoreflect"

	accountv1 "swiftbon.com/shared/proto/gen/go/account/v1"
	approvalsv1 "swiftbon.com/shared/proto/gen/go/approvals/v1"
	compliancev1 "swiftbon.com/shared/proto/gen/go/compliance/v1"
	documentsv1 "swiftbon.com/shared/proto/gen/go/documents/v1"
	fxv1 "swiftbon.com/shared/proto/gen/go/fx/v1"
	identityv1 "swiftbon.com/shared/proto/gen/go/identity/v1"
	kycv1 "swiftbon.com/shared/proto/gen/go/kyc/v1"
	postingsv1 "swiftbon.com/shared/proto/gen/go/postings/v1"
	railsv1 "swiftbon.com/shared/proto/gen/go/rails/v1"
	treasuryv1 "swiftbon.com/shared/proto/gen/go/treasury/v1"
)

func methods(desc grpc.ServiceDesc) []string {
	var out []string
	for _, m := range desc.Methods {
		out = append(out, m.MethodName)
	}
	sort.Strings(out)
	return out
}

func TestServiceContracts(t *testing.T) {
	tests := []struct {
		name string
		desc grpc.ServiceDesc
		want []string
	}{
		{"rails.v1.RailAdapter", railsv1.RailAdapter_ServiceDesc, []string{
			"Capabilities", "GetPayoutStatus", "GetProviderBalance", "GetSubAccount", "IssueReceivingDetails",
			"ListBanks", "ListPendingInbound", "ListProviderTransactions", "NameEnquiry", "OpenSubAccount",
			"RespondToRfi", "SubmitPayout",
		}},
		{"identity.v1.Identity", identityv1.Identity_ServiceDesc, []string{
			"AuthenticateApiKey", "ConsumeStepUp", "GetOrganisation", "GetUser", "ResolveRecipients",
			"ResolveSession", "SetOrganisationStatus",
		}},
		{"approvals.v1.Approvals", approvalsv1.Approvals_ServiceDesc, []string{
			"CancelRequest", "CreateRequest", "EvaluatePolicy", "GetRequest",
		}},
		{"kyc.v1.Kyc", kycv1.Kyc_ServiceDesc, []string{
			"CreateLivenessSession", "GetBusinessAddressVerification", "GetRelatedPartyVerification",
			"RegistryLookup", "ReleaseLivenessLock", "Screen", "VerifyBusinessAddress", "VerifyRelatedParty",
		}},
		{"documents.v1.Documents", documentsv1.Documents_ServiceDesc, []string{
			"GetCheckResult", "GetDocument", "RenderStatement",
		}},
		{"compliance.v1.Compliance", compliancev1.Compliance_ServiceDesc, []string{
			"RecordAudit", "ScreenCounterparty",
		}},
		{"treasury.v1.Treasury", treasuryv1.Treasury_ServiceDesc, []string{"GetRailStatus"}},
	}
	for _, tc := range tests {
		got := methods(tc.desc)
		if len(got) != len(tc.want) {
			t.Errorf("%s: methods = %v, want %v", tc.name, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: methods = %v, want %v", tc.name, got, tc.want)
				break
			}
		}
	}
}

// has checks that every wanted RPC exists on a service that also keeps its
// pre-existing RPCs: extended services must be supersets.
func TestExtendedServicesKeepAndGain(t *testing.T) {
	tests := []struct {
		name      string
		desc      grpc.ServiceDesc
		pre, gain []string
	}{
		{"postings.v1.Postings", postingsv1.Postings_ServiceDesc,
			[]string{"ProcessSingleTransfer", "PostTransaction", "ReverseTransaction", "GetTransactionByReference"},
			[]string{"PlaceHold", "ReleaseHold", "CaptureHold", "GetHold", "GetAvailableBalances", "GetStatement", "ListEntries", "SumCustomerLiabilities"}},
		{"account.v1.AccountMs", accountv1.AccountMs_ServiceDesc,
			[]string{"CreateCustomer", "UpdateCustomer", "SearchCustomer", "SearchCustomerByAccountNumber", "CheckCustomer", "CreateSystemAccount", "SearchSystemAccountByAccountNumber"},
			[]string{"CreateBusiness", "GetBusiness", "OpenBusinessAccount", "ListOrgAccounts", "GetCapabilities", "SetCapability", "IssueReceivingDetailsVersion"}},
		{"fx.v1.FxService", fxv1.FxService_ServiceDesc,
			[]string{"Quote", "GetQuote", "ConsumeQuote", "GetRates"},
			[]string{"Equivalent"}},
	}
	for _, tc := range tests {
		have := map[string]bool{}
		for _, m := range methods(tc.desc) {
			have[m] = true
		}
		for _, m := range append(append([]string{}, tc.pre...), tc.gain...) {
			if !have[m] {
				t.Errorf("%s lacks %s", tc.name, m)
			}
		}
	}
}

func TestPostingRequestHoldIsAdditive(t *testing.T) {
	fd := (&postingsv1.PostingRequest{}).ProtoReflect().Descriptor().Fields()
	hold := fd.ByName("hold")
	if hold == nil || hold.Number() != 13 || !hold.HasOptionalKeyword() {
		t.Fatalf("PostingRequest.hold = %v", hold)
	}
	// The pre-existing numbers are untouched.
	for name, n := range map[string]protoreflect.FieldNumber{"force_debit": 1, "reference_number": 4, "posting_records": 10, "metadata": 11, "fx_quote_id": 12} {
		f := fd.ByName(protoreflect.Name(name))
		if f == nil || f.Number() != n {
			t.Errorf("PostingRequest.%s changed: %v", name, f)
		}
	}
}

func TestRailsPayoutStates(t *testing.T) {
	// 00 §6.2 names exactly these seven outcomes.
	want := map[string]int32{
		"PAYOUT_STATE_ACCEPTED": 1, "PAYOUT_STATE_PROCESSING": 2, "PAYOUT_STATE_SETTLED": 3,
		"PAYOUT_STATE_RETURNED": 4, "PAYOUT_STATE_FAILED": 5, "PAYOUT_STATE_UNKNOWN": 6, "PAYOUT_STATE_NOT_FOUND": 7,
	}
	for name, n := range want {
		if got, ok := railsv1.PayoutState_value[name]; !ok || got != n {
			t.Errorf("%s = %d, %v; want %d", name, got, ok, n)
		}
	}
	if len(railsv1.PayoutState_value) != len(want)+1 { // plus UNSPECIFIED
		t.Errorf("PayoutState has %d values, want %d", len(railsv1.PayoutState_value), len(want)+1)
	}
	// An adapter's SubmitPayout answer carries safe_to_reroute, and GetPayoutStatus too.
	for _, m := range []protoreflect.MessageDescriptor{
		(&railsv1.SubmitPayoutResponse{}).ProtoReflect().Descriptor(),
		(&railsv1.PayoutStatus{}).ProtoReflect().Descriptor(),
	} {
		if m.Fields().ByName("safe_to_reroute") == nil {
			t.Errorf("%s lacks safe_to_reroute", m.FullName())
		}
	}
}

func TestTenantScopedRequestsCarryOrgID(t *testing.T) {
	// Every request message of a tenant-scoped RPC carries org_id. The few
	// platform-level requests are listed, with the reason, so adding one is a
	// decision rather than an accident.
	platformLevel := map[protoreflect.FullName]string{
		"rails.v1.CapabilitiesRequest":              "describes the adapter, not a tenant",
		"rails.v1.ListBanksRequest":                 "a bank list is shared",
		"rails.v1.GetProviderBalanceRequest":        "Swiftbon's own account at the provider",
		"rails.v1.ListProviderTransactionsRequest":  "reconciliation across all tenants",
		"postings.v1.SumCustomerLiabilitiesRequest": "total across all customers",
		"treasury.v1.GetRailStatusRequest":          "rail health is global",
		"identity.v1.ResolveSessionRequest":         "resolves a session id; the org comes back",
		"identity.v1.AuthenticateApiKeyRequest":     "authenticates a key; the org comes back",
		"identity.v1.GetUserRequest":                "org_id is optional there",
	}
	files := map[string]protoreflect.FileDescriptor{
		"rails.v1":      (&railsv1.CapabilitiesRequest{}).ProtoReflect().Descriptor().ParentFile(),
		"identity.v1":   (&identityv1.GetUserRequest{}).ProtoReflect().Descriptor().ParentFile(),
		"approvals.v1":  (&approvalsv1.GetRequestRequest{}).ProtoReflect().Descriptor().ParentFile(),
		"kyc.v1":        (&kycv1.ScreenRequest{}).ProtoReflect().Descriptor().ParentFile(),
		"documents.v1":  (&documentsv1.GetDocumentRequest{}).ProtoReflect().Descriptor().ParentFile(),
		"compliance.v1": (&compliancev1.RecordAuditRequest{}).ProtoReflect().Descriptor().ParentFile(),
		"treasury.v1":   (&treasuryv1.GetRailStatusRequest{}).ProtoReflect().Descriptor().ParentFile(),
	}
	for pkg, fd := range files {
		svcs := fd.Services()
		for i := 0; i < svcs.Len(); i++ {
			ms := svcs.Get(i).Methods()
			for j := 0; j < ms.Len(); j++ {
				in := ms.Get(j).Input()
				if _, exempt := platformLevel[in.FullName()]; exempt {
					continue
				}
				if in.Fields().ByName("org_id") == nil {
					t.Errorf("%s: %s.%s takes %s, which has no org_id", pkg, svcs.Get(i).Name(), ms.Get(j).Name(), in.Name())
				}
			}
		}
	}
}

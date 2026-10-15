package proto_test

// Wire compatibility with v0.0.1.
//
// Each case is a message built with the v0.0.1 generated code, serialised, and
// recorded here as hex. The current generated code must (1) decode those exact
// bytes into the same message and (2) serialise the same message back to the
// same bytes. Fields were only added since v0.0.1; if a number was reused,
// retyped or renumbered, this fails.
//
// To record a new case, build the message in a checkout of the release tag and
// print hex.EncodeToString of its deterministic marshalling.

import (
	"bytes"
	"encoding/hex"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	accountv1 "swiftbon.com/shared/proto/gen/go/account/v1"
	fxv1 "swiftbon.com/shared/proto/gen/go/fx/v1"
	moneyv1 "swiftbon.com/shared/proto/gen/go/money/v1"
	postingsv1 "swiftbon.com/shared/proto/gen/go/postings/v1"
)

func TestWireCompatibleWithV001(t *testing.T) {
	ts := timestamppb.New(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	quoteID := "q-123"
	cases := []struct {
		name  string
		msg   proto.Message
		blank proto.Message
		hex   string
	}{
		{"PostingRequest", &postingsv1.PostingRequest{
			ForceDebit: true, FinancialDate: ts, InstrumentNumber: "ins-1", ReferenceNumber: "ref-1",
			PostingType: 2, TransactionMethod: 3, Merchant: "m", TagId: 4, CategoryId: 5,
			PostingRecords: []*postingsv1.PostingRecord{
				{AccountNumber: "1001", AccountName: "A", Amount: &moneyv1.Money{Amount: 12345, Currency: "NGN"}, Narration: "n", EntryCode: "DR", BranchId: "b", PostingRecordType: 1, AccountCategory: 2},
				{AccountNumber: "2002", AccountName: "B", Amount: &moneyv1.Money{Amount: 12345, Currency: "NGN"}, Narration: "n", EntryCode: "CR", PostingRecordType: 2, AccountCategory: 3},
			},
			Metadata:  &postingsv1.PostingMetadata{SenderName: "s", SenderAccountNumber: "1001", SenderBank: "sb", RecipientName: "r", RecipientAccountNumber: "2002", RecipientBank: "rb", Fee: &moneyv1.Money{Amount: 50, Currency: "NGN"}},
			FxQuoteId: &quoteID,
		}, &postingsv1.PostingRequest{},
			"0801120608a5ebdcca061a05696e732d3122057265662d31280230033a016d4004480552210a043130303112014122016e2a02445232016238014002620808b96012034e474e521e0a043230303212014222016e2a02435238024003620808b96012034e474e5a230a01731204313030311a0273622201722a0432303032320272624207083212034e474e6205712d313233"},
		{"ProcessTransferRequest", &postingsv1.ProcessTransferRequest{
			RecipientAccountNumber: "2002", SenderAccountNumber: "1001", SenderAccountName: "A", EntryCode: "TR", Narration: "n",
			PostingType: 1, TransactionMethod: 2, InstrumentNumber: "i", Merchant: "m", TagId: 3, CategoryId: 4,
			Fee:    &postingsv1.TransactionFee{FeeAccount: "9001", Amount: &moneyv1.Money{Amount: 25, Currency: "USD"}},
			Amount: &moneyv1.Money{Amount: 1000, Currency: "USD"}, FxQuoteId: &quoteID,
		}, &postingsv1.ProcessTransferRequest{},
			"0a04323030321204313030311a01412202545232016e380140024a016952016d580360046a0f1204393030311a0708191203555344720808e80712035553447a05712d313233"},
		{"BaseResponse", &postingsv1.BaseResponse{ResponseCode: "00", ResponseDescription: "ok", ResponseMessage: "m"}, &postingsv1.BaseResponse{},
			"0a02303012026f6b1a016d"},
		{"ReversalRequest", &postingsv1.ReversalRequest{ReferenceNumber: "ref-1"}, &postingsv1.ReversalRequest{},
			"0a057265662d31"},
		{"CreateCustomerRequest", &accountv1.CreateCustomerRequest{PhoneNumber: "+2348012345678", FirstName: "Ada", LastName: "Obi", Email: "a@b.c", Gender: 2, PoliticallyExposed: true, IsTermsAgreed: true, Occupation: "trader"}, &accountv1.CreateCustomerRequest{},
			"0a0e2b32333438303132333435363738120341646122034f626932056140622e637802800101880101920106747261646572"},
		{"CreateSystemAccountRequest", &accountv1.CreateSystemAccountRequest{AccountName: "FX_ROUNDING_USD", CreatedBy: "sys", ApprovedBy: "sys", BranchId: "b", AccountType: 4, TransactionPermission: 1, AccountStatus: 1, InitialBalance: 99}, &accountv1.CreateSystemAccountRequest{},
			"0a0f46585f524f554e44494e475f55534412037379731a037379732201622804300138014063"},
		{"SearchCustomerRequest", &accountv1.SearchCustomerRequest{SearchParam: "+2348012345678"}, &accountv1.SearchCustomerRequest{},
			"0a0e2b32333438303132333435363738"},
		{"AccountBaseResponse", &accountv1.BaseResponse{ResponseCode: "00", ResponseDescription: "ok", ResponseMessage: "m"}, &accountv1.BaseResponse{},
			"0a02303012026f6b1a016d"},
		{"Quote", &fxv1.Quote{Id: "q-1", Sell: &moneyv1.Money{Amount: 100000, Currency: "USD"}, Buy: &moneyv1.Money{Amount: 15482000, Currency: "NGN"}, MidRate: "1550.0000000000", Rate: "1548.2000000000", SpreadBps: 12, RateSource: "src", SourceRateId: "sr-1", CreatedAt: ts, ExpiresAt: ts, Status: fxv1.QuoteStatus_QUOTE_STATUS_OPEN}, &fxv1.Quote{},
			"0a03712d31120908a08d0612035553441a0a0890f9b00712034e474e220f313535302e303030303030303030302a0f313534382e32303030303030303030300c3a03737263420473722d314a0608a5ebdcca06520608a5ebdcca065801"},
		{"QuoteRequest", &fxv1.QuoteRequest{Fixed: &fxv1.QuoteRequest_Sell{Sell: &moneyv1.Money{Amount: 5000, Currency: "USD"}}, CounterCurrency: "NGN", CustomerId: "c-1"}, &fxv1.QuoteRequest{},
			"1a034e474e2203632d310a080888271203555344"},
		{"ConsumeQuoteRequest", &fxv1.ConsumeQuoteRequest{QuoteId: "q-1", Reference: "ref-1"}, &fxv1.ConsumeQuoteRequest{},
			"0a03712d3112057265662d31"},
		{"Rate", &fxv1.Rate{SellCurrency: "USD", BuyCurrency: "NGN", Available: true, MidRate: "1550", Rate: "1548", SpreadBps: 12, RateSource: "s", ObservedAt: ts, FetchedAt: ts}, &fxv1.Rate{},
			"0a0355534412034e474e18012204313535302a0431353438300c3a0173420608a5ebdcca064a0608a5ebdcca06"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want, err := hex.DecodeString(tc.hex)
			if err != nil {
				t.Fatal(err)
			}
			if err := proto.Unmarshal(want, tc.blank); err != nil {
				t.Fatalf("v0.0.1 bytes no longer decode: %v", err)
			}
			if !proto.Equal(tc.blank, tc.msg) {
				t.Errorf("v0.0.1 bytes decode to a different message:\n got: %v\nwant: %v", tc.blank, tc.msg)
			}
			got, err := proto.MarshalOptions{Deterministic: true}.Marshal(tc.msg)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("message no longer serialises to the v0.0.1 bytes:\n got: %x\nwant: %x", got, want)
			}
		})
	}
}

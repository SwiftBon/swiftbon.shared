module swiftbon.com/shared

// 1.24.1 is the newest go directive in the estate (account, billers,
// communication, flights, kyc, paystack, postings). Repos on older directives
// — bani and chatbot on 1.22.2, juice and seahorse on 1.23.5 — must bump to at
// least this before they can consume the module. See the README.
go 1.24.1

require (
	github.com/shopspring/decimal v1.4.0
	google.golang.org/grpc v1.71.0
	google.golang.org/protobuf v1.36.6
)

require (
	golang.org/x/net v0.34.0 // indirect
	golang.org/x/sys v0.29.0 // indirect
	golang.org/x/text v0.21.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250115164207-1a7da9e5054f // indirect
)

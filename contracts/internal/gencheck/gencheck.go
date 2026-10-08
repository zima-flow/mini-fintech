package gencheck

import (
	commonv1 "github.com/zima-flow/go-mentor/mini-fintech/contracts/gen/bank/common/v1"

	_ "github.com/zima-flow/go-mentor/mini-fintech/contracts/gen/bank/account/v1"
	_ "github.com/zima-flow/go-mentor/mini-fintech/contracts/gen/bank/antifraud/v1"
	_ "github.com/zima-flow/go-mentor/mini-fintech/contracts/gen/bank/auth/v1"
	_ "github.com/zima-flow/go-mentor/mini-fintech/contracts/gen/bank/customer/v1"
	_ "github.com/zima-flow/go-mentor/mini-fintech/contracts/gen/bank/document/v1"
	_ "github.com/zima-flow/go-mentor/mini-fintech/contracts/gen/bank/events/v1"
	_ "github.com/zima-flow/go-mentor/mini-fintech/contracts/gen/bank/example/v1"
	_ "github.com/zima-flow/go-mentor/mini-fintech/contracts/gen/bank/kyc/v1"
	_ "github.com/zima-flow/go-mentor/mini-fintech/contracts/gen/bank/ledger/v1"
	_ "github.com/zima-flow/go-mentor/mini-fintech/contracts/gen/bank/notification/v1"
)

type Money = commonv1.Money

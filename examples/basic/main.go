package main

import (
	"context"
	"errors"

	"go.uber.org/zap"

	"github.com/your_github_user_or_org/zapalert"
	"github.com/your_github_user_or_org/zapalert/ctxmeta"
)

func main() {
	log, err := zapalert.New(
		zapalert.WithServiceName("kyc-service"),
		zapalert.WithStaticZapFields(zap.String("env", "local")),
	)
	if err != nil {
		panic(err)
	}
	defer log.Sync()

	ctx := context.Background()
	ctx = ctxmeta.WithRequestID(ctx, "req-001")
	ctx = ctxmeta.WithClientID(ctx, "client-42")
	ctx = ctxmeta.WithIP(ctx, "192.168.1.10")

	log.Info(ctx, "KYC.Submit", "request accepted", zap.String("stage", "validation"))
	log.Error(ctx, "KYC.Submit", errors.New("document missing"), "request failed", zap.String("doc_type", "passport"))
}

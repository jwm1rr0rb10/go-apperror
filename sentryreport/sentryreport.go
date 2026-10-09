// Package sentryreport adapts Sentry to apperror.Reporter.
//
//	h := apperror.NewErrorHandler("MY-SVC", apperror.WithReporter(sentryreport.Report))
package sentryreport

import (
	"context"

	"github.com/getsentry/sentry-go"
)

// Report sends err to the Sentry hub bound to ctx (e.g. by sentryhttp or a gRPC middleware),
// falling back to the current hub. Stack traces of internal *AppError are picked up automatically.
func Report(ctx context.Context, err error) {
	hub := sentry.GetHubFromContext(ctx)
	if hub == nil {
		hub = sentry.CurrentHub()
	}
	hub.CaptureException(err)
}

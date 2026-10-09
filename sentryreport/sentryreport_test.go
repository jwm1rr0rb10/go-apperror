package sentryreport

import (
	"context"
	"testing"

	"github.com/getsentry/sentry-go"
	"github.com/jwm1rr0rb10/go-apperror"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReportUsesHubFromContextWithStackTrace(t *testing.T) {
	var events []*sentry.Event
	client, err := sentry.NewClient(sentry.ClientOptions{
		BeforeSend: func(event *sentry.Event, _ *sentry.EventHint) *sentry.Event {
			events = append(events, event)
			return nil
		},
	})
	require.NoError(t, err)
	ctx := sentry.SetHubOnContext(context.Background(), sentry.NewHub(client, sentry.NewScope()))

	Report(ctx, apperror.NewInternalError("SVC"))

	require.Len(t, events, 1)
	require.NotEmpty(t, events[0].Exception)
	stack := events[0].Exception[len(events[0].Exception)-1].Stacktrace
	require.NotNil(t, stack, "stack trace of *AppError must be extracted")

	var functions []string
	for _, frame := range stack.Frames {
		functions = append(functions, frame.Function)
	}
	assert.Contains(t, functions, "TestReportUsesHubFromContextWithStackTrace")
}

func TestReportFallsBackToCurrentHub(t *testing.T) {
	assert.NotPanics(t, func() { Report(context.Background(), apperror.NewInternalError("SVC")) })
}

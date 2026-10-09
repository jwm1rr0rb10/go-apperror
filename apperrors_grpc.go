package apperror

import (
	"strconv"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/protoadapt"
	"google.golang.org/protobuf/types/known/durationpb"
)

// GRPCStatus implements the gRPC status interface with rich details.
func (e *AppError) GRPCStatus() *status.Status {
	st := status.New(e.Type.GRPCCode(), e.Message)

	details := []protoadapt.MessageV1{e.errorInfo()}
	if len(e.Violations) > 0 {
		details = append(details, e.fieldDetails())
	}
	if e.RetryAfter > 0 {
		details = append(details, &errdetails.RetryInfo{RetryDelay: durationpb.New(e.RetryAfter)})
	}

	withDetails, err := st.WithDetails(details...)
	if err != nil {
		return st
	}
	return withDetails
}

func (e *AppError) fieldDetails() *errdetails.BadRequest {
	br := &errdetails.BadRequest{
		FieldViolations: make([]*errdetails.BadRequest_FieldViolation, 0, len(e.Violations)),
	}
	for _, v := range e.Violations {
		br.FieldViolations = append(br.FieldViolations, &errdetails.BadRequest_FieldViolation{
			Field:       v.Field,
			Reason:      v.Reason,
			Description: v.Description,
		})
	}
	return br
}

func (e *AppError) errorInfo() *errdetails.ErrorInfo {
	metadata := make(map[string]string)
	if e.TraceID != "" {
		metadata["trace_id"] = e.TraceID
	}
	if e.Type.valid() {
		metadata["error_type"] = e.Type.String()
	}

	return &errdetails.ErrorInfo{
		Reason:   e.SystemCode + "-" + strconv.FormatUint(uint64(e.Code), 10),
		Domain:   e.Domain,
		Metadata: metadata,
	}
}

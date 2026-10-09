package apperror_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/jwm1rr0rb10/go-apperror"
)

var ErrUserNotFound = apperror.NewNotFoundError("USERS", apperror.WithCode(1), apperror.WithMessage("user not found"))

func findUser(ctx context.Context, id string) error {
	return ErrUserNotFound.WithTrace(ctx)
}

func Example() {
	h := apperror.NewErrorHandler("USERS")

	mux := http.NewServeMux()
	mux.Handle("GET /users/{id}", h.HTTP(func(w http.ResponseWriter, r *http.Request) error {
		if err := findUser(r.Context(), r.PathValue("id")); err != nil {
			return err
		}
		w.WriteHeader(http.StatusNoContent)
		return nil
	}))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/users/42", nil))
	fmt.Println(rec.Code, rec.Body.String())
	// Output: 404 {"message":"user not found","system_code":"USERS","type":"NotFound","code":1}
}

func ExampleNewInternalError() {
	dbErr := errors.New("pq: connection refused")
	err := apperror.NewInternalError("USERS", apperror.WithCode(2), apperror.WithErr(dbErr))

	fmt.Println(err.Message)
	fmt.Println(err.Error())
	fmt.Println(errors.Is(err, dbErr))
	// Output:
	// internal error
	// internal error: pq: connection refused (code: USERS-2)
	// true
}

func ExampleIsNotFound() {
	err := fmt.Errorf("load profile: %w", findUser(context.Background(), "42"))

	fmt.Println(apperror.IsNotFound(err), errors.Is(err, ErrUserNotFound))
	// Output: true true
}

func ExampleWithViolations() {
	err := apperror.NewValidationError("USERS",
		apperror.WithFields(apperror.ErrorFields{"email": "required"}),
		apperror.WithViolations(apperror.FieldViolation{Field: "password", Reason: "TOO_SHORT", Description: "min 8 chars"}),
	)

	fmt.Println(string(err.Marshal()))
	// Output: {"message":"validation failed","system_code":"USERS","type":"Validation","code":0,"violations":[{"field":"email","description":"required"},{"field":"password","reason":"TOO_SHORT","description":"min 8 chars"}]}
}

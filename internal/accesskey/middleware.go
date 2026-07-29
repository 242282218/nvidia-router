package accesskey

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"nvidia-router/internal/apierror"
)

type identityContextKey struct{}

func Middleware(service *Service, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		plaintext, ok := bearerToken(request)
		if !ok {
			writeInvalidAccessKey(writer)
			return
		}
		identity, err := service.Authenticate(request.Context(), plaintext)
		if errors.Is(err, ErrInvalidAccessKey) {
			writeInvalidAccessKey(writer)
			return
		}
		if err != nil {
			apierror.Error{
				Status:  http.StatusInternalServerError,
				Type:    "server_error",
				Code:    "internal_error",
				Message: "The server could not authenticate the API key.",
				Cause:   err,
			}.Write(writer)
			return
		}

		ctx := context.WithValue(request.Context(), identityContextKey{}, identity)
		next.ServeHTTP(writer, request.WithContext(ctx))
		service.RecordUse(ctx, identity.ID)
	})
}

func IdentityFromContext(ctx context.Context) (AccessKeyIdentity, bool) {
	identity, ok := ctx.Value(identityContextKey{}).(AccessKeyIdentity)
	return identity, ok
}

func bearerToken(request *http.Request) (string, bool) {
	values := request.Header.Values("Authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
		return "", false
	}
	token := strings.TrimPrefix(values[0], "Bearer ")
	return token, token != "" && !strings.ContainsAny(token, " \t\r\n")
}

func writeInvalidAccessKey(writer http.ResponseWriter) {
	apierror.Error{
		Status:  http.StatusUnauthorized,
		Type:    "invalid_request_error",
		Code:    "invalid_api_key",
		Message: "Invalid API key.",
	}.Write(writer)
}

package mcpserver

import (
	"bytes"
	"crypto/subtle"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const EndpointPath = "/mcp"

func NewHTTPHandler(server *mcp.Server, logger *slog.Logger) http.Handler {
	return newHTTPHandler(server, logger, func(handler http.Handler) http.Handler { return handler })
}

func NewAuthenticatedHTTPHandler(server *mcp.Server, bearerToken string, logger *slog.Logger) http.Handler {
	return newHTTPHandler(server, logger, func(handler http.Handler) http.Handler {
		return requireBearerToken(bearerToken, handler)
	})
}

func newHTTPHandler(
	server *mcp.Server,
	logger *slog.Logger,
	middleware func(http.Handler) http.Handler,
) http.Handler {
	streamableHandler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{
			Stateless:    true,
			JSONResponse: true,
			Logger:       logger,
		},
	)

	mux := http.NewServeMux()
	mux.Handle(EndpointPath, middleware(http.NewCrossOriginProtection().Handler(requireUTF8JSON(streamableHandler))))
	return mux
}

// Validate raw JSON bytes before the SDK's decoder can silently replace invalid
// UTF-8 in a vocabulary term or metadata. Keep the SDK's existing size limit.
func requireUTF8JSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.Body != nil {
			body := http.MaxBytesReader(w, r.Body, mcp.DefaultMaxRequestBodyBytes)
			defer body.Close()
			content, err := io.ReadAll(body)
			if err != nil {
				var tooLarge *http.MaxBytesError
				if errors.As(err, &tooLarge) {
					http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
				} else {
					http.Error(w, "Unable to read request body", http.StatusBadRequest)
				}
				return
			}
			if !utf8.Valid(content) {
				http.Error(w, "Request must contain valid UTF-8 JSON", http.StatusBadRequest)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(content))
		}
		next.ServeHTTP(w, r)
	})
}

func requireBearerToken(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		scheme, suppliedToken, found := strings.Cut(request.Header.Get("Authorization"), " ")
		tokenMatches := subtle.ConstantTimeCompare([]byte(suppliedToken), []byte(token)) == 1
		if token == "" || !found || !strings.EqualFold(scheme, "Bearer") || !tokenMatches {
			response.Header().Set("Cache-Control", "no-store")
			response.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(response, "Unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(response, request)
	})
}

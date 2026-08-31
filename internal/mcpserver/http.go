package mcpserver

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strings"

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
	mux.Handle(EndpointPath, middleware(streamableHandler))
	return mux
}

func requireBearerToken(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		scheme, suppliedToken, found := strings.Cut(request.Header.Get("Authorization"), " ")
		tokenMatches := subtle.ConstantTimeCompare([]byte(suppliedToken), []byte(token)) == 1
		if !found || !strings.EqualFold(scheme, "Bearer") || !tokenMatches {
			response.Header().Set("Cache-Control", "no-store")
			response.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(response, "Unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(response, request)
	})
}

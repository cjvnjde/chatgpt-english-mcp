package ankiexport

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"english-learning-mcp/internal/storage"
)

const EndpointPath = "/internal/anki/snapshot"

func NewHTTPHandler(store *storage.DB, owner, namespace, token string, logger *slog.Logger) http.Handler {
	expected := sha256.Sum256([]byte(token))
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		headers := request.Header.Values("Authorization")
		scheme, supplied, found := strings.Cut(request.Header.Get("Authorization"), " ")
		actual := sha256.Sum256([]byte(supplied))
		matches := subtle.ConstantTimeCompare(actual[:], expected[:]) == 1
		if len(token) < 32 || len(headers) != 1 || !found || !strings.EqualFold(scheme, "Bearer") || !matches {
			response.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(response, "Unauthorized", http.StatusUnauthorized)
			return
		}
		if request.URL.Path != EndpointPath {
			http.NotFound(response, request)
			return
		}
		if request.Method != http.MethodGet {
			response.Header().Set("Allow", http.MethodGet)
			http.Error(response, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}

		snapshot, err := store.ExportVocabulary(request.Context(), owner, namespace)
		if err != nil {
			logger.Error("Anki vocabulary snapshot unavailable")
			http.Error(response, "Snapshot unavailable", http.StatusServiceUnavailable)
			return
		}
		encoded, err := json.Marshal(snapshot)
		if err != nil {
			logger.Error("Anki vocabulary snapshot encoding failed")
			http.Error(response, "Snapshot unavailable", http.StatusServiceUnavailable)
			return
		}

		response.Header().Set("Content-Type", "application/json")
		if _, err := response.Write(encoded); err != nil {
			logger.Warn("Anki vocabulary snapshot response interrupted")
		}
	})
}

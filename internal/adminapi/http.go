package adminapi

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"english-learning-mcp/internal/apperr"
	"english-learning-mcp/internal/mcpserver"
	"english-learning-mcp/internal/storage"
	"english-learning-mcp/internal/vocabulary"
)

const EndpointPath = "/admin/api/"

// NewHandler is mounted only on the external listener. An empty token disables it.
func NewHandler(store *storage.DB, service *vocabulary.Service, owner, token string, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	exportSlot := make(chan struct{}, 1)
	mux.HandleFunc("GET /admin/api/database", func(w http.ResponseWriter, r *http.Request) {
		select {
		case exportSlot <- struct{}{}:
			defer func() { <-exportSlot }()
		default:
			writeJSON(w, http.StatusConflict, map[string]string{"error": "A database export is already running. Try again shortly."})
			return
		}
		file, cleanup, err := store.AdminBackup(r.Context())
		if err != nil {
			writeError(w, err, logger)
			return
		}
		defer cleanup()
		info, err := file.Stat()
		if err != nil {
			writeError(w, err, logger)
			return
		}
		filename := "english-mcp-" + time.Now().UTC().Format("2006-01-02-150405") + ".sqlite"
		w.Header().Set("Content-Type", "application/vnd.sqlite3")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
		w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
		if _, err := io.Copy(w, file); err != nil {
			logger.Warn("Database export download interrupted")
		}
	})
	handle := func(pattern string, fn func(http.ResponseWriter, *http.Request) (any, error)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			data, err := fn(w, r)
			if err != nil {
				writeError(w, err, logger)
				return
			}
			writeJSON(w, http.StatusOK, data)
		})
	}
	handle("GET /admin/api/session", func(w http.ResponseWriter, r *http.Request) (any, error) {
		return map[string]any{"owner": owner, "version": 1}, nil
	})
	handle("GET /admin/api/tables", func(w http.ResponseWriter, r *http.Request) (any, error) { return store.AdminTables(r.Context()) })
	handle("GET /admin/api/tables/{table}", func(w http.ResponseWriter, r *http.Request) (any, error) {
		q := r.URL.Query()
		limit, offset := 50, 0
		var err error
		if q.Has("limit") {
			limit, err = strconv.Atoi(q.Get("limit"))
			if err != nil {
				return nil, storage.ErrAdminQuery
			}
		}
		if q.Has("offset") {
			offset, err = strconv.Atoi(q.Get("offset"))
			if err != nil {
				return nil, storage.ErrAdminQuery
			}
		}
		return store.AdminRows(r.Context(), r.PathValue("table"), storage.AdminQuery{
			Query: q.Get("q"), Column: q.Get("column"), Value: q.Get("value"), Sort: q.Get("sort"), Direction: q.Get("direction"),
			From: q.Get("from"), To: q.Get("to"), CommentsOnly: q.Get("comments") == "true", Limit: limit, Offset: offset,
		})
	})
	handle("GET /admin/api/analytics", func(w http.ResponseWriter, r *http.Request) (any, error) {
		return store.AdminAnalytics(r.Context(), owner)
	})
	handle("GET /admin/api/vocabulary/{id}", func(w http.ResponseWriter, r *http.Request) (any, error) {
		return service.Get(r.Context(), r.PathValue("id"), "")
	})
	handle("POST /admin/api/vocabulary", func(w http.ResponseWriter, r *http.Request) (any, error) {
		var input mcpserver.VocabularySaveInput
		if err := decode(w, r, &input); err != nil {
			return nil, err
		}
		return service.Save(r.Context(), input.Term, vocabulary.InitialValues{
			Status: input.Status, Usefulness: input.Usefulness, Tags: input.Tags, CustomDescription: input.CustomDescription,
			DescriptionSource: input.DescriptionSource, Notes: input.Notes, Examples: input.Examples, Context: input.Context, Definition: input.Definition,
		})
	})
	handle("PATCH /admin/api/vocabulary/{id}", func(w http.ResponseWriter, r *http.Request) (any, error) {
		var changes mcpserver.VocabularyUpdateChanges
		if err := decode(w, r, &changes); err != nil {
			return nil, err
		}
		return service.Update(r.Context(), r.PathValue("id"), "", vocabulary.UpdateChanges{
			Status: changes.Status, Usefulness: changes.Usefulness, Tags: changes.Tags, CustomDescription: changes.CustomDescription,
			DescriptionSource: changes.DescriptionSource, Notes: changes.Notes, Examples: changes.Examples,
		})
	})
	handle("DELETE /admin/api/vocabulary/{id}", func(w http.ResponseWriter, r *http.Request) (any, error) {
		err := service.Delete(r.Context(), r.PathValue("id"))
		return map[string]bool{"deleted": err == nil}, err
	})
	expected := sha256.Sum256([]byte(token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if token == "" {
			http.NotFound(w, r)
			return
		}
		scheme, supplied, found := strings.Cut(r.Header.Get("Authorization"), " ")
		actual := sha256.Sum256([]byte(supplied))
		if !found || !strings.EqualFold(scheme, "Bearer") || subtle.ConstantTimeCompare(expected[:], actual[:]) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid admin token"})
			return
		}
		timeout := 15 * time.Second
		if r.URL.Path == "/admin/api/database" {
			timeout = 90 * time.Second
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		mux.ServeHTTP(w, r.WithContext(ctx))
	})
}

func decode(w http.ResponseWriter, r *http.Request, target any) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return apperr.New(apperr.InvalidArgument, "Content-Type must be application/json")
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return apperr.New(apperr.InvalidArgument, "Request must be a valid JSON object with supported fields (maximum 1 MiB)")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return apperr.New(apperr.InvalidArgument, "Request must contain one JSON object")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, err error, logger *slog.Logger) {
	status, message := http.StatusInternalServerError, "Request failed"
	appErr := apperr.From(err)
	switch {
	case errors.Is(err, storage.ErrNotFound), appErr.Code == apperr.NotFound:
		status, message = http.StatusNotFound, "Record not found"
	case errors.Is(err, storage.ErrAdminQuery):
		status, message = http.StatusBadRequest, "Invalid filter, sort column, date, or page size"
	case appErr.Code == apperr.InvalidArgument:
		status, message = http.StatusBadRequest, appErr.Message
	default:
		logger.Error("Admin request failed", "error", err)
	}
	writeJSON(w, status, map[string]string{"error": message})
}

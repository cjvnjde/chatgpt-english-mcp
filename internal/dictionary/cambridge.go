package dictionary

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"english-learning-mcp/internal/domain"
)

const (
	cambridgeParserVersion    = 12
	maxCambridgeResponseBytes = 8 << 20
	maxCambridgeDefinitions   = 20
)

type CambridgeProvider struct {
	baseURL *url.URL
	client  *http.Client
	logger  *slog.Logger
}

func NewCambridgeProvider(baseURL *url.URL, timeout time.Duration, logger *slog.Logger) *CambridgeProvider {
	if logger == nil {
		logger = slog.Default()
	}
	return &CambridgeProvider{
		baseURL: baseURL,
		client: &http.Client{
			Timeout: timeout,
		},
		logger: logger,
	}
}

func (provider *CambridgeProvider) Name() string {
	return "cambridge"
}

func (provider *CambridgeProvider) ParserVersion() int {
	return cambridgeParserVersion
}

func (provider *CambridgeProvider) DatasetVersion() string {
	return ""
}

func (provider *CambridgeProvider) Lookup(ctx context.Context, term string) (domain.DictionarySnapshotData, error) {
	dictionaryURL := provider.dictionaryURL(term)
	html, status, sourceURL, err := provider.fetch(ctx, dictionaryURL)
	if err != nil {
		return domain.DictionarySnapshotData{}, err
	}
	if !cacheableCambridgeStatus(status) {
		return domain.DictionarySnapshotData{}, fmt.Errorf("Cambridge returned HTTP status %d", status)
	}

	data, err := parseCambridgeHTML(html, term, sourceURL, status, provider.baseURL, maxCambridgeDefinitions)
	if err != nil {
		return domain.DictionarySnapshotData{}, fmt.Errorf("parse Cambridge response: %w", err)
	}
	if len(data.Entries) > 0 || len(data.Suggestions) > 0 {
		return data, nil
	}

	spellcheckHTML, spellcheckStatus, _, spellcheckErr := provider.fetch(ctx, provider.spellcheckURL(term))
	if spellcheckErr != nil {
		provider.logger.Warn("Cambridge spellcheck request failed")
		return data, nil
	}
	if !cacheableCambridgeStatus(spellcheckStatus) {
		provider.logger.Warn("Cambridge spellcheck returned an unusable response", "status", spellcheckStatus)
		return data, nil
	}
	data.Suggestions = parseCambridgeSuggestions(spellcheckHTML, provider.baseURL)
	return data, nil
}

func (provider *CambridgeProvider) dictionaryURL(term string) *url.URL {
	slug := strings.ReplaceAll(term, " ", "-")
	target := *provider.baseURL
	pathPrefix := strings.TrimRight(target.Path, "/")
	rawPathPrefix := strings.TrimRight(target.EscapedPath(), "/")
	target.Path = pathPrefix + "/dictionary/english/" + slug
	target.RawPath = rawPathPrefix + "/dictionary/english/" + url.PathEscape(slug)
	return &target
}

func (provider *CambridgeProvider) spellcheckURL(term string) *url.URL {
	reference := &url.URL{Path: "/spellcheck/english/", RawQuery: url.Values{"q": []string{term}}.Encode()}
	return provider.baseURL.ResolveReference(reference)
}

func (provider *CambridgeProvider) fetch(ctx context.Context, target *url.URL) (body string, status int, sourceURL string, err error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return "", 0, "", fmt.Errorf("create Cambridge request: %w", err)
	}
	request.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36")
	request.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	request.Header.Set("Accept-Language", "en-US,en;q=0.9")
	request.Header.Set("Cache-Control", "no-cache")
	request.Header.Set("Referer", provider.baseURL.String()+"/")

	response, err := provider.client.Do(request)
	if err != nil {
		return "", 0, "", fmt.Errorf("request Cambridge: %w", err)
	}
	defer response.Body.Close()

	limited := io.LimitReader(response.Body, maxCambridgeResponseBytes+1)
	contents, err := io.ReadAll(limited)
	if err != nil {
		return "", response.StatusCode, response.Request.URL.String(), fmt.Errorf("read Cambridge response: %w", err)
	}
	if len(contents) > maxCambridgeResponseBytes {
		return "", response.StatusCode, response.Request.URL.String(), fmt.Errorf("Cambridge response exceeds %d bytes", maxCambridgeResponseBytes)
	}
	return string(contents), response.StatusCode, response.Request.URL.String(), nil
}

func cacheableCambridgeStatus(status int) bool {
	return status >= 200 && status < 400 || status == http.StatusNotFound
}

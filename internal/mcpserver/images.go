package mcpserver

import (
	"context"
	"sort"
	"strings"

	"english-learning-mcp/internal/storage"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	maxEmbeddedImages     = 4
	maxEmbeddedImageBytes = 8 << 20 // decoded bytes; base64 adds about one third
	maxImageReadAttempts  = 16
)

// MediaReader reads local objects only. Image delivery never fetches upstream URLs.
type MediaReader interface {
	MediaByID(context.Context, string) (storage.MediaObject, error)
}

type imageMediaReaderKey struct{}

// embedDictionaryImages operates on the serialized response, not stored metadata.
// Original URLs are removed even when a local copy is unavailable. Media IDs,
// captions and credits remain in the JSON; image blocks carry the corresponding ID.
func embedDictionaryImages(ctx context.Context, value any) ([]mcp.Content, bool) {
	reader, _ := ctx.Value(imageMediaReaderKey{}).(MediaReader)
	var content []mcp.Content
	attempted := make(map[string]bool)
	included := make(map[string]bool)
	totalBytes := 0
	changed := false
	add := func(ids ...string) {
		for _, id := range ids {
			if id == "" {
				continue
			}
			if included[id] {
				return
			}
			if reader == nil || attempted[id] || len(attempted) >= maxImageReadAttempts || len(content) >= maxEmbeddedImages {
				continue
			}
			attempted[id] = true
			media, err := reader.MediaByID(ctx, id)
			if err != nil || !strings.HasPrefix(media.ContentType, "image/") || len(media.Data) == 0 || len(media.Data) > maxEmbeddedImageBytes-totalBytes {
				continue
			}
			totalBytes += len(media.Data)
			included[id] = true
			content = append(content, &mcp.ImageContent{Data: media.Data, MIMEType: media.ContentType, Meta: mcp.Meta{"mediaId": id}})
			return
		}
	}
	var walk func(any)
	walk = func(value any) {
		switch value := value.(type) {
		case []any:
			for _, child := range value {
				walk(child)
			}
		case map[string]any:
			// Stable traversal makes attachment selection predictable when capped.
			keys := make([]string, 0, len(value))
			for key := range value {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				if key == "images" {
					if images, ok := value[key].([]any); ok {
						for _, item := range images {
							image, ok := item.(map[string]any)
							if !ok {
								continue
							}
							_, hasImageURL := image["imageUrl"]
							_, hasThumbnailURL := image["thumbnailUrl"]
							if !hasImageURL && !hasThumbnailURL {
								continue
							}
							delete(image, "imageUrl")
							delete(image, "thumbnailUrl")
							changed = true
							full, _ := image["mediaId"].(string)
							thumb, _ := image["thumbnailMediaId"].(string)
							add(full, thumb)
						}
					}
				}
				walk(value[key])
			}
		}
	}
	walk(value)
	return content, changed
}

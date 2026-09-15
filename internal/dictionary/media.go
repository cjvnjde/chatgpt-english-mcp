package dictionary

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"

	"english-learning-mcp/internal/domain"
	"english-learning-mcp/internal/storage"
)

const (
	maxCambridgeAudioBytes = 5 << 20
	maxCambridgeImageBytes = 10 << 20
	maxCambridgeMediaFiles = 64
	maxCambridgeMediaBytes = 64 << 20
	maxCambridgeMediaTime  = 15 * time.Second
	mediaDownloadWorkers   = 4
)

type downloadedMedia struct {
	contentType string
	data        []byte
	sourceURL   string
}

type mediaDownloader interface {
	downloadMedia(ctx context.Context, sourceURL, kind string) (downloadedMedia, error)
}

type mediaStore interface {
	StoreMedia(ctx context.Context, input storage.MediaInsert) (storage.MediaObject, error)
}

type mediaBackfiller interface {
	BackfillDictionaryMedia(
		ctx context.Context,
		provider string,
		normalizedTerm string,
		links []storage.DictionaryMediaLink,
	) error
}

type mediaTarget struct {
	audio     *domain.DictionaryAudio
	image     *domain.DictionaryImage
	thumbnail bool
}

type mediaDownload struct {
	sourceURL   string
	kind        string
	targets     []mediaTarget
	mediaID     string
	contentType string
	err         error
}

func (provider *CambridgeProvider) checkRedirect(request *http.Request, previous []*http.Request) error {
	if len(previous) >= 10 {
		return fmt.Errorf("Cambridge redirect limit exceeded")
	}
	if !provider.sameOrigin(request.URL) {
		return fmt.Errorf("Cambridge redirected outside its configured origin")
	}
	return nil
}

func (provider *CambridgeProvider) sameOrigin(target *url.URL) bool {
	return target != nil && target.User == nil &&
		strings.EqualFold(target.Scheme, provider.baseURL.Scheme) &&
		strings.EqualFold(target.Host, provider.baseURL.Host)
}

func (provider *CambridgeProvider) downloadMedia(ctx context.Context, sourceURL, kind string) (downloadedMedia, error) {
	target, err := url.Parse(sourceURL)
	if err != nil || !provider.sameOrigin(target) {
		return downloadedMedia{}, fmt.Errorf("Cambridge media URL is outside its configured origin")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return downloadedMedia{}, fmt.Errorf("create Cambridge media request: %w", err)
	}
	request.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36")
	request.Header.Set("Accept", kind+"/*")
	request.Header.Set("Referer", provider.baseURL.String()+"/")
	response, err := provider.client.Do(request)
	if err != nil {
		return downloadedMedia{}, fmt.Errorf("request Cambridge media: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return downloadedMedia{}, fmt.Errorf("Cambridge media returned HTTP status %d", response.StatusCode)
	}
	maximum := int64(maxCambridgeImageBytes)
	if kind == storage.MediaKindAudio {
		maximum = maxCambridgeAudioBytes
	}
	if response.ContentLength > maximum {
		return downloadedMedia{}, fmt.Errorf("Cambridge media exceeds %d bytes", maximum)
	}
	contents, err := io.ReadAll(io.LimitReader(response.Body, maximum+1))
	if err != nil {
		return downloadedMedia{}, fmt.Errorf("read Cambridge media: %w", err)
	}
	if len(contents) == 0 || int64(len(contents)) > maximum {
		return downloadedMedia{}, fmt.Errorf("Cambridge media must contain between 1 and %d bytes", maximum)
	}
	contentType, err := storage.ValidatedMediaContentType(response.Header.Get("Content-Type"), contents, kind)
	if err != nil {
		return downloadedMedia{}, err
	}
	return downloadedMedia{contentType: contentType, data: contents, sourceURL: response.Request.URL.String()}, nil
}

func (service *Service) storeProviderMedia(ctx context.Context, normalizedTerm string, data *domain.DictionarySnapshotData, now time.Time) {
	downloader, downloadable := service.provider.(mediaDownloader)
	store, writable := service.store.(mediaStore)
	if !downloadable || !writable {
		return
	}
	downloads := make([]mediaDownload, 0)
	byResource := make(map[string]int)
	skipped := 0
	add := func(sourceURL, kind string, target mediaTarget) {
		if sourceURL == "" {
			return
		}
		key := kind + "\x00" + sourceURL
		if index, exists := byResource[key]; exists {
			downloads[index].targets = append(downloads[index].targets, target)
			return
		}
		if len(downloads) >= maxCambridgeMediaFiles {
			skipped++
			return
		}
		byResource[key] = len(downloads)
		downloads = append(downloads, mediaDownload{sourceURL: sourceURL, kind: kind, targets: []mediaTarget{target}})
	}
	for entryIndex := range data.Entries {
		entry := &data.Entries[entryIndex]
		if entry.Audio != nil {
			if entry.Audio.UK != nil {
				add(entry.Audio.UK.AudioURL, storage.MediaKindAudio, mediaTarget{audio: entry.Audio.UK})
			}
			if entry.Audio.US != nil {
				add(entry.Audio.US.AudioURL, storage.MediaKindAudio, mediaTarget{audio: entry.Audio.US})
			}
		}
		for definitionIndex := range entry.Definitions {
			images := entry.Definitions[definitionIndex].Images
			for imageIndex := range images {
				image := &images[imageIndex]
				add(image.ImageURL, storage.MediaKindImage, mediaTarget{image: image})
				add(image.ThumbnailURL, storage.MediaKindImage, mediaTarget{image: image, thumbnail: true})
			}
		}
	}
	for imageIndex := range data.Images {
		image := &data.Images[imageIndex]
		add(image.ImageURL, storage.MediaKindImage, mediaTarget{image: image})
		add(image.ThumbnailURL, storage.MediaKindImage, mediaTarget{image: image, thumbnail: true})
	}

	downloadContext, cancel := context.WithTimeout(ctx, maxCambridgeMediaTime)
	defer cancel()
	var storedBytes atomic.Int64
	var group errgroup.Group
	group.SetLimit(mediaDownloadWorkers)
	for index := range downloads {
		item := &downloads[index]
		group.Go(func() error {
			fetched, err := downloader.downloadMedia(downloadContext, item.sourceURL, item.kind)
			if err != nil {
				item.err = err
				return nil
			}
			size := int64(len(fetched.data))
			if storedBytes.Add(size) > maxCambridgeMediaBytes {
				storedBytes.Add(-size)
				item.err = fmt.Errorf("Cambridge media exceeds the aggregate storage limit")
				return nil
			}
			stored, err := store.StoreMedia(downloadContext, storage.MediaInsert{
				ContentType: fetched.contentType,
				Data:        fetched.data,
				SourceURL:   fetched.sourceURL,
				Now:         now,
			})
			if err != nil {
				storedBytes.Add(-size)
				item.err = err
				return nil
			}
			item.mediaID = stored.ID
			item.contentType = stored.ContentType
			return nil
		})
	}
	_ = group.Wait()
	failed := 0
	links := make([]storage.DictionaryMediaLink, 0, len(downloads))
	for index := range downloads {
		item := &downloads[index]
		if item.err != nil {
			failed++
			continue
		}
		links = append(links, storage.DictionaryMediaLink{
			SourceURL: item.sourceURL, Kind: item.kind, MediaID: item.mediaID, ContentType: item.contentType,
		})
		for _, target := range item.targets {
			if target.audio != nil {
				target.audio.MediaID = item.mediaID
				target.audio.ContentType = item.contentType
			} else if target.thumbnail {
				target.image.ThumbnailMediaID = item.mediaID
			} else {
				target.image.MediaID = item.mediaID
			}
		}
	}
	if backfiller, ok := service.store.(mediaBackfiller); ok && len(links) > 0 {
		if err := backfiller.BackfillDictionaryMedia(
			ctx,
			service.provider.Name(),
			normalizedTerm,
			links,
		); err != nil {
			service.logger.Warn("Older Cambridge snapshots could not be linked to stored media")
		}
	}
	if failed > 0 || skipped > 0 {
		service.logger.Warn("Some Cambridge media could not be stored", "failed", failed, "skipped", skipped, "attempted", len(downloads))
	}
}

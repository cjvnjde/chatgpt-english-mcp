package dictionary

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"english-learning-mcp/internal/domain"
	"english-learning-mcp/internal/storage"
)

func TestCambridgeLookupStoresAudioOnlyMP4(t *testing.T) {
	data, err := os.ReadFile("../storage/testdata/audio-only.mp4")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/audio.mp4" {
			// Generic MP4 servers and Go's sniffer can label audio as video/mp4.
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write(data)
			return
		}
		_, _ = io.WriteString(w, `<div class="entry-body__el"><span class="hw dhw">bank</span><div class="uk"><audio><source type="audio/mp4" src="/audio.mp4"></audio></div><div class="def-block ddef_block"><div class="def ddef_d">land beside a river</div></div></div>`)
	}))
	defer server.Close()
	ctx := context.Background()
	store, err := storage.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	base, _ := url.Parse(server.URL)
	service := NewService(store, NewCambridgeProvider(base, time.Second, nil), nil)
	result, err := service.Lookup(ctx, "bank", false)
	if err != nil {
		t.Fatal(err)
	}
	audio := result.Entries[0].Audio.UK
	stored, err := store.MediaByID(ctx, audio.MediaID)
	if err != nil || audio.ContentType != "audio/mp4" || stored.ContentType != "audio/mp4" || !bytes.Equal(stored.Data, data) {
		t.Fatalf("audio-only MP4 not stored: %#v %v", audio, err)
	}
}

func TestRefreshPreservesOnlyMatchingStoredMedia(t *testing.T) {
	for _, changedURL := range []bool{false, true} {
		t.Run(fmt.Sprintf("changedURL=%t", changedURL), func(t *testing.T) {
			var failMedia atomic.Bool
			png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/dictionary/english/bank" {
					prefix := ""
					if changedURL && failMedia.Load() {
						prefix = "new-"
					}
					_, _ = fmt.Fprintf(w, `<div class="entry-body__el"><span class="hw dhw">bank</span><div class="uk"><audio><source type="audio/mpeg" src="/%[1]saudio.mp3"></audio></div><div class="def-block ddef_block"><div class="def ddef_d">land beside a river</div><div class="dimg"><img src="/%[1]simage.png"></div></div></div>`, prefix)
					return
				}
				if failMedia.Load() {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				if r.URL.Path == "/audio.mp3" {
					w.Header().Set("Content-Type", "audio/mpeg")
					_, _ = io.WriteString(w, "ID3\x04\x00\x00\x00\x00\x00\x00")
				} else {
					w.Header().Set("Content-Type", "image/png")
					_, _ = w.Write(png)
				}
			}))
			defer server.Close()
			ctx := context.Background()
			store, err := storage.Open(ctx, ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			base, _ := url.Parse(server.URL)
			service := NewService(store, NewCambridgeProvider(base, time.Second, nil), nil)
			first, err := service.Lookup(ctx, "bank", false)
			if err != nil {
				t.Fatal(err)
			}
			oldAudio := first.Entries[0].Audio.UK.MediaID
			oldImage := first.Images[0].MediaID
			if oldAudio == "" || oldImage == "" {
				t.Fatal("initial copies failed")
			}
			_, item, err := store.SaveVocabulary(ctx, storage.VocabularyCreate{
				OwnerKey: "owner", Term: "bank", NormalizedTerm: "bank", LookupID: first.LookupID,
				Status: domain.LearningStatusNew, Context: "river", SenseKey: "context:river", Now: time.Now(),
			})
			if err != nil {
				t.Fatal(err)
			}
			failMedia.Store(true)
			refreshed, err := service.Lookup(ctx, "bank", true)
			if err != nil {
				t.Fatal(err)
			}
			cached, err := service.Lookup(ctx, "bank", false)
			if err != nil {
				t.Fatal(err)
			}
			if cached.LookupID != refreshed.LookupID || cached.Cache.State != domain.CacheHit {
				t.Fatal("refresh was not activated")
			}
			loaded, err := store.VocabularyByID(ctx, "owner", item.ItemID)
			if err != nil {
				t.Fatal(err)
			}
			for _, result := range []domain.DictionaryLookupResult{refreshed, cached, *loaded.Lookup} {
				wantAudio, wantImage := oldAudio, oldImage
				if changedURL {
					wantAudio, wantImage = "", ""
				}
				if result.Entries[0].Audio.UK.MediaID != wantAudio || result.Images[0].MediaID != wantImage || result.Entries[0].Definitions[0].Images[0].MediaID != wantImage {
					t.Fatalf("incorrect retained media: %#v", result)
				}
			}
			if _, err := store.MediaByID(ctx, oldAudio); err != nil {
				t.Fatal(err)
			}
		})
	}
}

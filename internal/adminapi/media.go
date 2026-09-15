package adminapi

import (
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

	"english-learning-mcp/internal/apperr"
	"english-learning-mcp/internal/storage"
	"english-learning-mcp/internal/vocabulary"
)

const (
	maxImageUploadBytes  = 10 << 20
	maxImageRequestBytes = maxImageUploadBytes + (64 << 10)
	maxImageFieldBytes   = 8 << 10
)

func decodeImageUpload(w http.ResponseWriter, r *http.Request) (vocabulary.ImageAttachmentInput, error) {
	mediaType, parameters, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" || parameters["boundary"] == "" {
		return vocabulary.ImageAttachmentInput{}, apperr.New(apperr.InvalidArgument, "Content-Type must be multipart/form-data with a boundary")
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxImageRequestBytes)
	reader := multipart.NewReader(r.Body, parameters["boundary"])
	var input vocabulary.ImageAttachmentInput
	seen := make(map[string]bool)
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return vocabulary.ImageAttachmentInput{}, apperr.New(apperr.InvalidArgument, "Image upload is malformed or exceeds 10 MiB")
		}
		name := part.FormName()
		if seen[name] || (name != "image" && name != "example" && name != "expectedRevision") {
			part.Close()
			return vocabulary.ImageAttachmentInput{}, apperr.New(apperr.InvalidArgument, "Image upload contains a duplicate or unsupported field")
		}
		seen[name] = true
		switch name {
		case "image":
			if part.FileName() == "" {
				part.Close()
				return vocabulary.ImageAttachmentInput{}, apperr.New(apperr.InvalidArgument, "image must be a file")
			}
			input.Data, err = io.ReadAll(io.LimitReader(part, maxImageUploadBytes+1))
			input.OriginalFilename = part.FileName()
			if err == nil && len(input.Data) > maxImageUploadBytes {
				err = fmt.Errorf("image exceeds limit")
			}
			if err == nil {
				input.ContentType, err = storage.ValidatedMediaContentType(part.Header.Get("Content-Type"), input.Data, storage.MediaKindImage)
			}
		case "example", "expectedRevision":
			var value []byte
			value, err = io.ReadAll(io.LimitReader(part, maxImageFieldBytes+1))
			if err == nil && len(value) > maxImageFieldBytes {
				err = fmt.Errorf("field exceeds limit")
			}
			if err == nil && name == "example" {
				input.Example = string(value)
			}
			if err == nil && name == "expectedRevision" {
				input.ExpectedRevision, err = strconv.ParseInt(strings.TrimSpace(string(value)), 10, 64)
			}
		}
		closeErr := part.Close()
		if err != nil || closeErr != nil {
			return vocabulary.ImageAttachmentInput{}, apperr.New(apperr.InvalidArgument, "Upload a JPEG, PNG, GIF, WebP, or AVIF image no larger than 10 MiB")
		}
	}
	if !seen["image"] || !seen["expectedRevision"] || input.ExpectedRevision <= 0 {
		return vocabulary.ImageAttachmentInput{}, apperr.New(apperr.InvalidArgument, "image and a positive expectedRevision are required")
	}
	return input, nil
}

func writeMedia(w http.ResponseWriter, media storage.MediaObject) error {
	w.Header().Set("Content-Type", media.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(media.ByteSize, 10))
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(media.Data)
	return err
}

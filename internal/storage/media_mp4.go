package storage

import "encoding/binary"

// The HTTP sniffer classifies ISO MP4 containers as video/mp4, even for AAC-only
// files. Inspect track handlers instead. This validates container structure and
// audio-only tracks, not individual encoded samples (as with the other sniffers).
func isAudioMP4(data []byte) bool {
	fileType, movie, mediaData := false, false, false
	tracks := 0
	valid := walkMP4Boxes(data, func(kind string, body []byte) bool {
		switch kind {
		case "ftyp":
			if fileType || len(body) < 8 || len(body)%4 != 0 {
				return false
			}
			for offset := 0; offset < len(body); offset += 4 {
				if offset == 4 {
					continue
				} // minor version, not a brand
				switch string(body[offset : offset+4]) {
				case "isom", "iso2", "iso3", "iso4", "iso5", "iso6", "iso8", "iso9", "mp41", "mp42", "M4A ", "M4B ", "M4P ":
					fileType = true
				}
			}
			return fileType
		case "moov":
			if movie {
				return false
			}
			movie = true
			return walkMP4Boxes(body, func(kind string, track []byte) bool {
				if kind != "trak" {
					return true
				}
				tracks++
				handlers, mediaBoxes := 0, 0
				valid := walkMP4Boxes(track, func(kind string, media []byte) bool {
					if kind != "mdia" {
						return true
					}
					mediaBoxes++
					return walkMP4Boxes(media, func(kind string, handler []byte) bool {
						if kind != "hdlr" {
							return true
						}
						handlers++
						return len(handler) >= 24 && handler[0] == 0 && string(handler[8:12]) == "soun"
					})
				})
				return valid && mediaBoxes == 1 && handlers == 1
			})
		case "mdat":
			mediaData = mediaData || len(body) > 0
		}
		return true
	})
	return valid && fileType && movie && mediaData && tracks > 0
}

// Sizes include their header. Fixed traversal depth and bounded input bytes keep
// this linear without decoding samples or allocating based on untrusted sizes.
func walkMP4Boxes(data []byte, visit func(string, []byte) bool) bool {
	for len(data) > 0 {
		if len(data) < 8 {
			return false
		}
		header := uint64(8)
		size := uint64(binary.BigEndian.Uint32(data[:4]))
		if size == 1 {
			if len(data) < 16 {
				return false
			}
			header, size = 16, binary.BigEndian.Uint64(data[8:16])
		} else if size == 0 {
			size = uint64(len(data))
		}
		if size < header || size > uint64(len(data)) || !visit(string(data[4:8]), data[header:size]) {
			return false
		}
		data = data[size:]
	}
	return true
}

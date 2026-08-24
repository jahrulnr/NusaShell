package domain

import "bytes"

// SniffMagic inspects the leading bytes of a media file and returns the
// detected media type (e.g. "image/png") and kind ("image", "audio",
// "video", "document"). Returns ("", "") when the bytes do not match any
// known media magic number.
//
// This is the single source of truth for file-type validation in the
// read_media tool (image, audio, video, and document/PDF). Extension-based MIME
// detection is intentionally NOT used here — extensions can be lied about
// (e.g. a .js file renamed to .png). Only the binary magic number is
// trustworthy.
//
// The function reads at most the first 32 bytes of data. Callers should
// pass at least 32 bytes; shorter buffers are handled safely but may not
// match formats whose signature extends further (e.g. WebM's doctype).
func SniffMagic(data []byte) (mediaType string, kind string) {
	if len(data) < 2 {
		return "", ""
	}

	// ── Document formats ───────────────────────────────────────────

	// PDF: "%PDF-" (versions 1.0–1.7, 2.0). The signature is always at
	// byte 0; the version number follows (e.g. "%PDF-1.4"). We match the
	// 5-byte prefix "%PDF-" which covers all versions.
	if len(data) >= 5 && bytes.Equal(data[:5], []byte("%PDF-")) {
		return "application/pdf", "document"
	}

	// ── Image formats ───────────────────────────────────────────────

	// JPEG: FF D8 FF
	if len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return "image/jpeg", "image"
	}
	// PNG: 89 50 4E 47 0D 0A 1A 0A
	if len(data) >= 8 && bytes.Equal(data[:8], []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}) {
		return "image/png", "image"
	}
	// GIF: "GIF87a" or "GIF89a"
	if len(data) >= 6 && (bytes.Equal(data[:6], []byte("GIF87a")) || bytes.Equal(data[:6], []byte("GIF89a"))) {
		return "image/gif", "image"
	}
	// BMP: "BM"
	if len(data) >= 2 && data[0] == 'B' && data[1] == 'M' {
		return "image/bmp", "image"
	}
	// TIFF: "II\x2a\x00" (little-endian) or "MM\x00\x2a" (big-endian)
	if len(data) >= 4 {
		if bytes.Equal(data[:4], []byte{'I', 'I', 0x2A, 0x00}) || bytes.Equal(data[:4], []byte{'M', 'M', 0x00, 0x2A}) {
			return "image/tiff", "image"
		}
	}

	// ── RIFF-based formats (WebP image, WAV audio, AVI video) ───────
	//
	// RIFF structure: "RIFF" + 4-byte size + 4-byte format tag.
	// The format tag at offset 8-11 discriminates:
	//   "WEBP" → image/webp
	//   "WAVE" → audio/wav
	//   "AVI " → video/x-msvideo

	if len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) {
		switch string(data[8:12]) {
		case "WEBP":
			return "image/webp", "image"
		case "WAVE":
			return "audio/wav", "audio"
		case "AVI ":
			return "video/x-msvideo", "video"
		}
		// Unknown RIFF variant — don't guess.
		return "", ""
	}

	// ── ISO BMFF (MP4 video/audio, MOV video, M4A audio) ───────────
	//
	// Structure: 4-byte box size + "ftyp" + 4-byte major brand.
	// The major brand at offset 8-11 discriminates:
	//   "M4A " / "M4V " → audio/mp4
	//   "qt  "          → video/quicktime
	//   "isom", "mp42", "iso2", "avc1", "iso5", "iso6", "mp41" → video/mp4
	//
	// We accept any "ftyp" box as MP4-family and refine by brand.

	if len(data) >= 12 && bytes.Equal(data[4:8], []byte("ftyp")) {
		brand := string(data[8:12])
		switch brand {
		case "M4A ", "M4V ":
			return "audio/mp4", "audio"
		case "qt  ":
			return "video/quicktime", "video"
		case "isom", "iso2", "iso3", "iso4", "iso5", "iso6", "iso7", "iso8", "iso9",
			"mp41", "mp42", "avc1", "f4v ", "dash":
			return "video/mp4", "video"
		}
		// Unknown ftyp brand — default to video/mp4 since the MP4
		// container is most commonly video. Audio-only MP4 uses "M4A "
		// which is handled above.
		return "video/mp4", "video"
	}

	// ── EBML-based formats (WebM video, MKV video) ─────────────────
	//
	// EBML header starts with 1A 45 DF A3. The doctype element appears
	// later in the stream and contains "webm" or "matroska". We scan
	// the first 32 bytes for the doctype string to discriminate.
	//
	// A full EBML parser is overkill here; scanning for the doctype
	// string within the first 32 bytes is reliable for all real-world
	// WebM/MKV files (the doctype element is always near the start).

	if len(data) >= 4 && bytes.Equal(data[:4], []byte{0x1A, 0x45, 0xDF, 0xA3}) {
		window := data
		if len(window) > 64 {
			window = window[:64]
		}
		if bytes.Contains(window, []byte("webm")) {
			return "video/webm", "video"
		}
		if bytes.Contains(window, []byte("matroska")) {
			return "video/x-matroska", "video"
		}
		// EBML but unknown doctype — don't guess.
		return "", ""
	}

	// ── Audio-only formats ─────────────────────────────────────────

	// MP3: "ID3" (ID3v2 tag) or MPEG audio frame sync (11-bit 0xFFE).
	// Frame sync: FF Ex where the high 3 bits of the second byte are
	// 111 (version bits). Common: FF FB, FF F3, FF F2, FF FA.
	if len(data) >= 3 && bytes.Equal(data[:3], []byte("ID3")) {
		return "audio/mpeg", "audio"
	}
	if len(data) >= 2 && data[0] == 0xFF && (data[1]&0xE0) == 0xE0 {
		// Distinguish AAC (FF F1 / FF F9) from MP3 (FF FB / FF F3 / FF F2 / FF FA).
		// AAC ADTS: FF F0 | FF F1 | FF F8 | FF F9
		switch data[1] {
		case 0xF1, 0xF9:
			return "audio/aac", "audio"
		case 0xFB, 0xF3, 0xF2, 0xFA:
			return "audio/mpeg", "audio"
		}
		// Other MPEG audio versions — default to audio/mpeg.
		return "audio/mpeg", "audio"
	}
	// OGG: "OggS"
	if len(data) >= 4 && bytes.Equal(data[:4], []byte("OggS")) {
		return "audio/ogg", "audio"
	}
	// FLAC: "fLaC"
	if len(data) >= 4 && bytes.Equal(data[:4], []byte("fLaC")) {
		return "audio/flac", "audio"
	}

	return "", ""
}

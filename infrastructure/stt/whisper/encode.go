package whisper

import (
	"bytes"
	"encoding/binary"
)

// encodeWav16 writes int16 samples as a canonical RIFF/WAVE PCM16 file —
// the exact input format whisper-cli demands (16-bit WAV, mono, 16 kHz).
func encodeWav16(samples []int16) []byte {
	dataSize := len(samples) * 2
	buf := new(bytes.Buffer)
	buf.WriteString("RIFF")
	putU32(buf, uint32(36+dataSize))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	putU32(buf, 16)           // fmt chunk size
	putU16(buf, 1)            // PCM
	putU16(buf, 1)            // mono
	putU32(buf, sampleRate)   // 16000 Hz
	putU32(buf, sampleRate*2) // byte rate
	putU16(buf, 2)            // block align (bytes per sample)
	putU16(buf, 16)           // bits per sample
	buf.WriteString("data")
	putU32(buf, uint32(dataSize))
	for _, s := range samples {
		putI16(buf, s)
	}
	return buf.Bytes()
}

func putU32(buf *bytes.Buffer, v uint32) {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, v)
	buf.Write(b)
}

func putU16(buf *bytes.Buffer, v uint16) {
	b := make([]byte, 2)
	binary.LittleEndian.PutUint16(b, v)
	buf.Write(b)
}

func putI16(buf *bytes.Buffer, v int16) {
	b := make([]byte, 2)
	binary.LittleEndian.PutUint16(b, uint16(v))
	buf.Write(b)
}

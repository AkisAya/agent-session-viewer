package dsh

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"

	"github.com/klauspost/compress/zstd"

	"github.com/akisaya/agent-session-viewer/internal/source"
)

// DSH stores compressed sessions as a plain concatenation of independent
// Zstandard frames: one frame for the header line, then one frame per durable
// append batch. Reading frame by frame lets an incremental scan resume at a
// frame boundary and ignore a torn final frame (a crash in the middle of a
// write), which a plain "decompress the whole file" pass cannot distinguish
// from a corrupt file.
const zstdMagic = 0xFD2FB528

var (
	errIncompleteFrame = errors.New("dsh: incomplete zstd frame")
	errCorruptFrame    = errors.New("dsh: corrupt zstd frame")
)

// readFrame reads exactly one complete Zstandard frame from r and returns its
// encoded bytes. It returns io.EOF at a clean end of file and
// errIncompleteFrame when the remaining bytes are a torn frame (which is
// recoverable: the caller keeps its previous offset and retries later).
func readFrame(r *bufio.Reader) ([]byte, error) {
	var buf bytes.Buffer

	magic := make([]byte, 4)
	if _, err := io.ReadFull(r, magic); err != nil {
		if err == io.EOF {
			return nil, io.EOF
		}
		return nil, errIncompleteFrame
	}
	if binary.LittleEndian.Uint32(magic) != zstdMagic {
		return nil, errCorruptFrame
	}
	buf.Write(magic)

	descriptor := make([]byte, 1)
	if _, err := io.ReadFull(r, descriptor); err != nil {
		return nil, errIncompleteFrame
	}
	buf.Write(descriptor)
	fhd := descriptor[0]
	if fhd&0x08 != 0 { // reserved bit must be zero
		return nil, errCorruptFrame
	}
	fcsFlag := fhd >> 6
	singleSegment := fhd&0x20 != 0
	checksum := fhd&0x04 != 0
	dictFlag := fhd & 0x03

	if !singleSegment {
		if err := copyN(&buf, r, 1); err != nil { // window descriptor
			return nil, err
		}
	}
	dictSizes := [4]int{0, 1, 2, 4}
	if err := copyN(&buf, r, dictSizes[dictFlag]); err != nil {
		return nil, err
	}
	fcsSizes := [4]int{0, 2, 4, 8}
	fcsSize := fcsSizes[fcsFlag]
	if fcsFlag == 0 && singleSegment {
		fcsSize = 1
	}
	if err := copyN(&buf, r, fcsSize); err != nil {
		return nil, err
	}

	for {
		header := make([]byte, 3)
		if _, err := io.ReadFull(r, header); err != nil {
			return nil, errIncompleteFrame
		}
		buf.Write(header)
		block := uint32(header[0]) | uint32(header[1])<<8 | uint32(header[2])<<16
		last := block&1 != 0
		blockType := (block >> 1) & 3
		size := int(block >> 3)

		switch blockType {
		case 0: // raw block
			if err := copyN(&buf, r, size); err != nil {
				return nil, err
			}
		case 1: // RLE block
			if err := copyN(&buf, r, 1); err != nil {
				return nil, err
			}
		case 2: // compressed block
			if err := copyN(&buf, r, size); err != nil {
				return nil, err
			}
		default: // reserved
			return nil, errCorruptFrame
		}
		if last {
			break
		}
	}
	if checksum {
		if err := copyN(&buf, r, 4); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

// copyN appends n bytes from r to buf, translating a short read into the
// incomplete-frame sentinel.
func copyN(buf *bytes.Buffer, r io.Reader, n int) error {
	if n <= 0 {
		return nil
	}
	if _, err := io.CopyN(buf, r, int64(n)); err != nil {
		return errIncompleteFrame
	}
	return nil
}

// forEachRecord calls fn for every complete JSONL record in path starting at
// offset. It returns the byte offset after the last complete frame (compressed)
// or line (raw), so the caller can persist an exact resume point. A torn final
// frame or unterminated final raw line is left unread for the next scan.
func forEachRecord(path string, offset int64, fn func([]byte) error) (int64, error) {
	if !isCompressed(path) {
		lines, newOffset, err := source.ReadNewLines(path, offset)
		for _, line := range lines {
			if ferr := fn(line); ferr != nil {
				return newOffset, ferr
			}
		}
		return newOffset, err
	}

	f, err := os.Open(path)
	if err != nil {
		return offset, err
	}
	defer f.Close()
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return offset, err
		}
	}

	dec, err := zstd.NewReader(nil)
	if err != nil {
		return offset, err
	}
	defer dec.Close()

	br := bufio.NewReaderSize(f, 1<<20)
	pos := offset
	for {
		frame, err := readFrame(br)
		if err == io.EOF || err == errIncompleteFrame {
			return pos, nil
		}
		if err != nil {
			return pos, err
		}
		data, err := dec.DecodeAll(frame, nil)
		if err != nil {
			return pos, err
		}
		if err := forEachLine(data, fn); err != nil {
			return pos, err
		}
		pos += int64(len(frame))
	}
}

// forEachLine splits one decompressed frame into JSONL records.
func forEachLine(data []byte, fn func([]byte) error) error {
	for len(data) > 0 {
		var line []byte
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			line, data = data[:i], data[i+1:]
		} else {
			line, data = data, nil
		}
		line = bytes.TrimRight(line, "\r")
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if err := fn(line); err != nil {
			return err
		}
	}
	return nil
}

func isCompressed(path string) bool {
	return len(path) > 5 && path[len(path)-5:] == ".zstd"
}

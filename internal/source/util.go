package source

import (
	"bufio"
	"bytes"
	"io"
	"os"
)

// ReadNewLines reads complete lines from path starting at byte offset. It stops
// before an unterminated trailing line so the returned offset always lands on a
// line boundary; the caller can safely resume there on the next scan.
func ReadNewLines(path string, offset int64) (lines [][]byte, newOffset int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, offset, err
	}
	defer f.Close()

	if offset > 0 {
		if _, err = f.Seek(offset, io.SeekStart); err != nil {
			return nil, offset, err
		}
	}

	br := bufio.NewReaderSize(f, 1<<20)
	newOffset = offset
	for {
		line, rerr := br.ReadBytes('\n')
		if len(line) > 0 {
			if line[len(line)-1] != '\n' {
				break // unterminated final line: leave it for the next scan
			}
			newOffset += int64(len(line))
			trimmed := bytes.TrimRight(line, "\r\n")
			if len(bytes.TrimSpace(trimmed)) > 0 {
				lines = append(lines, trimmed)
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				break
			}
			return lines, newOffset, rerr
		}
	}
	return lines, newOffset, nil
}

// ReadAllLines reads every complete line of a file.
func ReadAllLines(path string) ([][]byte, error) {
	lines, _, err := ReadNewLines(path, 0)
	return lines, err
}

// FirstText returns the first non-empty string among the candidates.
func FirstText(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// Truncate shortens s to at most n runes, appending an ellipsis when cut.
func Truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

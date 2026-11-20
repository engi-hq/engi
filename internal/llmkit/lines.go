package llmkit

import (
	"bufio"
	"errors"
	"io"
	"strings"
)

type lineReader struct{ br *bufio.Reader }

func newLineReader(r io.Reader) *lineReader { return &lineReader{br: bufio.NewReaderSize(r, 64<<10)} }

// next returns the next line without its line ending. At the end of input it
// returns the final partial line (possibly empty) together with io.EOF.
func (l *lineReader) next() (string, error) {
	var sb strings.Builder
	for {
		chunk, isPrefix, err := l.br.ReadLine()
		sb.Write(chunk)
		if sb.Len() > maxLineBytes {
			return "", errors.New("stream line exceeds the size limit")
		}
		if err != nil {
			return sb.String(), err //nolint:wrapcheck // io.EOF is compared by callers
		}
		if !isPrefix {
			return sb.String(), nil
		}
	}
}

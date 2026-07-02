package precision

import (
	"bufio"
	"strings"
	"testing"
)

func TestLSPFrameRejectsOversizedContentLength(t *testing.T) {
	t.Parallel()

	client := &rustAnalyzerClient{
		stdout: bufio.NewReader(strings.NewReader("Content-Length: 67108865\r\n\r\n")),
	}
	_, err := client.read()
	if err == nil {
		t.Fatal("read error = nil, want out of bounds content-length")
	}
	if !strings.Contains(err.Error(), "out of bounds") {
		t.Fatalf("read error = %q, want out of bounds", err.Error())
	}
}

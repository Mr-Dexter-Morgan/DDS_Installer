package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestProgressKnownSizeIsMeasured(t *testing.T) {
	src := bytes.Repeat([]byte("A"), 64<<10)
	var dst bytes.Buffer
	var seen []downloadProgress
	n, err := copyDownload(&dst, bytes.NewReader(src), int64(len(src)), int64(len(src)), func(v downloadProgress) { seen = append(seen, v) })
	if err != nil || n != int64(len(src)) || !bytes.Equal(src, dst.Bytes()) {
		t.Fatal("bytes changed", n, err)
	}
	if len(seen) < 2 || seen[0].Bytes != 0 || seen[0].Finished || !seen[len(seen)-1].Finished || seen[len(seen)-1].Bytes != n || seen[len(seen)-1].Total != n {
		t.Fatalf("incorrect progress: %+v", seen)
	}
	line := formatDownloadLine(n, n, 0, 2*time.Second, true)
	if !strings.Contains(line, "100%") || !strings.Contains(line, "[##################]") {
		t.Fatal(line)
	}
}

func TestProgressUnknownSizeDoesNotInventPercent(t *testing.T) {
	line := formatDownloadLine(512<<10, -1, 2, 3*time.Second, false)
	if strings.Contains(line, "%") || !strings.Contains(line, "0.5 МиБ") || !strings.Contains(line, "3 с") {
		t.Fatal(line)
	}
	line2 := formatDownloadLine(512<<10, -1, 2, 3*time.Second, true)
	if strings.Contains(line2, "%") || !strings.Contains(line2, "размер неизвестен") {
		t.Fatal(line2)
	}
}

func TestProgressPrematureEndDoesNotClaimComplete(t *testing.T) {
	var out bytes.Buffer
	var final downloadProgress
	_, err := copyDownload(&out, strings.NewReader("abc"), 99, 30, func(x downloadProgress) { final = x })
	if err != nil || !final.Finished || final.Bytes != 3 || final.Total != 30 {
		t.Fatal(final, err)
	}
	line := formatDownloadLine(final.Bytes, final.Total, 0, 0, true)
	if strings.Contains(line, "100%") || !strings.Contains(line, " 10%") {
		t.Fatal(line)
	}
}

type failingDownloadReader struct{ read bool }

func (f *failingDownloadReader) Read(b []byte) (int, error) {
	if !f.read {
		f.read = true
		copy(b, []byte("abc"))
		return 3, nil
	}
	return 0, errors.New("connection lost")
}
func TestProgressErrorTerminatesDisplay(t *testing.T) {
	var target bytes.Buffer
	display := newConsoleDownloadDisplay(&target)
	_, err := copyDownload(io.Discard, &failingDownloadReader{}, 100, 10, display.update)
	if err == nil || !strings.Contains(err.Error(), "connection lost") {
		t.Fatal(err)
	}
	text := target.String()
	if !strings.HasSuffix(text, "\n") || strings.Contains(text, "100%") || !strings.Contains(text, "30%") {
		t.Fatal(text)
	}
}

func TestDownloadProgressDoesNotChangeSizeLimit(t *testing.T) {
	var dst bytes.Buffer
	n, err := copyDownload(&dst, strings.NewReader("abcdef"), 3, 6, nil)
	if err != nil || n != 4 || dst.String() != "abcd" {
		t.Fatal(n, err, dst.String())
	}
}

func TestProgressHeartbeatWhenNetworkStalls(t *testing.T) {
	var b bytes.Buffer
	display := newConsoleDownloadDisplay(&b)
	display.update(downloadProgress{Bytes: 0, Total: 100})
	time.Sleep(1100 * time.Millisecond)
	display.update(downloadProgress{Bytes: 10, Total: 100, Finished: true})
	if strings.Count(b.String(), "\r") < 3 || strings.Contains(b.String(), "100%") {
		t.Fatalf("no honest heartbeat during stall: %q", b.String())
	}
}

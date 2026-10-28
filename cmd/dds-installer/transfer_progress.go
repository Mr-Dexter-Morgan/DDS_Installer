package main

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// downloadProgress describes ONLY the number of bytes read from the response.
// A known total comes from the verified release manifest or the HTTP response;
// an unknown total (-1) must never be represented as a guessed percentage.
type downloadProgress struct {
	Bytes    int64
	Total    int64
	Finished bool
}

// copyDownload counts successfully written network bytes without changing
// size caps, hash verification, or the existing staged-file transaction.
func copyDownload(dst io.Writer, src io.Reader, maxBytes, total int64, report func(downloadProgress)) (int64, error) {
	writer := &countingDownloadWriter{target: dst, total: total, report: report}
	if report != nil {
		report(downloadProgress{Total: total})
	}
	n, err := io.Copy(writer, io.LimitReader(src, maxBytes+1))
	if report != nil {
		report(downloadProgress{Bytes: n, Total: total, Finished: true})
	}
	return n, err
}

type countingDownloadWriter struct {
	target io.Writer
	report func(downloadProgress)
	total  int64
	n      int64
	last   time.Time
}

func (w *countingDownloadWriter) Write(p []byte) (int, error) {
	n, err := w.target.Write(p)
	w.n += int64(n)
	if w.report != nil && (time.Since(w.last) >= 150*time.Millisecond || w.total > 0 && w.n >= w.total) {
		w.report(downloadProgress{Bytes: w.n, Total: w.total})
		w.last = time.Now()
	}
	return n, err
}

// The display is single-download at a time, but keeps animating if a network
// read stalls. This is an activity signal, NOT a made-up transfer percentage.
type consoleDownloadDisplay struct {
	out        io.Writer
	mu         sync.Mutex
	active     bool
	done       int64
	total      int64
	started    time.Time
	frame      int
	stop       chan struct{}
	stopped    chan struct{}
	lastLength int
}

func newConsoleDownloadDisplay(out io.Writer) *consoleDownloadDisplay {
	return &consoleDownloadDisplay{out: out}
}

func (d *consoleDownloadDisplay) update(p downloadProgress) {
	d.mu.Lock()
	if !d.active {
		d.active = true
		d.started = time.Now()
		d.stop = make(chan struct{})
		d.stopped = make(chan struct{})
		stop, stopped := d.stop, d.stopped
		go func() {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			defer close(stopped)
			for {
				select {
				case <-stop:
					return
				case <-ticker.C:
					d.mu.Lock()
					if d.active {
						d.frame++
						d.renderLocked(false)
					}
					d.mu.Unlock()
				}
			}
		}()
	}
	d.done, d.total = p.Bytes, p.Total
	if p.Finished {
		d.renderLocked(true)
		d.active = false
		close(d.stop)
		stopped := d.stopped
		d.mu.Unlock()
		<-stopped
		return
	}
	d.renderLocked(false)
	d.mu.Unlock()
}

func (d *consoleDownloadDisplay) renderLocked(finished bool) {
	line := formatDownloadLine(d.done, d.total, d.frame, time.Since(d.started), finished)
	pad := d.lastLength - len([]rune(line))
	if pad < 0 {
		pad = 0
	}
	fmt.Fprint(d.out, "\r", line, strings.Repeat(" ", pad))
	d.lastLength = len([]rune(line))
	if finished {
		fmt.Fprintln(d.out)
	}
}

func formatDownloadLine(done, total int64, frame int, elapsed time.Duration, finished bool) string {
	const width = 18
	if done < 0 {
		done = 0
	}
	seconds := int(elapsed.Seconds())
	if total > 0 {
		// Cap the visual bar at 100, never alter the reported byte count.
		pct := int(done * 100 / total)
		if pct > 100 {
			pct = 100
		}
		count := pct * width / 100
		return fmt.Sprintf("  [%-18s] %3d%% | %.1f/%.1f МиБ | %d с", strings.Repeat("#", count), pct, float64(done)/(1<<20), float64(total)/(1<<20), seconds)
	}
	if finished {
		return fmt.Sprintf("  [%s] Получено %.1f МиБ | %d с (размер неизвестен)", strings.Repeat("#", width), float64(done)/(1<<20), seconds)
	}
	spin := []byte{'|', '/', '-', '\\'}
	return fmt.Sprintf("  [%c] Получено %.1f МиБ | %d с (размер неизвестен)", spin[frame%len(spin)], float64(done)/(1<<20), seconds)
}

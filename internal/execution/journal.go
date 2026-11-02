package execution

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Journal is an append-only event log with stable sequence numbers. It is the
// source of truth for recovery: the snapshot file is only a fast path, and a
// torn final line is skipped instead of corrupting the rest of the history.
//
// Event kinds:
//
//	run.created   a run opened
//	task.added    a task entered the graph
//	task.moved    a status transition (from/to validated by the store)
//	task.attempt  an attempt counter bump
//	task.error    the last error for a task
//	verify.added  a verification result
//	checkpoint    a snapshot reference for recovery
//	run.closed    the run reached a terminal state
type Event struct {
	Seq  int64           `json:"seq"`
	At   string          `json:"at"`
	Kind string          `json:"kind"`
	Task string          `json:"task,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
}

// Journal writes events as newline-delimited JSON.
type Journal struct {
	mu   sync.Mutex
	path string
	file *os.File
	seq  int64
}

// OpenJournal opens (or creates) the journal at path and returns the last
// valid sequence number found in it. A truncated trailing record is ignored
// and truncated away so the next append lands on a clean boundary.
func OpenJournal(path string) (*Journal, int64, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, 0, err
	}
	last, truncatedTo, err := scanJournal(path)
	if err != nil {
		return nil, 0, err
	}
	if truncatedTo >= 0 {
		if err := truncateFile(path, truncatedTo); err != nil {
			return nil, 0, err
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, 0, err
	}
	return &Journal{path: path, file: f, seq: last}, last, nil
}

// Append writes one event, assigning the next sequence number.
func (j *Journal) Append(kind, task string, data any) (Event, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	var raw json.RawMessage
	if data != nil {
		b, err := json.Marshal(data)
		if err != nil {
			return Event{}, err
		}
		raw = b
	}
	j.seq++
	ev := Event{Seq: j.seq, At: time.Now().UTC().Format(time.RFC3339Nano), Kind: kind, Task: task, Data: raw}
	line, err := json.Marshal(ev)
	if err != nil {
		return Event{}, err
	}
	if j.file == nil {
		return ev, fmt.Errorf("journal is closed")
	}
	if _, err := j.file.Write(append(line, '\n')); err != nil {
		return ev, err
	}
	return ev, j.file.Sync()
}

// Close releases the file handle. It is safe to call more than once.
func (j *Journal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.file == nil {
		return nil
	}
	err := j.file.Close()
	j.file = nil
	return err
}

// Replay reads every valid event in order.
func Replay(path string) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev Event
		if json.Unmarshal([]byte(line), &ev) != nil || ev.Kind == "" {
			// A corrupt record ends the valid history: everything after it is
			// untrustworthy, so stop instead of skipping ahead.
			break
		}
		out = append(out, ev)
	}
	return out, sc.Err()
}

// scanJournal returns the last valid sequence number and, when the file ends
// in a torn record, the byte offset where the valid prefix stops.
func scanJournal(path string) (last int64, truncateAt int64, err error) {
	truncateAt = -1
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, -1, nil
		}
		return 0, -1, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	var offset int64 // end offset of the last valid line
	for sc.Scan() {
		line := sc.Bytes()
		var ev Event
		valid := len(strings.TrimSpace(string(line))) > 0 && json.Unmarshal(line, &ev) == nil && ev.Kind != ""
		offset += int64(len(line)) + 1
		if valid {
			last = ev.Seq
		} else if strings.TrimSpace(string(line)) != "" {
			// A corrupt complete line: keep the history before it and cut the
			// rest, so later appends never follow garbage.
			truncateAt = offset - int64(len(line)) - 1
			break
		}
	}
	if err := sc.Err(); err != nil {
		return last, -1, err
	}
	info, statErr := os.Stat(path)
	if statErr == nil && info.Size() > offset && offset >= 0 && truncateAt < 0 {
		truncateAt = offset // trailing partial line without newline
	}
	return last, truncateAt, nil
}

func truncateFile(path string, size int64) error {
	if size < 0 {
		return nil
	}
	return os.Truncate(path, size)
}

// maxJournalBytes caps a single journal before it is rolled to an archive.
const maxJournalBytes = 8 << 20

// NeedsRoll reports whether the journal has grown past its size budget.
func (j *Journal) NeedsRoll() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.file == nil {
		return false
	}
	info, err := j.file.Stat()
	return err == nil && info.Size() > maxJournalBytes
}

// Roll closes the journal and renames it to path + ".1" (replacing an older
// roll). The caller reopens a fresh journal at path.
func (j *Journal) Roll() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.file != nil {
		_ = j.file.Close()
		j.file = nil
	}
	archive := j.path + ".1"
	_ = os.Remove(archive)
	return os.Rename(j.path, archive)
}

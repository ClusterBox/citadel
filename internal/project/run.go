package project

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"
)

// MaxRuns is how many run folders NewRun keeps under .citadel/runs/.
const MaxRuns = 50

// Run and step statuses written to run.json and state/<env>.json.
const (
	StatusInProgress = "in_progress"
	StatusSuccess    = "success"
	StatusFailed     = "failed"
	StatusSkipped    = "skipped"
)

const runIDLayout = "20060102T150405Z"

// runIDRe matches run folder names; anything else under runs/ is left alone.
var runIDRe = regexp.MustCompile(`^\d{8}T\d{6}Z-[0-9a-f]{4}$`)

// RunInfo describes the deploy a Run records.
type RunInfo struct {
	Env     string
	Message string
	GitSHA  string
}

// StepRecord is one entry of run.json's steps list.
type StepRecord struct {
	Name       string     `json:"name"`
	Status     string     `json:"status"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	DurationMS int64      `json:"duration_ms"`
	Error      string     `json:"error,omitempty"`
}

// runFile is the on-disk shape of run.json.
type runFile struct {
	ID         string       `json:"id"`
	Env        string       `json:"env"`
	Message    string       `json:"message"`
	GitSHA     string       `json:"git_sha"`
	Status     string       `json:"status"`
	Error      string       `json:"error,omitempty"`
	StartedAt  time.Time    `json:"started_at"`
	FinishedAt *time.Time   `json:"finished_at,omitempty"`
	Steps      []StepRecord `json:"steps"`
}

// Run records one deploy under .citadel/runs/<id>/: run.json plus one
// NN-<step>.log per executed step. Logging is best-effort: the first I/O
// failure prints a single warning to the terminal and the Run carries on
// writing to the terminal only. A terminal-only Run (TerminalRun) records
// nothing on disk.
type Run struct {
	mu       sync.Mutex
	parent   *Dir
	dir      string // run folder; "" once disabled or for a terminal-only run
	term     io.Writer
	now      func() time.Time
	rec      runFile
	seq      int
	finished bool
}

// Step is one named stage of a Run.
type Step struct {
	run     *Run
	idx     int // index into run.rec.Steps; -1 when not recorded
	out     io.Writer
	file    *os.File
	started time.Time
	ended   bool
}

// NewRun creates a new run folder, writes its initial run.json and prunes
// runs beyond MaxRuns. term receives step output alongside the log files.
func (d *Dir) NewRun(info RunInfo, term io.Writer) (*Run, error) {
	return d.newRun(info, term, time.Now)
}

func (d *Dir) newRun(info RunInfo, term io.Writer, now func() time.Time) (*Run, error) {
	root := filepath.Join(d.path, runsDirName)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", root, err)
	}
	start := now().UTC()
	var id, path string
	for attempt := 0; ; attempt++ {
		id = start.Format(runIDLayout) + "-" + randomHex(2)
		path = filepath.Join(root, id)
		err := os.Mkdir(path, 0o755)
		if err == nil {
			break
		}
		if errors.Is(err, os.ErrExist) && attempt < 20 {
			continue
		}
		return nil, fmt.Errorf("create run folder: %w", err)
	}
	r := &Run{
		parent: d,
		dir:    path,
		term:   term,
		now:    now,
		rec: runFile{
			ID: id, Env: info.Env, Message: info.Message, GitSHA: info.GitSHA,
			Status: StatusInProgress, StartedAt: start, Steps: []StepRecord{},
		},
	}
	if err := r.writeRunFile(); err != nil {
		os.RemoveAll(path)
		return nil, err
	}
	prune(root, MaxRuns)
	return r, nil
}

// TerminalRun returns a Run that sends step output to term only and records
// nothing on disk. Used for --dry-run and when .citadel/ is unavailable.
func TerminalRun(term io.Writer) *Run {
	return &Run{term: term, now: time.Now}
}

// ID returns the run id, or "" for a terminal-only run.
func (r *Run) ID() string { return r.rec.ID }

// Dir returns the run folder, or "" when nothing is recorded on disk.
func (r *Run) Dir() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dir
}

// Step starts a named step. Its Out() writer mirrors to the terminal and to
// NN-<name>.log, where NN counts executed steps from 01.
func (r *Run) Step(name string) *Step {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := &Step{run: r, idx: -1, out: r.term, started: r.now()}
	if r.dir == "" {
		return s
	}
	r.seq++
	f, err := os.Create(filepath.Join(r.dir, fmt.Sprintf("%02d-%s.log", r.seq, name)))
	if err != nil {
		r.disableLocked(err)
		return s
	}
	s.file = f
	s.out = &teeWriter{term: r.term, file: f}
	started := s.started.UTC()
	r.rec.Steps = append(r.rec.Steps, StepRecord{Name: name, Status: StatusInProgress, StartedAt: &started})
	s.idx = len(r.rec.Steps) - 1
	r.flushLocked()
	return s
}

// Out is where the step writes its output.
func (s *Step) Out() io.Writer { return s.out }

// End closes the step's log and records its status and duration. Only the
// first call has effect.
func (s *Step) End(err error) {
	r := s.run
	r.mu.Lock()
	defer r.mu.Unlock()
	if s.ended {
		return
	}
	s.ended = true
	if s.file != nil {
		s.file.Close()
	}
	if s.idx < 0 || r.dir == "" {
		return
	}
	rec := &r.rec.Steps[s.idx]
	rec.DurationMS = r.now().Sub(s.started).Milliseconds()
	rec.Status = StatusSuccess
	if err != nil {
		rec.Status = StatusFailed
		rec.Error = err.Error()
	}
	r.flushLocked()
}

// Skip records a step that did not run. It gets no number and no log file.
func (r *Run) Skip(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.dir == "" {
		return
	}
	r.rec.Steps = append(r.rec.Steps, StepRecord{Name: name, Status: StatusSkipped})
	r.flushLocked()
}

// Finish records the outcome in run.json and state/<env>.json, filling st's
// RunID, Status, GitSHA and times. Only the first call has effect, so callers
// can both defer it and call it early (e.g. before blocking on log streaming).
func (r *Run) Finish(err error, st State) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return
	}
	r.finished = true
	if r.dir == "" {
		return
	}
	end := r.now().UTC()
	status := StatusSuccess
	if err != nil {
		status = StatusFailed
		r.rec.Error = err.Error()
	}
	r.rec.Status = status
	r.rec.FinishedAt = &end
	r.flushLocked()
	if r.dir == "" {
		return
	}
	st.RunID = r.rec.ID
	st.Status = status
	st.GitSHA = r.rec.GitSHA
	st.StartedAt = r.rec.StartedAt
	st.FinishedAt = end
	if werr := r.parent.WriteState(r.rec.Env, st); werr != nil {
		r.disableLocked(werr)
	}
}

func (r *Run) writeRunFile() error {
	data, err := json.MarshalIndent(r.rec, "", "  ")
	if err != nil {
		return fmt.Errorf("encode run.json: %w", err)
	}
	return WriteFileAtomic(filepath.Join(r.dir, "run.json"), append(data, '\n'))
}

// flushLocked rewrites run.json, disabling the run on failure. r.mu is held.
func (r *Run) flushLocked() {
	if err := r.writeRunFile(); err != nil {
		r.disableLocked(err)
	}
}

// disableLocked prints one warning and stops all further disk writes. r.mu is held.
func (r *Run) disableLocked(err error) {
	if r.dir == "" {
		return
	}
	r.dir = ""
	fmt.Fprintf(r.term, "   ⚠️  run logging disabled: %v\n", err)
}

// teeWriter mirrors writes to the terminal and a step's log file. Callers see
// the terminal's result only: after the log file's first error it is dropped
// silently, so a full disk can never abort a docker/cdk output stream.
type teeWriter struct {
	mu   sync.Mutex
	term io.Writer
	file io.Writer
}

func (t *teeWriter) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	n, err := t.term.Write(p)
	if t.file != nil {
		if _, ferr := t.file.Write(p); ferr != nil {
			t.file = nil
		}
	}
	return n, err
}

// prune removes all but the newest keep run folders under root. Entries that
// are not run folders are left alone. Errors are ignored: pruning is housekeeping.
func prune(root string, keep int) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() && runIDRe.MatchString(e.Name()) {
			ids = append(ids, e.Name())
		}
	}
	if len(ids) <= keep {
		return
	}
	sort.Strings(ids)
	for _, id := range ids[:len(ids)-keep] {
		os.RemoveAll(filepath.Join(root, id))
	}
}

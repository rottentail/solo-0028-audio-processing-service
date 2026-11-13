package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrNotFound = errors.New("job not found")
var ErrConflict = errors.New("job state does not permit this operation")
var ErrFull = errors.New("job queue is full")
var ErrInvalid = errors.New("invalid job input")

type Job struct {
	ID        string    `json:"id"`
	Operation string    `json:"operation"`
	State     string    `json:"state"`
	Error     string    `json:"error,omitempty"`
	Created   time.Time `json:"created"`
	Updated   time.Time `json:"updated"`
	Attempt   int       `json:"attempt"`
}

type Manager struct {
	mu           sync.Mutex
	root, ffmpeg string
	jobs         map[string]*Job
	cancels      map[string]context.CancelFunc
	queue        chan string
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
}

// Open requires exclusive ownership of root by a single service process.
func Open(root, ffmpeg string, workers, capacity int) (*Manager, error) {
	if workers < 1 || capacity < 1 {
		return nil, errors.New("workers and queue capacity must be positive")
	}
	if _, err := exec.LookPath(ffmpeg); err != nil {
		return nil, fmt.Errorf("ffmpeg: %w", err)
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{root: root, ffmpeg: ffmpeg, jobs: map[string]*Job{}, cancels: map[string]context.CancelFunc{}, queue: make(chan string, capacity), ctx: ctx, cancel: cancel}
	entries, err := os.ReadDir(root)
	if err != nil {
		cancel()
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !validID(entry.Name()) {
			continue
		}
		b, e := os.ReadFile(filepath.Join(root, entry.Name(), "job.json"))
		if e != nil {
			cancel()
			return nil, e
		}
		var job Job
		if e = json.Unmarshal(b, &job); e != nil || job.ID != entry.Name() {
			cancel()
			return nil, fmt.Errorf("invalid job record %s", entry.Name())
		}
		if job.State == "queued" || job.State == "running" {
			job.State = "failed"
			job.Error = "interrupted by service restart"
			job.Updated = time.Now().UTC()
			if e = m.save(&job); e != nil {
				cancel()
				return nil, e
			}
		}
		files, e := os.ReadDir(filepath.Join(root, job.ID))
		if e != nil {
			cancel()
			return nil, e
		}
		for _, file := range files {
			if strings.HasPrefix(file.Name(), "output-") && strings.HasSuffix(file.Name(), ".tmp") {
				_ = os.Remove(filepath.Join(root, job.ID, file.Name()))
			}
		}
		m.jobs[job.ID] = &job
	}
	for i := 0; i < workers; i++ {
		m.wg.Add(1)
		go m.worker()
	}
	return m, nil
}
func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil && strings.ToLower(id) == id
}
func (m *Manager) save(j *Job) error {
	b, err := json.Marshal(j)
	if err != nil {
		return err
	}
	dir := filepath.Join(m.root, j.ID)
	f, err := os.CreateTemp(dir, ".state-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	return os.Rename(name, filepath.Join(dir, "job.json"))
}
func (m *Manager) transition(j *Job, state, detail string) error {
	old := *j
	j.State = state
	j.Error = detail
	j.Updated = time.Now().UTC()
	if err := m.save(j); err != nil {
		*j = old
		return err
	}
	return nil
}
func (m *Manager) Create(operation string, input io.Reader) (Job, error) {
	if operation != "video-mp4" && operation != "audio-m4a" && operation != "thumbnail" {
		return Job{}, fmt.Errorf("%w: unsupported operation", ErrInvalid)
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return Job{}, err
	}
	id := hex.EncodeToString(raw[:])
	dir := filepath.Join(m.root, id)
	if err := os.Mkdir(dir, 0700); err != nil {
		return Job{}, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(dir)
		}
	}()
	f, err := os.OpenFile(filepath.Join(dir, "input"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return Job{}, err
	}
	n, err := io.Copy(f, io.LimitReader(input, (64<<20)+1))
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return Job{}, err
	}
	if n == 0 || n > 64<<20 {
		return Job{}, fmt.Errorf("%w: input must be between 1 byte and 64 MiB", ErrInvalid)
	}
	now := time.Now().UTC()
	j := &Job{ID: id, Operation: operation, State: "queued", Created: now, Updated: now, Attempt: 1}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ctx.Err() != nil {
		return Job{}, ErrConflict
	}
	if len(m.queue) == cap(m.queue) {
		return Job{}, ErrFull
	}
	if err = m.save(j); err != nil {
		return Job{}, err
	}
	m.jobs[id] = j
	m.queue <- id
	keep = true
	return *j, nil
}
func (m *Manager) Get(id string) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.jobs[id]
	if j == nil {
		return Job{}, ErrNotFound
	}
	return *j, nil
}
func (m *Manager) List() []Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Job, 0, len(m.jobs))
	for _, j := range m.jobs {
		out = append(out, *j)
	}
	sort.Slice(out, func(i, k int) bool { return out[i].Created.Before(out[k].Created) })
	return out
}
func (m *Manager) Cancel(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.jobs[id]
	if j == nil {
		return ErrNotFound
	}
	if j.State != "queued" && j.State != "running" {
		return ErrConflict
	}
	if err := m.transition(j, "cancelled", ""); err != nil {
		return err
	}
	if cancel := m.cancels[id]; cancel != nil {
		cancel()
	}
	return nil
}
func (m *Manager) Retry(id string) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.jobs[id]
	if j == nil {
		return Job{}, ErrNotFound
	}
	if (j.State != "failed" && j.State != "cancelled") || m.ctx.Err() != nil || m.cancels[id] != nil {
		return Job{}, ErrConflict
	}
	if len(m.queue) == cap(m.queue) {
		return Job{}, ErrFull
	}
	old := *j
	j.Attempt++
	if err := m.transition(j, "queued", ""); err != nil {
		*j = old
		return Job{}, err
	}
	m.queue <- id
	return *j, nil
}
func (m *Manager) Output(id string) (*os.File, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.jobs[id]
	if j == nil {
		return nil, "", ErrNotFound
	}
	if j.State != "completed" {
		return nil, "", ErrConflict
	}
	f, err := os.Open(filepath.Join(m.root, id, "output"))
	if err != nil {
		return nil, "", err
	}
	mime := "video/mp4"
	if j.Operation == "audio-m4a" {
		mime = "audio/mp4"
	}
	if j.Operation == "thumbnail" {
		mime = "image/jpeg"
	}
	return f, mime, nil
}
func (m *Manager) worker() {
	defer m.wg.Done()
	for {
		select {
		case <-m.ctx.Done():
			return
		case id := <-m.queue:
			m.run(id)
		}
	}
}
func (m *Manager) run(id string) {
	m.mu.Lock()
	j := m.jobs[id]
	if j == nil || j.State != "queued" || m.ctx.Err() != nil {
		m.mu.Unlock()
		return
	}
	if err := m.transition(j, "running", ""); err != nil {
		m.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.cancels[id] = cancel
	operation := j.Operation
	attempt := j.Attempt
	m.mu.Unlock()
	dir := filepath.Join(m.root, id)
	temp := filepath.Join(dir, fmt.Sprintf("output-%d.tmp", attempt))
	_ = os.Remove(temp)
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-i", filepath.Join(dir, "input")}
	switch operation {
	case "video-mp4":
		args = append(args, "-map", "0:v:0", "-map", "0:a:0?", "-c:v", "libx264", "-preset", "veryfast", "-c:a", "aac", "-movflags", "+faststart", "-f", "mp4")
	case "audio-m4a":
		args = append(args, "-map", "0:a:0", "-vn", "-c:a", "aac", "-f", "ipod")
	case "thumbnail":
		args = append(args, "-map", "0:v:0", "-frames:v", "1", "-f", "image2", "-vcodec", "mjpeg")
	}
	args = append(args, temp)
	runctx, timeout := context.WithTimeout(ctx, 5*time.Minute)
	cmd := exec.CommandContext(runctx, m.ffmpeg, args...)
	err := cmd.Run()
	timeout()
	cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.cancels, id)
	j = m.jobs[id]
	if j.State != "running" {
		_ = os.Remove(temp)
		return
	}
	if m.ctx.Err() != nil {
		_ = os.Remove(temp)
		_ = m.transition(j, "failed", "interrupted by service shutdown")
		return
	}
	if err != nil {
		_ = os.Remove(temp)
		_ = m.transition(j, "failed", "media processing failed")
		return
	}
	stat, err := os.Stat(temp)
	if err == nil && stat.Size() == 0 {
		err = errors.New("empty output")
	}
	if err == nil {
		err = os.Rename(temp, filepath.Join(dir, "output"))
	}
	if err != nil {
		_ = os.Remove(temp)
		_ = m.transition(j, "failed", "cannot save output")
		return
	}
	if err = m.transition(j, "completed", ""); err != nil {
		_ = os.Remove(filepath.Join(dir, "output"))
	}
}
func (m *Manager) Close() { m.cancel(); m.wg.Wait() }

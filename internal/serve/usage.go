package serve

import (
	"bufio"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hasoftware/claude-credentials-manager/v2/internal/usage"
)

var reportingZone = time.FixedZone("UTC+07", 7*60*60)

type tokenCounts struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
	Requests   int64 `json:"requests"`
}

func (c *tokenCounts) add(v tokenCounts) {
	c.Input += v.Input
	c.Output += v.Output
	c.CacheRead += v.CacheRead
	c.CacheWrite += v.CacheWrite
	c.Requests += v.Requests
}

type usageBucket struct{ day, model string }
type indexedFile struct {
	info    os.FileInfo
	offset  int64
	buckets map[usageBucket]tokenCounts
}

type usageIndex struct {
	mu    sync.Mutex
	files map[string]*indexedFile
}

type tokenReport struct {
	Version    int                    `json:"version"`
	Date       string                 `json:"date"`
	Month      string                 `json:"month"`
	FirstDay   string                 `json:"firstDay"`
	Today      tokenCounts            `json:"today"`
	MonthTotal tokenCounts            `json:"monthTotal"`
	Days       map[string]tokenCounts `json:"days"`
	Models     map[string]tokenCounts `json:"models"`
	Months     []string               `json:"months"`
}

func (idx *usageIndex) report(dir, month string, now time.Time) (tokenReport, error) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	today := now.In(reportingZone).Format("2006-01-02")
	result := tokenReport{Version: APIVersion, Date: today, Month: month, Days: map[string]tokenCounts{}, Models: map[string]tokenCounts{}}
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return result, err
	}
	current := make(map[string]*indexedFile)
	months := map[string]bool{today[:7]: true, month: true}
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".ndjson") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return result, err
		}
		f := idx.files[entry.Name()]
		if f == nil || !os.SameFile(f.info, info) || info.Size() < f.info.Size() || (info.Size() == f.info.Size() && !info.ModTime().Equal(f.info.ModTime())) {
			f = &indexedFile{buckets: map[usageBucket]tokenCounts{}}
		}
		if f.offset < info.Size() {
			if err := f.read(filepath.Join(dir, entry.Name()), info.Size()); err != nil {
				return result, err
			}
		}
		f.info = info
		current[entry.Name()] = f
		for key, v := range f.buckets {
			if key.day > today {
				continue
			}
			if result.FirstDay == "" || key.day < result.FirstDay {
				result.FirstDay = key.day
			}
			months[key.day[:7]] = true
			if key.day == today {
				result.Today.add(v)
			}
			if key.day[:7] != month {
				continue
			}
			result.MonthTotal.add(v)
			d := result.Days[key.day]
			d.add(v)
			result.Days[key.day] = d
			m := result.Models[key.model]
			m.add(v)
			result.Models[key.model] = m
		}
	}
	idx.files = current
	for key := range months {
		result.Months = append(result.Months, key)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(result.Months)))
	return result, nil
}

// Advance only past complete lines so an append in progress is seen on the next refresh.
func (f *indexedFile) read(path string, size int64) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	r := bufio.NewReader(io.NewSectionReader(file, f.offset, size-f.offset))
	for {
		line, err := r.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		f.offset += int64(len(line))
		rec, err := usage.UnmarshalRecord(line)
		if err != nil || rec.TS.IsZero() || rec.In < 0 || rec.Out < 0 || rec.CR < 0 || rec.CW < 0 {
			continue
		}
		model := rec.Model
		if model == "" {
			model = "Unknown"
		}
		k := usageBucket{rec.TS.In(reportingZone).Format("2006-01-02"), model}
		c := f.buckets[k]
		c.add(tokenCounts{rec.In, rec.Out, rec.CR, rec.CW, 1})
		f.buckets[k] = c
	}
}

func (h *handler) apiUsage(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	month := r.URL.Query().Get("month")
	if month == "" {
		month = now.In(reportingZone).Format("2006-01")
	}
	if _, err := time.Parse("2006-01", month); err != nil {
		writeJSON(w, 400, map[string]string{"error": "Expected month as YYYY-MM"})
		return
	}
	report, err := h.tokenUsage.report(usage.Dir(), month, now)
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "Could not read usage history"})
		return
	}
	writeJSON(w, 200, report)
}

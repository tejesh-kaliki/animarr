package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"animarr/internal/store"
)

type encodeRow struct {
	ID      int64
	ShowID  int64
	Episode int
	Title   string
	State   string
	Badge   string
	Path    string
	Live    string
	Sizes   string
	Saved   string
	Error   string
	Encoder string
}

func (s *Server) encodePage(w http.ResponseWriter, r *http.Request) {
	v := s.encodeView(r)
	s.page(w, v)
}

func (s *Server) encodeTable(w http.ResponseWriter, r *http.Request) {
	v := s.encodeView(r)
	s.render(w, "encode-table", v)
}

func (s *Server) encodeView(r *http.Request) view {
	v := view{Title: "Encode", Nav: "encode", Body: "encode"}
	switch {
	case r.URL.Query().Get("err") == "disabled":
		v.Error = "Encode is off. Turn on ENCODE_ENABLED in Settings, then queue files."
	case r.URL.Query().Get("queued") != "":
		v.Flash = "Queued " + r.URL.Query().Get("queued") + " file(s). Done, skipped, pending, and running rows were left as they are."
	case r.URL.Query().Get("requeued") != "":
		v.Flash = "Requeued " + r.URL.Query().Get("requeued") + " failed file(s)."
	}
	cfg := s.current(r)
	v.EncodeOn = cfg.EncodeEnabled
	if s.Store == nil {
		v.Error = "database is not open"
		return v
	}
	counts, err := s.Store.CountEncodes(r.Context())
	if err != nil {
		v.Error = err.Error()
		return v
	}
	v.EncodeSummary = encodeSummary(counts)
	jobs, err := s.Store.ListEncodes(r.Context(), 200)
	if err != nil {
		v.Error = err.Error()
		return v
	}
	v.EncodeRows = make([]encodeRow, 0, len(jobs))
	for _, job := range jobs {
		v.EncodeRows = append(v.EncodeRows, presentEncode(job))
	}
	return v
}

func encodeSummary(counts map[string]int) string {
	return fmt.Sprintf("pending %d · running %d · done %d · skipped %d · failed %d",
		counts[store.EncodePending], counts[store.EncodeRunning], counts[store.EncodeDone],
		counts[store.EncodeSkipped], counts[store.EncodeFailed])
}

func presentEncode(job store.EncodeJob) encodeRow {
	row := encodeRow{
		ID: job.ID, ShowID: job.ShowID, Episode: job.Episode, Title: job.Title,
		State: job.State, Badge: encodeBadge(job.State), Path: job.FilePath,
		Error: job.Error, Encoder: job.Encoder,
	}
	if job.State == store.EncodeRunning && (job.Progress > 0 || job.FPS > 0 || job.Speed > 0) {
		row.Live = fmt.Sprintf("%.0f%% · %.0f fps · %.1fx", job.Progress, job.FPS, job.Speed)
	}
	if job.SrcSize > 0 && job.DstSize > 0 {
		row.Sizes = humanBytes(job.SrcSize) + " → " + humanBytes(job.DstSize)
		if job.DstSize < job.SrcSize {
			row.Saved = fmt.Sprintf("%.0f%%", float64(job.SrcSize-job.DstSize)/float64(job.SrcSize)*100)
		}
	}
	return row
}

func encodeBadge(state string) string {
	switch state {
	case store.EncodeRunning:
		return "downloading"
	case store.EncodeFailed:
		return "error"
	case store.EncodeDone:
		return "downloaded"
	case store.EncodeSkipped:
		return "ignored"
	default:
		return "wanted"
	}
}

func humanBytes(n int64) string {
	if n < 0 {
		n = 0
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit && exp < 4; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGT"[exp])
}

func (s *Server) postEncodeEnqueue(w http.ResponseWriter, r *http.Request) {
	if s.Store == nil {
		s.fail(w, r, errNoPipe, http.StatusInternalServerError)
		return
	}
	if !s.current(r).EncodeEnabled {
		http.Redirect(w, r, "/encode?err=disabled", http.StatusSeeOther)
		return
	}
	n, err := s.Store.EnqueueDownloaded(r.Context(), time.Now())
	if err != nil {
		s.fail(w, r, err, http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/encode?queued="+strconv.Itoa(n), http.StatusSeeOther)
}

func (s *Server) postEncodeRequeue(w http.ResponseWriter, r *http.Request) {
	if s.Store == nil {
		s.fail(w, r, errNoPipe, http.StatusInternalServerError)
		return
	}
	if !s.current(r).EncodeEnabled {
		http.Redirect(w, r, "/encode?err=disabled", http.StatusSeeOther)
		return
	}
	n, err := s.Store.RequeueFailedEncodes(r.Context(), time.Now())
	if err != nil {
		s.fail(w, r, err, http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/encode?requeued="+strconv.Itoa(n), http.StatusSeeOther)
}

func (s *Server) encodeNext(w http.ResponseWriter, r *http.Request) {
	if s.Store == nil || !s.current(r).EncodeEnabled {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	job, ok, err := s.Store.ClaimEncode(r.Context(), time.Now(), store.EncodeLease)
	if err != nil {
		s.fail(w, r, err, http.StatusInternalServerError)
		return
	}
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":           job.ID,
		"episodeId":    job.EpisodeID,
		"filePath":     job.FilePath,
		"leaseSeconds": int(store.EncodeLease / time.Second),
	})
}

func (s *Server) encodeHeartbeat(w http.ResponseWriter, r *http.Request) {
	id, ok := encodeID(w, r)
	if !ok {
		return
	}
	var body struct {
		Encoder string  `json:"encoder"`
		Percent float64 `json:"percent"`
		FPS     float64 `json:"fps"`
		Speed   float64 `json:"speed"`
	}
	if err := readJSON(r, &body); err != nil {
		s.fail(w, r, err, http.StatusBadRequest)
		return
	}
	hit, err := s.Store.HeartbeatEncode(r.Context(), id, store.EncodeProgress{
		Encoder: body.Encoder, Progress: body.Percent, FPS: body.FPS, Speed: body.Speed,
	}, time.Now(), store.EncodeLease)
	if err != nil {
		s.fail(w, r, err, http.StatusInternalServerError)
		return
	}
	if !hit {
		http.NotFound(w, r)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) encodeDone(w http.ResponseWriter, r *http.Request) {
	id, ok := encodeID(w, r)
	if !ok {
		return
	}
	var body struct {
		FilePath string  `json:"filePath"`
		Codec    string  `json:"codec"`
		Encoder  string  `json:"encoder"`
		SrcSize  int64   `json:"srcSize"`
		DstSize  int64   `json:"dstSize"`
		Skipped  bool    `json:"skipped"`
		Speed    float64 `json:"speed"`
		Error    string  `json:"error"`
	}
	if err := readJSON(r, &body); err != nil {
		s.fail(w, r, err, http.StatusBadRequest)
		return
	}
	state := store.EncodeDone
	if body.Skipped {
		state = store.EncodeSkipped
	}
	err := s.Store.FinishEncode(r.Context(), id, store.EncodeFinish{
		State: state, FilePath: body.FilePath, Codec: body.Codec, Encoder: body.Encoder,
		SrcSize: body.SrcSize, DstSize: body.DstSize, Error: body.Error, Speed: body.Speed,
	}, time.Now())
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, r, err, http.StatusInternalServerError)
		return
	}
	if state == store.EncodeDone && s.Pipe != nil {
		if err := s.Pipe.RefreshLibrary(r.Context()); err != nil {
			s.log().Warn("jellyfin refresh", "err", err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) encodeFail(w http.ResponseWriter, r *http.Request) {
	id, ok := encodeID(w, r)
	if !ok {
		return
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := readJSON(r, &body); err != nil {
		s.fail(w, r, err, http.StatusBadRequest)
		return
	}
	if body.Error == "" {
		body.Error = "encode failed"
	}
	err := s.Store.FinishEncode(r.Context(), id, store.EncodeFinish{State: store.EncodeFailed, Error: body.Error}, time.Now())
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, r, err, http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) encodeRelease(w http.ResponseWriter, r *http.Request) {
	id, ok := encodeID(w, r)
	if !ok {
		return
	}
	hit, err := s.Store.ReleaseEncode(r.Context(), id, time.Now())
	if err != nil {
		s.fail(w, r, err, http.StatusInternalServerError)
		return
	}
	if !hit {
		http.NotFound(w, r)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func encodeID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "bad id", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

func readJSON(r *http.Request, dest any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(dest)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

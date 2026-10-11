package playback

import (
	"errors"
	"time"

	pwclient "github.com/uidbz/pwplay/client"
	pwplayer "github.com/uidbz/pwplay/player"
)

// PlayRecord is one finished play reported by a backend's play log: the
// queue entry heard (a stream URL), when, and for how long. Classifying it
// (complete / skip) is the consumer's business.
type PlayRecord struct {
	Seq       uint64
	URL       string
	StartedAt time.Time
	Listened  float64 // seconds actually heard (seeks and pauses excluded)
	Duration  float64 // the track's length, 0 = unknown
	Position  float64 // where playback left the track
}

// History is a backend's play log since a sequence number. Sequence numbers
// are unique per Source, so (Source, Seq) identifies a play across restarts
// and re-deliveries.
type History struct {
	Source  string
	LastSeq uint64
	Records []PlayRecord
}

// ErrNoHistory reports a backend (an older pwplay-server) without a play log.
var ErrNoHistory = errors.New("playback backend keeps no play log")

// HistorySource is implemented by backends that log finished plays: both the
// pwplay remote (GET /history — the server keeps playing, and logging, while
// tie-audio is closed) and the local engine. Optional, so fakes and older
// implementations need not provide it.
type HistorySource interface {
	History(since uint64) (History, error)
}

func (r *pwplayRemote) History(since uint64) (History, error) {
	h, err := r.c.History(since)
	if errors.Is(err, pwclient.ErrNoHistory) {
		return History{}, ErrNoHistory
	}
	if err != nil {
		return History{}, err
	}
	out := History{Source: h.Source, LastSeq: h.LastSeq, Records: make([]PlayRecord, 0, len(h.Records))}
	for _, rec := range h.Records {
		out.Records = append(out.Records, PlayRecord{
			Seq: rec.Seq, URL: rec.URL, StartedAt: rec.StartedAt,
			Listened: rec.Listened, Duration: rec.Duration, Position: rec.Position,
		})
	}
	return out, nil
}

func (b *localBackend) History(since uint64) (History, error) {
	if b.plays == nil {
		return History{}, ErrNoHistory
	}
	recs := b.plays.Since(since)
	out := History{Source: b.plays.Source(), LastSeq: b.plays.LastSeq(), Records: make([]PlayRecord, 0, len(recs))}
	for _, rec := range recs {
		out.Records = append(out.Records, fromEngineRecord(rec))
	}
	return out, nil
}

func fromEngineRecord(rec pwplayer.PlayRecord) PlayRecord {
	return PlayRecord{
		Seq: rec.Seq, URL: rec.URL, StartedAt: rec.StartedAt,
		Listened: rec.Listened, Duration: rec.Duration, Position: rec.Position,
	}
}

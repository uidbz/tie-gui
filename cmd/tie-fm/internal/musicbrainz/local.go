package musicbrainz

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
)

// localProvider reads the full MusicBrainz PostgreSQL database (schema
// "musicbrainz" plus "cover_art_archive").
type localProvider struct {
	db        *sql.DB
	coverBase string
	timeout   time.Duration
}

func openLocal(s Settings) (Provider, error) {
	if strings.TrimSpace(s.DSN) == "" {
		return nil, fmt.Errorf("musicbrainz: empty local database DSN")
	}
	db, err := sql.Open("postgres", s.DSN)
	if err != nil {
		return nil, fmt.Errorf("musicbrainz: open local database: %w", err)
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxIdleTime(5 * time.Minute)
	return &localProvider{db: db, coverBase: s.CoverArtBaseURL, timeout: 30 * time.Second}, nil
}

func (l *localProvider) Kind() string { return SourceLocal }

func (l *localProvider) Close() error { return l.db.Close() }

func (l *localProvider) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var n int
	return l.db.QueryRowContext(ctx, `SELECT count(*) FROM musicbrainz.release LIMIT 1`).Scan(&n)
}

func (l *localProvider) queryContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, l.timeout)
}

// resolveArtists finds artist ids by name: exact, alias, prefix, then contains.
// It returns as soon as a stage finds candidates, so the common exact-name case
// stays index-only.
func (l *localProvider) resolveArtists(ctx context.Context, name string) ([]int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil
	}
	stages := []struct {
		query string
		arg   string
	}{
		{`SELECT id FROM musicbrainz.artist WHERE name = $1 ORDER BY edits_pending, id LIMIT 25`, name},
		{`SELECT artist FROM musicbrainz.artist_alias WHERE name = $1 LIMIT 25`, name},
		{`SELECT id FROM musicbrainz.artist WHERE name ILIKE $1 || '%' ORDER BY length(name), id LIMIT 25`, name},
		{`SELECT id FROM musicbrainz.artist WHERE name ILIKE '%' || $1 || '%' ORDER BY length(name), id LIMIT 25`, name},
	}
	for _, st := range stages {
		rows, err := l.db.QueryContext(ctx, st.query, st.arg)
		if err != nil {
			return nil, err
		}
		var ids []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
		if len(ids) > 0 {
			return dedupeIDs(ids), nil
		}
	}
	return nil, nil
}

func dedupeIDs(ids []int64) []int64 {
	seen := make(map[int64]bool, len(ids))
	out := ids[:0]
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

const releaseSelect = `
SELECT r.id,
       r.gid::text,
       r.name,
       ac.name,
       COALESCE(ar.gid, ''),
       COALESCE(rg.gid::text, ''),
       COALESCE(rg.name, ''),
       COALESCE(rgpt.name, ''),
       COALESCE(rs.name, ''),
       COALESCE(rd.year, 0),
       COALESCE(mt.tracks, 0),
       COALESCE(mt.discs, 0),
       COALESCE(st.secondary, ''),
       EXISTS (
           SELECT 1
             FROM cover_art_archive.cover_art ca
             JOIN cover_art_archive.cover_art_type cat ON cat.id = ca.id
            WHERE ca.release = r.id AND cat.type_id = 1
       )
  FROM musicbrainz.release r
  JOIN musicbrainz.artist_credit ac ON ac.id = r.artist_credit
  JOIN musicbrainz.release_group rg ON rg.id = r.release_group
  LEFT JOIN musicbrainz.release_group_primary_type rgpt ON rgpt.id = rg.type
  LEFT JOIN musicbrainz.release_status rs ON rs.id = r.status
  LEFT JOIN LATERAL (
      SELECT a.gid::text AS gid
        FROM musicbrainz.artist_credit_name acn
        JOIN musicbrainz.artist a ON a.id = acn.artist
       WHERE acn.artist_credit = r.artist_credit
       ORDER BY acn.position
       LIMIT 1
  ) ar ON true
  LEFT JOIN LATERAL (
      SELECT date_year AS year
        FROM musicbrainz.release_country
       WHERE release = r.id AND date_year IS NOT NULL
      UNION ALL
      SELECT date_year
        FROM musicbrainz.release_unknown_country
       WHERE release = r.id AND date_year IS NOT NULL
       ORDER BY year
       LIMIT 1
  ) rd ON true
  LEFT JOIN LATERAL (
      SELECT sum(track_count)::int AS tracks, count(*)::int AS discs
        FROM musicbrainz.medium
       WHERE release = r.id
  ) mt ON true
  LEFT JOIN LATERAL (
      SELECT string_agg(t.name, ', ' ORDER BY t.name) AS secondary
        FROM musicbrainz.release_group_secondary_type_join j
        JOIN musicbrainz.release_group_secondary_type t ON t.id = j.secondary_type
       WHERE j.release_group = rg.id
  ) st ON true`

func scanReleaseRows(rows *sql.Rows) ([]Release, error) {
	var out []Release
	for rows.Next() {
		var (
			id         int64
			r          Release
			artistMBID string
			rgTitle    string
			primary    string
			status     string
			secondary  string
		)
		err := rows.Scan(
			&id, &r.MBID, &r.Title, &r.ArtistCredit, &artistMBID,
			&r.ReleaseGroupMBID, &rgTitle, &primary, &status,
			&r.Year, &r.TrackCount, &r.DiscCount, &secondary, &r.HasCoverArt,
		)
		if err != nil {
			return nil, err
		}
		r.Artist = Artist{MBID: artistMBID, Name: r.ArtistCredit}
		r.ReleaseGroupTitle = rgTitle
		r.PrimaryType = primary
		r.Status = status
		if secondary != "" {
			r.SecondaryTypes = strings.Split(secondary, ", ")
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// matchStage controls how a title is matched. Exact/prefix stages can use the
// MusicBrainz btree name indexes; contains is the fuzzy fallback.
type matchStage int

const (
	matchAny matchStage = iota
	matchExact
	matchPrefix
	matchContains
)

func (l *localProvider) SearchReleases(ctx context.Context, q ReleaseQuery) ([]Release, error) {
	ctx, cancel := l.queryContext(ctx)
	defer cancel()

	artistIDs, err := l.resolveArtists(ctx, q.Artist)
	if err != nil {
		return nil, err
	}
	album := strings.TrimSpace(q.Album)
	if len(artistIDs) == 0 && album == "" {
		return nil, fmt.Errorf("musicbrainz: empty release query")
	}

	if len(artistIDs) > 0 {
		stages := []matchStage{matchAny}
		if album != "" {
			stages = []matchStage{matchExact, matchPrefix, matchAny}
		}
		for _, stage := range stages {
			rels, err := l.queryReleases(ctx, artistIDs, album, stage, 1000)
			if err != nil {
				return nil, err
			}
			if len(rels) > 0 {
				return RankReleases(q, rels), nil
			}
		}
		return nil, nil
	}

	for _, stage := range []matchStage{matchExact, matchPrefix, matchContains} {
		rels, err := l.queryReleases(ctx, nil, album, stage, 100)
		if err != nil {
			return nil, err
		}
		if len(rels) > 0 {
			return RankReleases(q, rels), nil
		}
	}
	return nil, nil
}

// queryReleases runs one staged release query. Artist filtering uses the
// artist_credit_name index; the title predicate stays index-friendly for the
// exact/prefix stages, and only the fuzzy fallback scans names.
func (l *localProvider) queryReleases(ctx context.Context, artistIDs []int64, album string, stage matchStage, limit int) ([]Release, error) {
	var (
		args  []any
		conds []string
	)
	if len(artistIDs) > 0 {
		args = append(args, pq.Array(artistIDs))
		conds = append(conds, fmt.Sprintf(
			`r.artist_credit IN (SELECT artist_credit FROM musicbrainz.artist_credit_name WHERE artist = ANY($%d))`,
			len(args)))
	}
	if stage != matchAny {
		args = append(args, album)
		ph := fmt.Sprintf("$%d", len(args))
		switch stage {
		case matchExact:
			conds = append(conds, "r.name = "+ph)
		case matchPrefix:
			conds = append(conds, "r.name LIKE "+ph+" || '%'")
		case matchContains:
			conds = append(conds, "r.name ILIKE '%' || "+ph+" || '%'")
		}
	}
	where := "TRUE"
	if len(conds) > 0 {
		where = strings.Join(conds, " AND ")
	}
	query := releaseSelect + "\n WHERE " + where +
		"\n ORDER BY length(r.name), r.name\n LIMIT " + strconv.Itoa(limit)
	rows, err := l.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanReleaseRows(rows)
}

func (l *localProvider) LookupRelease(ctx context.Context, mbid string) (*Release, error) {
	mbid = strings.TrimSpace(mbid)
	if mbid == "" {
		return nil, fmt.Errorf("musicbrainz: empty release MBID")
	}
	ctx, cancel := l.queryContext(ctx)
	defer cancel()

	query := releaseSelect + "\n WHERE r.gid = $1::uuid"
	rows, err := l.db.QueryContext(ctx, query, mbid)
	if err != nil {
		return nil, err
	}
	releases, err := scanReleaseRows(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(releases) == 0 {
		return nil, fmt.Errorf("musicbrainz: release %s not found", mbid)
	}
	rel := &releases[0]

	var releaseID int64
	var rgID int64
	if err := l.db.QueryRowContext(ctx,
		`SELECT r.id, r.release_group FROM musicbrainz.release r WHERE r.gid = $1::uuid`, mbid,
	).Scan(&releaseID, &rgID); err != nil {
		return nil, err
	}

	if err := l.loadMediums(ctx, releaseID, rel); err != nil {
		return nil, err
	}
	rel.Genres, _ = l.loadGenres(ctx, releaseID, rgID)
	rel.Tags, _ = l.loadTags(ctx, releaseID, rgID)

	// Some releases have art only on the release group; mirror that signal.
	if !rel.HasCoverArt {
		var has bool
		_ = l.db.QueryRowContext(ctx, `
            SELECT EXISTS (
                SELECT 1 FROM cover_art_archive.release_group_cover_art
                 WHERE release_group = $1
            )`, rgID).Scan(&has)
		if has {
			var rgRelease string
			if err := l.db.QueryRowContext(ctx, `
                SELECT r.gid::text
                  FROM cover_art_archive.release_group_cover_art rgca
                  JOIN musicbrainz.release r ON r.id = rgca.release
                 WHERE rgca.release_group = $1
                 LIMIT 1`, rgID).Scan(&rgRelease); err == nil && rgRelease != "" {
				// Keep HasCoverArt true and point the URL at that release.
				rel.HasCoverArt = true
				if rel.CoverArtMBID == "" {
					rel.CoverArtMBID = rgRelease
				}
			}
		}
	}
	return rel, nil
}

func (l *localProvider) loadMediums(ctx context.Context, releaseID int64, rel *Release) error {
	rows, err := l.db.QueryContext(ctx, `
        SELECT m.position, COALESCE(mf.name, ''), m.track_count,
               t.id, t.gid::text, t.position, t.number, t.name,
               COALESCE(t.length, 0), rec.gid::text, rec.name,
               COALESCE(rac.name, '')
          FROM musicbrainz.medium m
          LEFT JOIN musicbrainz.medium_format mf ON mf.id = m.format
          JOIN musicbrainz.track t ON t.medium = m.id
          JOIN musicbrainz.recording rec ON rec.id = t.recording
          LEFT JOIN musicbrainz.artist_credit rac ON rac.id = t.artist_credit
         WHERE m.release = $1
         ORDER BY m.position, t.position`, releaseID)
	if err != nil {
		return err
	}
	defer rows.Close()

	byPos := map[int]int{}
	for rows.Next() {
		var (
			mediumPos, mediumTracks int
			format                  string
			trackID                 int64
			tr                      Track
			recordingTitle          string
		)
		if err := rows.Scan(&mediumPos, &format, &mediumTracks,
			&trackID, &tr.MBID, &tr.Position, &tr.Number, &tr.Title,
			&tr.LengthMS, &tr.RecordingMBID, &recordingTitle, &tr.ArtistCredit,
		); err != nil {
			return err
		}
		idx, ok := byPos[mediumPos]
		if !ok {
			idx = len(rel.Mediums)
			byPos[mediumPos] = idx
			rel.Mediums = append(rel.Mediums, Medium{Position: mediumPos, Format: format})
		}
		tr.Disc = mediumPos
		tr.Artist = tr.ArtistCredit
		rel.Mediums[idx].Tracks = append(rel.Mediums[idx].Tracks, tr)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rel.TrackCount = 0
	for i := range rel.Mediums {
		rel.Mediums[i].TrackCount = len(rel.Mediums[i].Tracks)
		rel.TrackCount += rel.Mediums[i].TrackCount
	}
	rel.DiscCount = len(rel.Mediums)
	return nil
}

func (l *localProvider) loadGenres(ctx context.Context, releaseID, rgID int64) ([]string, error) {
	rows, err := l.db.QueryContext(ctx, `
        SELECT g.name
          FROM (
              SELECT genre, count FROM musicbrainz.genre_release WHERE release = $1
              UNION ALL
              SELECT genre, count FROM musicbrainz.genre_release_group WHERE release_group = $2
          ) x
          JOIN musicbrainz.genre g ON g.id = x.genre
         GROUP BY g.name
         ORDER BY sum(x.count) DESC, g.name
         LIMIT 10`, releaseID, rgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

func (l *localProvider) loadTags(ctx context.Context, releaseID, rgID int64) ([]string, error) {
	rows, err := l.db.QueryContext(ctx, `
        SELECT t.name
          FROM (
              SELECT tag, count FROM musicbrainz.release_tag WHERE release = $1 AND count > 0
              UNION ALL
              SELECT tag, count FROM musicbrainz.release_group_tag WHERE release_group = $2 AND count > 0
          ) x
          JOIN musicbrainz.tag t ON t.id = x.tag
         GROUP BY t.name
         ORDER BY sum(x.count) DESC, t.name
         LIMIT 10`, releaseID, rgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

func (l *localProvider) SearchRecordings(ctx context.Context, q RecordingQuery) ([]Recording, error) {
	ctx, cancel := l.queryContext(ctx)
	defer cancel()

	title := strings.TrimSpace(q.Title)
	artistIDs, err := l.resolveArtists(ctx, q.Artist)
	if err != nil {
		return nil, err
	}
	if title == "" && len(artistIDs) == 0 {
		return nil, fmt.Errorf("musicbrainz: empty recording query")
	}

	stages := []matchStage{matchAny}
	if title != "" {
		stages = []matchStage{matchExact, matchPrefix, matchContains}
	}
	type rowRec struct {
		id  int64
		rec Recording
	}
	var found []rowRec
	for _, stage := range stages {
		rows, err := l.queryRecordings(ctx, artistIDs, title, stage, 300)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var rr rowRec
			if err := rows.Scan(&rr.id, &rr.rec.MBID, &rr.rec.Title, &rr.rec.ArtistCredit,
				&rr.rec.LengthMS, &rr.rec.ArtistMBID); err != nil {
				rows.Close()
				return nil, err
			}
			rr.rec.Artist = rr.rec.ArtistCredit
			found = append(found, rr)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		if len(found) > 0 {
			break
		}
	}

	recs := make([]Recording, 0, len(found))
	idByMBID := make(map[string]int64, len(found))
	for _, rr := range found {
		recs = append(recs, rr.rec)
		idByMBID[rr.rec.MBID] = rr.id
	}
	recs = RankRecordings(q, recs)
	if len(recs) > 10 {
		recs = recs[:10]
	}
	if len(recs) > 0 {
		ids := make([]int64, 0, len(recs))
		for _, rec := range recs {
			ids = append(ids, idByMBID[rec.MBID])
		}
		l.attachRecordingReleases(ctx, recs, ids)
	}
	return recs, nil
}

// queryRecordings runs one staged recording query. See queryReleases.
func (l *localProvider) queryRecordings(ctx context.Context, artistIDs []int64, title string, stage matchStage, limit int) (*sql.Rows, error) {
	var (
		args  []any
		conds []string
	)
	if len(artistIDs) > 0 {
		args = append(args, pq.Array(artistIDs))
		conds = append(conds, fmt.Sprintf(
			`rec.artist_credit IN (SELECT artist_credit FROM musicbrainz.artist_credit_name WHERE artist = ANY($%d))`,
			len(args)))
	}
	if stage != matchAny {
		args = append(args, title)
		ph := fmt.Sprintf("$%d", len(args))
		switch stage {
		case matchExact:
			conds = append(conds, "rec.name = "+ph)
		case matchPrefix:
			conds = append(conds, "rec.name LIKE "+ph+" || '%'")
		case matchContains:
			conds = append(conds, "rec.name ILIKE '%' || "+ph+" || '%'")
		}
	}
	where := "TRUE"
	if len(conds) > 0 {
		where = strings.Join(conds, " AND ")
	}
	return l.db.QueryContext(ctx, `
        SELECT rec.id, rec.gid::text, rec.name, COALESCE(ac.name, ''),
               COALESCE(rec.length, 0), COALESCE(ar.gid::text, '')
          FROM musicbrainz.recording rec
          LEFT JOIN musicbrainz.artist_credit ac ON ac.id = rec.artist_credit
          LEFT JOIN LATERAL (
              SELECT a.gid::text AS gid
                FROM musicbrainz.artist_credit_name acn
                JOIN musicbrainz.artist a ON a.id = acn.artist
               WHERE acn.artist_credit = rec.artist_credit
               ORDER BY acn.position
               LIMIT 1
          ) ar ON true
         WHERE `+where+`
         ORDER BY length(rec.name), rec.name
         LIMIT `+strconv.Itoa(limit), args...)
}

// attachRecordingReleases adds one release reference per recording so the UI
// can suggest the containing album.
func (l *localProvider) attachRecordingReleases(ctx context.Context, recs []Recording, ids []int64) {
	if len(ids) == 0 {
		return
	}
	rows, err := l.db.QueryContext(ctx, `
        SELECT DISTINCT ON (t.recording)
               t.recording, r.gid::text, r.name,
               COALESCE((SELECT date_year FROM musicbrainz.release_country rc
                          WHERE rc.release = r.id AND rc.date_year IS NOT NULL
                          ORDER BY date_year LIMIT 1), 0)
          FROM musicbrainz.track t
          JOIN musicbrainz.medium m ON m.id = t.medium
          JOIN musicbrainz.release r ON r.id = m.release
         WHERE t.recording = ANY($1)
         ORDER BY t.recording, r.name`, pq.Array(ids))
	if err != nil {
		return
	}
	defer rows.Close()
	byID := make(map[int64]*Recording, len(recs))
	for i := range recs {
		byID[ids[i]] = &recs[i]
	}
	for rows.Next() {
		var id int64
		var rel RecordingRelease
		if err := rows.Scan(&id, &rel.MBID, &rel.Title, &rel.Year); err != nil {
			return
		}
		if rec, ok := byID[id]; ok && len(rec.Releases) == 0 {
			rec.Releases = append(rec.Releases, rel)
		}
	}
}

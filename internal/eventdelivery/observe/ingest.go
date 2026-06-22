package observe

import (
	"context"
	"database/sql"
	"fmt"
)

// IngestResult summarises one ingestion batch.
type IngestResult struct {
	Inserted int // new raw_observations rows
	Deduped  int // observations that already existed (same EventID / unique key)
}

// Ingest writes observations into the session store's raw_observations table,
// keyed on the deterministic EventID. Re-ingesting the same observation is a
// no-op (INSERT OR IGNORE on the primary key and the component UNIQUE
// constraint), so re-polling an unchanged artifact or re-importing an unread
// thread does not create duplicate rows. The run must already exist
// (store.StartRun).
func Ingest(ctx context.Context, db *sql.DB, runID string, obs []Observation) (IngestResult, error) {
	var res IngestResult
	for _, ob := range obs {
		var threadID sql.NullString
		if ob.NotificationThreadID != "" {
			threadID = sql.NullString{String: ob.NotificationThreadID, Valid: true}
		}
		r, err := db.ExecContext(ctx, `
INSERT OR IGNORE INTO raw_observations
  (id, run_id, source, repo, artifact_kind, artifact_number, notification_thread_id, event_kind, event_key, event_class, source_query, payload_json)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			ob.EventID(), runID, ob.Source, ob.Repo, ob.ArtifactKind, ob.ArtifactNumber,
			threadID, ob.EventKind, ob.EventKey, ob.EventClass, ob.SourceQuery, ob.PayloadJSON)
		if err != nil {
			return res, fmt.Errorf("ingest %s: %w", ob.EventID(), err)
		}
		n, err := r.RowsAffected()
		if err != nil {
			return res, fmt.Errorf("ingest %s rows affected: %w", ob.EventID(), err)
		}
		if n == 1 {
			res.Inserted++
		} else {
			res.Deduped++
		}
	}
	return res, nil
}

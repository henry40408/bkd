// Package backgroundcheck checks, on a timer, the bookmarks of users who turned
// it on.
//
// It uses the same checker as the "Check links" button, so the same rules keep
// it off private addresses. On top of that it is gentle: a bounded batch per
// round, one request at a time per site with a pause between, and a site that
// asks to be left alone (429, 503) or stops answering is skipped for a while.
// The wait is what the site asked for, or else doubles with each time in a row
// and starts over after an answer.
package backgroundcheck

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/henry40408/bkd/internal/linkcheck"
	"github.com/henry40408/bkd/internal/users"
)

const (
	// recheckAfter: a result younger than this is not asked for again.
	recheckAfter = 7 * 24 * time.Hour
	// overfetch is the rows read per bookmark wanted, so bookmarks of sites
	// that are cooling down can be skipped.
	overfetch = 5
	// sameHostPause is between two requests to one site. Longer than the
	// manual check, which is quick on purpose.
	sameHostPause = 1500 * time.Millisecond
	// baseCooldown is the first wait for a site that did not say how long.
	// Each time in a row doubles it.
	baseCooldown = time.Minute
	// maxCooldown is also the longest a site can ask for: a Retry-After of a
	// week is not believed.
	maxCooldown = 24 * time.Hour
	// forgetAfter is how long after a wait ends a site is still remembered, so
	// that asking again soon doubles the wait. After this, a site is treated as new.
	forgetAfter  = 24 * time.Hour
	maxDoublings = 20
)

// Config says what to check and how often.
type Config struct {
	DB      *sql.DB
	Checker *linkcheck.Checker
	// Interval is the time between the starts of two rounds.
	Interval time.Duration
	// Batch is the bookmarks checked per round, all users together.
	Batch  int
	Logger *slog.Logger
	// Now is the clock; nil means time.Now. A nil Logger discards.
	Now func() time.Time
}

// Run does rounds until ctx ends. A slow round is not followed by a burst of
// rounds to catch up.
func Run(ctx context.Context, config Config) {
	worker := newWorker(config)
	config = worker.config
	ticker := time.NewTicker(config.Interval)
	defer ticker.Stop()
	for {
		worker.logRound(ctx)
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return
		}
	}
}

// worker holds what is remembered between rounds.
type worker struct {
	config    Config
	cooldowns map[string]cooldown
}

// cooldown is what is known about a site that asked to be left alone.
type cooldown struct {
	// strikes is the times in a row it asked. Starts over when the site answers normally.
	strikes int
	until   time.Time
}

func newWorker(config Config) *worker {
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Logger == nil {
		config.Logger = slog.New(slog.DiscardHandler)
	}
	return &worker{config: config, cooldowns: make(map[string]cooldown)}
}

func (w *worker) logRound(ctx context.Context) {
	count, err := w.runRound(ctx)
	switch {
	case ctx.Err() != nil:
	case err != nil:
		w.config.Logger.Error("background link check failed", "error", err)
	case count > 0:
		w.config.Logger.Info("background link check finished a round", "count", count)
	}
}

// cooldownFor is how long to leave a site alone after its strikes-th request
// in a row to be.
func cooldownFor(strikes int, retryAfter *time.Duration) time.Duration {
	if retryAfter != nil {
		return min(*retryAfter, maxCooldown)
	}
	doublings := min(max(strikes-1, 0), maxDoublings)
	return min(baseCooldown<<doublings, maxCooldown)
}

// due is a bookmark that is time to check.
type due struct {
	id     int64
	userID int64
	url    string
}

// runRound checks the next batch and returns how many bookmarks got a result.
func (w *worker) runRound(ctx context.Context) (int, error) {
	batch, err := w.dueNow(ctx)
	if err != nil || len(batch) == 0 {
		return 0, err
	}
	owners := make(map[int64]int64, len(batch))
	targets := make([]linkcheck.Target, len(batch))
	for i, item := range batch {
		owners[item.id] = item.userID
		targets[i] = linkcheck.Target{ID: item.id, URL: item.url}
	}
	running := w.config.Checker.StartPaced(ctx, targets, sameHostPause)
	defer running.Cancel()
	stored := 0
	for event := range running.Events {
		// Skipped targets stay due, and a later round picks them up.
		if event.Checked == nil {
			continue
		}
		w.learnFrom(*event.Checked)
		written, err := w.store(ctx, owners[event.Checked.Target.ID], *event.Checked)
		if err != nil {
			return stored, err
		}
		stored += written
	}
	return stored, nil
}

func (w *worker) learnFrom(checked linkcheck.Checked) {
	switch {
	case checked.Backoff != nil:
		w.leaveAlone(*checked.Backoff)
	// Only a reply ends the waiting; a timeout says nothing about a site's mood.
	case checked.Answered:
		delete(w.cooldowns, linkcheck.HostOf(checked.Target.URL))
	}
}

func (w *worker) store(ctx context.Context, userID int64, checked linkcheck.Checked) (int, error) {
	// The user may have opted out, or been disabled, since the round began.
	wanted, err := users.WantsBackgroundCheck(ctx, w.config.DB, userID)
	if err != nil || !wanted {
		return 0, err
	}
	stored, err := linkcheck.NewStore(w.config.DB).Store(ctx, linkcheck.StoreRequest{
		UserID:  userID,
		Checked: []linkcheck.Checked{checked},
		Now:     w.config.Now().Unix(),
	})
	return len(stored), err
}

func (w *worker) leaveAlone(backoff linkcheck.Backoff) {
	known := w.cooldowns[backoff.Host]
	known.strikes++
	wait := cooldownFor(known.strikes, backoff.RetryAfter)
	known.until = w.config.Now().Add(wait)
	w.cooldowns[backoff.Host] = known
	w.config.Logger.Info("leaving a site alone for a while", "host", backoff.Host, "seconds", int64(wait.Seconds()))
}

// forgetOldCooldowns stops the map from keeping every site that was ever throttled.
func (w *worker) forgetOldCooldowns() {
	now := w.config.Now()
	for host, known := range w.cooldowns {
		if !known.until.Add(forgetAfter).After(now) {
			delete(w.cooldowns, host)
		}
	}
}

func (w *worker) isCooling(host string) bool {
	known, ok := w.cooldowns[host]
	return ok && known.until.After(w.config.Now())
}

// dueNow returns the bookmarks to check now, skipping sites that are cooling down.
func (w *worker) dueNow(ctx context.Context) ([]due, error) {
	w.forgetOldCooldowns()
	// The cooling sites are known only after the query, so read extra rows: a
	// site that fills the front of the line must not leave the batch empty.
	candidates, err := dueBookmarks(ctx, w.config.DB, w.config.Now(), w.config.Batch*overfetch)
	if err != nil {
		return nil, err
	}
	var ready []due
	for _, candidate := range candidates {
		if len(ready) == w.config.Batch {
			break
		}
		if !w.isCooling(linkcheck.HostOf(candidate.url)) {
			ready = append(ready, candidate)
		}
	}
	return ready, nil
}

// dueBookmarks lists never-checked bookmarks first, then the longest
// unchecked. Users take turns, so one person with thousands of bookmarks does
// not use up every round.
func dueBookmarks(ctx context.Context, pool *sql.DB, now time.Time, limit int) ([]due, error) {
	rows, err := pool.QueryContext(ctx,
		`SELECT id, user_id, url FROM (
		     SELECT b.id, b.user_id, b.url, b.link_checked_at,
		            ROW_NUMBER() OVER (
		                PARTITION BY b.user_id
		                ORDER BY COALESCE(b.link_checked_at, 0), b.id
		            ) AS turn
		     FROM bookmarks b
		     JOIN users u ON u.id = b.user_id
		     WHERE u.link_check_enabled = 1
		       AND u.disabled_at IS NULL
		       AND (b.link_checked_at IS NULL OR b.link_checked_at < ?1)
		 )
		 ORDER BY turn, COALESCE(link_checked_at, 0), id
		 LIMIT ?2`,
		now.Add(-recheckAfter).Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var found []due
	for rows.Next() {
		var item due
		if err := rows.Scan(&item.id, &item.userID, &item.url); err != nil {
			return nil, err
		}
		found = append(found, item)
	}
	return found, rows.Err()
}

package bookmarks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/henry40408/bkd/internal/db"
)

// SetSiteRequest is what a read of the page behind a bookmark's address found.
type SetSiteRequest struct {
	UserID int64
	ID     int64
	// URL is the address that was read. A bookmark whose URL is something else
	// by now was edited during the read, and what the old page said does not
	// describe it.
	URL  string
	Site Site
}

// SiteStored says what SetSite did.
type SiteStored struct {
	// Stored is false when the bookmark is gone, its URL changed since the page
	// was read, or the page said nothing.
	Stored bool
	// NoteFilled is true when the note was blank and now holds the description.
	NoteFilled bool
}

// SetSite keeps the details of a page that was read apart from the title and the
// note, and fills a blank note with the description. The title and a note that
// has text are never touched, nor is updated_at: reading a page is not an edit
// by the user, and a batch of reads must not reorder the list.
func (s *Store) SetSite(ctx context.Context, req SetSiteRequest) (SiteStored, error) {
	site := req.Site.Fitted()
	if site == (Site{}) {
		return SiteStored{}, nil
	}
	var stored SiteStored
	err := db.WithImmediateTx(ctx, s.db, func(conn *sql.Conn) error {
		stored = SiteStored{}
		var note string
		err := conn.QueryRowContext(ctx,
			"SELECT note FROM bookmarks WHERE id = ?1 AND user_id = ?2 AND url = ?3",
			req.ID, req.UserID, req.URL).Scan(&note)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read note: %w", err)
		}
		fillNote := note == "" && site.Description != ""
		_, err = conn.ExecContext(ctx,
			`UPDATE bookmarks SET site_title = NULLIF(?4, ''), site_description = NULLIF(?5, ''),
			        note = CASE WHEN ?6 THEN ?5 ELSE note END
			 WHERE id = ?1 AND user_id = ?2 AND url = ?3`,
			req.ID, req.UserID, req.URL, site.Title, site.Description, fillNote)
		if err != nil {
			return fmt.Errorf("store site details: %w", err)
		}
		stored = SiteStored{Stored: true, NoteFilled: fillNote}
		return nil
	})
	if err != nil {
		return SiteStored{}, err
	}
	return stored, nil
}

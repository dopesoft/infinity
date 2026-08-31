package dashboard

import (
	"context"
	"errors"
	"time"

	"github.com/dopesoft/infinity/core/internal/llm"
	"github.com/jackc/pgx/v5"
)

// Quote is the line under the dashboard greeting. One per day, the same one
// all day, the same one on every device.
type Quote struct {
	ID     string `json:"id"`
	Text   string `json:"text"`
	Author string `json:"author"`
	Source string `json:"source,omitempty"`
}

// ErrQuotePickFailed means the corpus has rows but no quote came back for
// today. That is a bug in the pick, not an empty result, and it is returned as
// an error on purpose: a caller that treated it as "no quote today" would turn
// a broken query into a silent blank line forever. Empty-because-broken must
// never read as empty-because-fine.
var ErrQuotePickFailed = errors.New("quote: corpus is populated but today's pick returned nothing")

// quoteDay is the boss's LOCAL day. llm.UserLocation is already the single
// source of truth for his clock frame (INFINITY_USER_TIMEZONE, default
// America/Chicago) and is what stamps <current_time> into the system prompt,
// so the quote turns over at the same midnight Jarvis thinks it is.
func quoteDay(now time.Time) string {
	return now.In(llm.UserLocation()).Format("2006-01-02")
}

// loadQuote resolves today's quote, assigning one on the first call of the day.
//
// Two statements rather than one upsert-returning, because ~100% of calls are
// the second and subsequent request of a day and those must not write a dead
// tuple just to read a row back.
//
// Returns (nil, nil) ONLY when the corpus is genuinely empty - the honest
// "nothing to show" case. Every other failure returns an error so it lands in
// the fan-out's warn log instead of disappearing.
func (a *API) loadQuote(ctx context.Context) (*Quote, error) {
	if a.Pool == nil {
		return nil, errors.New("no pool")
	}
	day := quoteDay(time.Now())

	// 1. Already assigned? Read it and leave.
	var q Quote
	err := a.Pool.QueryRow(ctx, `
		SELECT q.id::text, q.text, q.author, COALESCE(q.source, '')
		FROM mem_quote_days d
		JOIN mem_quotes q ON q.id = d.quote_id
		WHERE d.day = $1::date
	`, day).Scan(&q.ID, &q.Text, &q.Author, &q.Source)
	if err == nil {
		return &q, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	// 2. First request of the day: pick and stamp.
	//
	// ORDER BY max(day) ASC NULLS FIRST is the cycling rule - every quote in
	// the corpus is shown once before any is shown twice, and the pool then
	// rotates least-recently-shown. NULLS FIRST is explicit because Postgres
	// defaults ASC to NULLS LAST, which would show the same handful forever.
	// The md5 tiebreak keeps the choice deterministic instead of ordering by
	// physical row position.
	//
	// ON CONFLICT ... DO UPDATE, not DO NOTHING. Two tabs (or two replicas)
	// hitting a fresh day race here. With DO NOTHING the loser's CTE returns
	// zero rows AND its outer SELECT reads a snapshot taken before the winner
	// committed, so it gets nothing at all. DO UPDATE makes the loser block on
	// the winner's row lock and then return the winner's quote. Both see the
	// same line, which is the whole point of a daily quote.
	err = a.Pool.QueryRow(ctx, `
		WITH cand AS (
		    SELECT q.id
		      FROM mem_quotes q
		     WHERE q.active
		     ORDER BY (SELECT max(d.day) FROM mem_quote_days d WHERE d.quote_id = q.id)
		                 ASC NULLS FIRST,
		               md5(q.id::text || $2)
		     LIMIT 1
		), ins AS (
		    INSERT INTO mem_quote_days (day, quote_id)
		    SELECT $1::date, id FROM cand
		    ON CONFLICT (day) DO UPDATE SET day = EXCLUDED.day
		    RETURNING quote_id
		)
		SELECT q.id::text, q.text, q.author, COALESCE(q.source, '')
		  FROM mem_quotes q
		  JOIN ins ON ins.quote_id = q.id
	`, day, day).Scan(&q.ID, &q.Text, &q.Author, &q.Source)
	if err == nil {
		return &q, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	// No rows from the pick. Distinguish the two reasons, because they are
	// completely different problems and only one of them is fine.
	var any bool
	if probeErr := a.Pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM mem_quotes WHERE active)`).Scan(&any); probeErr != nil {
		return nil, probeErr
	}
	if !any {
		return nil, nil // corpus genuinely empty: nothing to show, nothing broken
	}
	return nil, ErrQuotePickFailed
}

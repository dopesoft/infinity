// google.go - Google Calendar v3 provider through Composio's native actions.
//
// Composio does NOT expose a generic /v3/tools/proxy passthrough (their
// docs hint at it but the endpoint 404s in production). They DO publish
// every Google Calendar v3 verb we need as a first-class action:
//
//   GOOGLECALENDAR_EVENTS_LIST    full or windowed pull
//   GOOGLECALENDAR_SYNC_EVENTS    incremental via syncToken
//   GOOGLECALENDAR_PATCH_EVENT    partial update (used for RSVP)
//   GOOGLECALENDAR_FIND_EVENT     single-event read (used pre-RSVP)
//   GOOGLECALENDAR_CREATE_EVENT   new event
//   GOOGLECALENDAR_DELETE_EVENT   cancel
//
// Each call returns Composio's standard envelope:
//
//   { "data": {...google response...}, "successful": true, "error": null }
//
// where `data` IS the Google API response verbatim (items + nextSyncToken
// + nextPageToken etc.), so our existing parseGoogleEvent decoder reads
// it unchanged. The boss's OAuth token never leaves Composio; the
// connected_account + entity (user_id) tuple identifies the grant.
//
// CRITICAL: every execute call MUST carry both `connected_account_id`
// AND `user_id` - Composio rejects with code 1811
// (ActionExecute_ConnectedAccountEntityIdRequired) otherwise. The
// userIDFor callback resolves account -> entity via the connectors cache.

package calendar

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/connectors"
)

const (
	providerSlug      = "google_calendar"
	defaultCalendarID = "primary"
	defaultPageSize   = 250

	actEventsList  = "GOOGLECALENDAR_EVENTS_LIST"
	actSyncEvents  = "GOOGLECALENDAR_SYNC_EVENTS"
	actPatchEvent  = "GOOGLECALENDAR_PATCH_EVENT"
	actFindEvent   = "GOOGLECALENDAR_FIND_EVENT"
	actCreateEvent = "GOOGLECALENDAR_CREATE_EVENT"
	actDeleteEvent = "GOOGLECALENDAR_DELETE_EVENT"
)

// GoogleProvider implements Provider against Google Calendar via Composio
// native actions. selfEmailFor returns the boss's email per account
// (needed when writing an RSVP). userIDFor returns the Composio entity_id
// per account (mandatory for every execute call). Both nil-safe: missing
// resolvers surface clear errors instead of silent failures.
type GoogleProvider struct {
	exec         *connectors.ExecuteClient
	selfEmailFor func(accountID string) string
	userIDFor    func(accountID string) string
}

// NewGoogleProvider builds the provider. exec is required; both callbacks
// may be nil (Respond / Execute return descriptive errors when called).
func NewGoogleProvider(
	exec *connectors.ExecuteClient,
	selfEmailFor func(accountID string) string,
	userIDFor func(accountID string) string,
) *GoogleProvider {
	return &GoogleProvider{
		exec:         exec,
		selfEmailFor: selfEmailFor,
		userIDFor:    userIDFor,
	}
}

func (g *GoogleProvider) Slug() string { return providerSlug }

// execute is the one place every Composio call goes through so we can
// guarantee user_id is attached. Returns the raw json envelope `data`
// field on success, or a descriptive error on failure.
func (g *GoogleProvider) execute(ctx context.Context, slug, accountID string, args map[string]any) (json.RawMessage, error) {
	if g.userIDFor == nil {
		return nil, fmt.Errorf("calendar: no user_id resolver wired for %s", slug)
	}
	entity := strings.TrimSpace(g.userIDFor(accountID))
	if entity == "" {
		return nil, fmt.Errorf("calendar: no entity/user_id known for account %s (Composio requires user_id with every execute)", accountID)
	}
	resp, err := g.exec.Execute(ctx, connectors.ExecuteRequest{
		Slug:               slug,
		ConnectedAccountID: accountID,
		UserID:             entity,
		Arguments:          args,
	})
	if err != nil {
		return nil, err
	}
	if !resp.Successful {
		return nil, fmt.Errorf("composio %s: %s", slug, resp.Error)
	}
	return resp.Data, nil
}

// List - one page of events. Routes to SYNC_EVENTS when SyncToken is
// set (incremental), EVENTS_LIST otherwise (full window). On a 410
// GONE Composio surfaces an error string containing "410" or "Sync
// token is no longer valid" - we detect either and return FullResync.
func (g *GoogleProvider) List(ctx context.Context, accountID string, opts ListOptions) (*ListPage, error) {
	calID := opts.CalendarID
	if calID == "" {
		calID = defaultCalendarID
	}
	pageSize := opts.MaxResults
	if pageSize <= 0 || pageSize > 2500 {
		pageSize = defaultPageSize
	}

	args := map[string]any{
		"calendarId":   calID,
		"maxResults":   pageSize,
		"singleEvents": true,
		"showDeleted":  true,
	}

	var slug string
	if opts.SyncToken != "" {
		slug = actSyncEvents
		args["syncToken"] = opts.SyncToken
	} else {
		slug = actEventsList
		tmin := opts.TimeMin
		if tmin.IsZero() {
			tmin = time.Now().AddDate(0, 0, -1)
		}
		tmax := opts.TimeMax
		if tmax.IsZero() {
			tmax = time.Now().AddDate(0, 6, 0)
		}
		args["timeMin"] = tmin.UTC().Format(time.RFC3339)
		args["timeMax"] = tmax.UTC().Format(time.RFC3339)
		args["orderBy"] = "startTime"
	}
	if opts.PageToken != "" {
		args["pageToken"] = opts.PageToken
	}

	data, err := g.execute(ctx, slug, accountID, args)
	if err != nil {
		// Detect sync-token expiry and trigger a full re-sync at the
		// next loop iteration. Google + Composio both put "410" or
		// "Sync token is no longer valid" in the error string.
		es := strings.ToLower(err.Error())
		if strings.Contains(es, "410") ||
			strings.Contains(es, "sync token") ||
			strings.Contains(es, "synctoken") {
			return &ListPage{FullResync: true}, nil
		}
		return nil, err
	}

	var raw struct {
		Items         []json.RawMessage `json:"items"`
		NextPageToken string            `json:"nextPageToken"`
		NextSyncToken string            `json:"nextSyncToken"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("google list decode: %w", err)
	}

	out := &ListPage{
		Events:    make([]Event, 0, len(raw.Items)),
		NextPage:  raw.NextPageToken,
		SyncToken: raw.NextSyncToken,
	}
	for _, item := range raw.Items {
		ev, perr := parseGoogleEvent(item, accountID, calID)
		if perr != nil {
			continue
		}
		out.Events = append(out.Events, ev)
	}
	return out, nil
}

// Respond patches the boss's attendee row. Composio's PATCH_EVENT accepts
// `attendees` as a top-level argument; we round-trip via FIND_EVENT so we
// don't clobber other invitees' responses.
func (g *GoogleProvider) Respond(ctx context.Context, accountID, calendarID, remoteID string, response Response) (*Event, error) {
	if !response.Valid() {
		return nil, fmt.Errorf("invalid response %q", response)
	}
	if g.selfEmailFor == nil {
		return nil, fmt.Errorf("self-email resolver not wired - cannot rsvp on behalf of unknown identity")
	}
	self := strings.TrimSpace(g.selfEmailFor(accountID))
	if self == "" {
		return nil, fmt.Errorf("self-email for account %q not yet discovered - retry after the next connector identity sweep", accountID)
	}
	calID := calendarID
	if calID == "" {
		calID = defaultCalendarID
	}

	current, err := g.getEvent(ctx, accountID, calID, remoteID)
	if err != nil {
		return nil, err
	}
	var found bool
	attendees := current.Attendees
	for i, a := range attendees {
		if strings.EqualFold(a.Email, self) || a.Self {
			attendees[i].ResponseStatus = string(response)
			attendees[i].Self = true
			found = true
			break
		}
	}
	if !found {
		attendees = append(attendees, Attendee{
			Email:          self,
			ResponseStatus: string(response),
			Self:           true,
		})
	}

	args := map[string]any{
		"calendarId":  calID,
		"eventId":     remoteID,
		"attendees":   attendeesToGoogle(attendees),
		"sendUpdates": "externalOnly",
	}
	data, err := g.execute(ctx, actPatchEvent, accountID, args)
	if err != nil {
		return nil, err
	}
	ev, err := parseGoogleEvent(data, accountID, calID)
	if err != nil {
		return nil, err
	}
	return &ev, nil
}

// Patch applies a partial update. Composio's PATCH_EVENT accepts the
// same fields Google's Event resource does, flattened into argument
// keys (summary, description, location, start, end, attendees, ...).
// Caller-provided keys are merged into the call body verbatim alongside
// the required calendarId + eventId.
func (g *GoogleProvider) Patch(ctx context.Context, accountID, calendarID, remoteID string, patch map[string]any) (*Event, error) {
	calID := calendarID
	if calID == "" {
		calID = defaultCalendarID
	}
	args := map[string]any{
		"calendarId": calID,
		"eventId":    remoteID,
	}
	for k, v := range patch {
		args[k] = v
	}
	data, err := g.execute(ctx, actPatchEvent, accountID, args)
	if err != nil {
		return nil, err
	}
	ev, err := parseGoogleEvent(data, accountID, calID)
	if err != nil {
		return nil, err
	}
	return &ev, nil
}

// Create posts a new event. `draft` is the flat argument map Composio's
// CREATE_EVENT accepts (summary, start, end, description, location,
// attendees, ...). We add calendarId + sendUpdates=all so invitees
// receive the invitation email.
func (g *GoogleProvider) Create(ctx context.Context, accountID, calendarID string, draft map[string]any) (*Event, error) {
	calID := calendarID
	if calID == "" {
		calID = defaultCalendarID
	}
	args := map[string]any{
		"calendarId":  calID,
		"sendUpdates": "all",
	}
	for k, v := range draft {
		args[k] = v
	}
	data, err := g.execute(ctx, actCreateEvent, accountID, args)
	if err != nil {
		return nil, err
	}
	ev, err := parseGoogleEvent(data, accountID, calID)
	if err != nil {
		return nil, err
	}
	return &ev, nil
}

// Delete removes an event. 404 is treated as success since the
// idempotent intent ("event gone") is satisfied.
func (g *GoogleProvider) Delete(ctx context.Context, accountID, calendarID, remoteID string) error {
	calID := calendarID
	if calID == "" {
		calID = defaultCalendarID
	}
	args := map[string]any{
		"calendarId":  calID,
		"eventId":     remoteID,
		"sendUpdates": "all",
	}
	_, err := g.execute(ctx, actDeleteEvent, accountID, args)
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			return nil
		}
		return err
	}
	return nil
}

// getEvent is the internal read we use to round-trip an event before a
// RSVP patch (to preserve other invitees' responses).
func (g *GoogleProvider) getEvent(ctx context.Context, accountID, calendarID, remoteID string) (*Event, error) {
	args := map[string]any{
		"calendarId": calendarID,
		"eventId":    remoteID,
	}
	data, err := g.execute(ctx, actFindEvent, accountID, args)
	if err != nil {
		return nil, err
	}
	ev, err := parseGoogleEvent(data, accountID, calendarID)
	if err != nil {
		return nil, err
	}
	return &ev, nil
}

// attendeesToGoogle reshapes our Attendee slice into the wire format
// Google (and Composio's PATCH_EVENT) expects.
func attendeesToGoogle(in []Attendee) []map[string]any {
	out := make([]map[string]any, 0, len(in))
	for _, a := range in {
		row := map[string]any{"email": a.Email}
		if a.DisplayName != "" {
			row["displayName"] = a.DisplayName
		}
		if a.ResponseStatus != "" {
			row["responseStatus"] = a.ResponseStatus
		}
		if a.Self {
			row["self"] = true
		}
		if a.Organizer {
			row["organizer"] = true
		}
		if a.Optional {
			row["optional"] = true
		}
		out = append(out, row)
	}
	return out
}

// parseGoogleEvent decodes one item from events.list (or one full event
// resource) into our Event shape. Tolerant of missing fields - every
// field on Google's Event resource is optional in the response.
func parseGoogleEvent(raw json.RawMessage, accountID, calendarID string) (Event, error) {
	var src struct {
		ID          string `json:"id"`
		Etag        string `json:"etag"`
		Status      string `json:"status"`
		Summary     string `json:"summary"`
		Description string `json:"description"`
		Location    string `json:"location"`
		HTMLLink    string `json:"htmlLink"`
		HangoutLink string `json:"hangoutLink"`
		Start       struct {
			DateTime string `json:"dateTime"`
			Date     string `json:"date"`
			TimeZone string `json:"timeZone"`
		} `json:"start"`
		End struct {
			DateTime string `json:"dateTime"`
			Date     string `json:"date"`
			TimeZone string `json:"timeZone"`
		} `json:"end"`
		Recurrence []string `json:"recurrence"`
		Organizer  struct {
			Email       string `json:"email"`
			DisplayName string `json:"displayName"`
			Self        bool   `json:"self"`
		} `json:"organizer"`
		Attendees []struct {
			Email          string `json:"email"`
			DisplayName    string `json:"displayName"`
			ResponseStatus string `json:"responseStatus"`
			Self           bool   `json:"self"`
			Organizer      bool   `json:"organizer"`
			Optional       bool   `json:"optional"`
		} `json:"attendees"`
		ConferenceData struct {
			Solution struct {
				Name    string `json:"name"`
				IconURI string `json:"iconUri"`
			} `json:"solution"`
			EntryPoints []struct {
				EntryPointType string `json:"entryPointType"`
				URI            string `json:"uri"`
				Label          string `json:"label"`
			} `json:"entryPoints"`
		} `json:"conferenceData"`
	}
	if err := json.Unmarshal(raw, &src); err != nil {
		return Event{}, err
	}

	out := Event{
		AccountID:   accountID,
		CalendarID:  calendarID,
		RemoteID:    src.ID,
		RemoteEtag:  src.Etag,
		Title:       src.Summary,
		Description: src.Description,
		Location:    src.Location,
		HTMLLink:    src.HTMLLink,
		HangoutLink: src.HangoutLink,
		Status:      src.Status,
		Recurrence:  src.Recurrence,
		Organizer: Organizer{
			Email:       src.Organizer.Email,
			DisplayName: src.Organizer.DisplayName,
			Self:        src.Organizer.Self,
		},
	}

	if src.Start.DateTime != "" {
		if t, err := time.Parse(time.RFC3339, src.Start.DateTime); err == nil {
			out.StartsAt = t
		}
	} else if src.Start.Date != "" {
		out.AllDay = true
		if t, err := time.Parse("2006-01-02", src.Start.Date); err == nil {
			out.StartsAt = t
		}
	}
	if src.End.DateTime != "" {
		if t, err := time.Parse(time.RFC3339, src.End.DateTime); err == nil {
			out.EndsAt = t
		}
	} else if src.End.Date != "" {
		if t, err := time.Parse("2006-01-02", src.End.Date); err == nil {
			out.EndsAt = t
		}
	}

	for _, a := range src.Attendees {
		out.Attendees = append(out.Attendees, Attendee{
			Email:          a.Email,
			DisplayName:    a.DisplayName,
			ResponseStatus: a.ResponseStatus,
			Self:           a.Self,
			Organizer:      a.Organizer,
			Optional:       a.Optional,
		})
		if a.Self {
			out.ResponseStatus = a.ResponseStatus
		}
	}

	if src.ConferenceData.Solution.Name != "" || len(src.ConferenceData.EntryPoints) > 0 {
		conf := ConferenceData{
			SolutionName: src.ConferenceData.Solution.Name,
			IconURI:      src.ConferenceData.Solution.IconURI,
		}
		for _, ep := range src.ConferenceData.EntryPoints {
			conf.EntryPoints = append(conf.EntryPoints, ConferenceEntryPoint{
				Type:  ep.EntryPointType,
				URI:   ep.URI,
				Label: ep.Label,
			})
		}
		out.Conference = conf
	}

	return out, nil
}

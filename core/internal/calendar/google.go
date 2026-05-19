// google.go - Google Calendar v3 provider through Composio's tools.proxy.
//
// We do not hold an OAuth token. Every call routes through Composio's
// /api/v3/tools/proxy, which attaches the connected_account's current
// access token (refreshing as needed) and forwards the call to
// www.googleapis.com. The upstream JSON comes back unmodified.
//
// We use only documented Google Calendar v3 endpoints:
//
//   List:        GET    /calendar/v3/calendars/{calendarId}/events
//                       ?syncToken=… | ?timeMin=…&timeMax=…&pageToken=…
//   Patch RSVP:  PATCH  /calendar/v3/calendars/{calendarId}/events/{eventId}
//                       body: { attendees: [{email: SELF, responseStatus: …}] }
//                       Note: setting the boss's response is done via PATCH
//                       on the event with the self-attendee carrying the new
//                       responseStatus. Per Google's docs this is the
//                       supported pattern (there is no /respond endpoint).
//   Patch/Create/Delete: standard CRUD on the same /events resource.

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
	googleCalendarAPI = "/calendar/v3"
	defaultCalendarID = "primary"
	defaultPageSize   = 250
)

// GoogleProvider implements Provider against Google Calendar via Composio.
// `selfEmailFor` is a hook the sync layer fills so we know the boss's own
// email address per account (needed when writing the RSVP patch). The
// connector identity cache populates this from GMAIL/CALENDAR identity
// discovery; we just read it.
type GoogleProvider struct {
	exec         *connectors.ExecuteClient
	selfEmailFor func(accountID string) string
}

// NewGoogleProvider builds the provider. exec is required; selfEmailFor
// may be nil (Respond will return an error when called without it).
func NewGoogleProvider(exec *connectors.ExecuteClient, selfEmailFor func(accountID string) string) *GoogleProvider {
	return &GoogleProvider{exec: exec, selfEmailFor: selfEmailFor}
}

func (g *GoogleProvider) Slug() string { return providerSlug }

// List - one page of events. Either incremental (syncToken set) or full
// (timeMin/timeMax). Google forbids combining syncToken with timeMin -
// callers honor that by leaving SyncToken empty for full syncs.
func (g *GoogleProvider) List(ctx context.Context, accountID string, opts ListOptions) (*ListPage, error) {
	calID := opts.CalendarID
	if calID == "" {
		calID = defaultCalendarID
	}
	pageSize := opts.MaxResults
	if pageSize <= 0 || pageSize > 2500 {
		pageSize = defaultPageSize
	}

	params := []connectors.ProxyParam{
		{Name: "maxResults", Value: fmt.Sprintf("%d", pageSize), In: "query"},
		{Name: "singleEvents", Value: "true", In: "query"},
		{Name: "showDeleted", Value: "true", In: "query"},
	}
	if opts.SyncToken != "" {
		params = append(params, connectors.ProxyParam{Name: "syncToken", Value: opts.SyncToken, In: "query"})
	} else {
		// Full sync window. Default to "starting yesterday" so freshly
		// past events are still visible while ongoing today, and cap
		// out at +6mo so the initial pull stays bounded.
		tmin := opts.TimeMin
		if tmin.IsZero() {
			tmin = time.Now().AddDate(0, 0, -1)
		}
		tmax := opts.TimeMax
		if tmax.IsZero() {
			tmax = time.Now().AddDate(0, 6, 0)
		}
		params = append(params,
			connectors.ProxyParam{Name: "timeMin", Value: tmin.UTC().Format(time.RFC3339), In: "query"},
			connectors.ProxyParam{Name: "timeMax", Value: tmax.UTC().Format(time.RFC3339), In: "query"},
			connectors.ProxyParam{Name: "orderBy", Value: "startTime", In: "query"},
		)
	}
	if opts.PageToken != "" {
		params = append(params, connectors.ProxyParam{Name: "pageToken", Value: opts.PageToken, In: "query"})
	}

	resp, err := g.exec.Proxy(ctx, connectors.ProxyRequest{
		ConnectedAccountID: accountID,
		Endpoint:           googleCalendarAPI + "/calendars/" + calID + "/events",
		Method:             "GET",
		Parameters:         params,
	})
	if err != nil {
		// Detect Google's 410 GONE for an invalid syncToken; signal a
		// full-resync to the caller instead of returning an error.
		if strings.Contains(err.Error(), "410") {
			return &ListPage{FullResync: true}, nil
		}
		return nil, err
	}
	if !resp.Successful {
		if strings.Contains(strings.ToLower(resp.Error), "gone") || resp.Status == 410 {
			return &ListPage{FullResync: true}, nil
		}
		return nil, fmt.Errorf("google list: %s (status=%d)", resp.Error, resp.Status)
	}

	var raw struct {
		Items         []json.RawMessage `json:"items"`
		NextPageToken string            `json:"nextPageToken"`
		NextSyncToken string            `json:"nextSyncToken"`
	}
	if err := json.Unmarshal(resp.Data, &raw); err != nil {
		return nil, fmt.Errorf("google list decode: %w", err)
	}

	out := &ListPage{
		Events:    make([]Event, 0, len(raw.Items)),
		NextPage:  raw.NextPageToken,
		SyncToken: raw.NextSyncToken,
	}
	for _, item := range raw.Items {
		ev, err := parseGoogleEvent(item, accountID, calID)
		if err != nil {
			// Skip malformed rows rather than fail the whole page; one
			// odd event shouldn't drop the entire sync.
			continue
		}
		out.Events = append(out.Events, ev)
	}
	return out, nil
}

// Respond patches the boss's attendee row with a new responseStatus.
// Google's API requires us to PATCH the event with the FULL attendee
// list including the boss's row updated; we read the current state via
// a quick GET to avoid clobbering other attendees' responses.
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

	// Pull current event so we round-trip the existing attendee list.
	current, err := g.getEvent(ctx, accountID, calID, remoteID)
	if err != nil {
		return nil, err
	}
	// Update the matching attendee row in-place; if the boss isn't on the
	// list yet (rare - happens when an invitation lands via aliasing), add.
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

	patchBody := map[string]any{
		"attendees": attendeesToGoogle(attendees),
	}
	resp, err := g.exec.Proxy(ctx, connectors.ProxyRequest{
		ConnectedAccountID: accountID,
		Endpoint:           googleCalendarAPI + "/calendars/" + calID + "/events/" + remoteID,
		Method:             "PATCH",
		Parameters: []connectors.ProxyParam{
			// sendUpdates=externalOnly notifies non-self attendees (the
			// organizer + invitees) without spamming the boss with a
			// copy of his own RSVP. Standard for invitee RSVPs.
			{Name: "sendUpdates", Value: "externalOnly", In: "query"},
		},
		Body: patchBody,
	})
	if err != nil {
		return nil, err
	}
	if !resp.Successful {
		return nil, fmt.Errorf("google respond: %s (status=%d)", resp.Error, resp.Status)
	}
	ev, err := parseGoogleEvent(resp.Data, accountID, calID)
	if err != nil {
		return nil, err
	}
	return &ev, nil
}

// Patch applies a partial update. Patch keys map 1:1 to Google's Event
// resource (title -> "summary", description, location, start, end, …).
// The caller is responsible for building the Google-shaped patch body;
// this is intentionally low-level so the agent tool can pass through
// rich shapes the user typed in chat.
func (g *GoogleProvider) Patch(ctx context.Context, accountID, calendarID, remoteID string, patch map[string]any) (*Event, error) {
	calID := calendarID
	if calID == "" {
		calID = defaultCalendarID
	}
	resp, err := g.exec.Proxy(ctx, connectors.ProxyRequest{
		ConnectedAccountID: accountID,
		Endpoint:           googleCalendarAPI + "/calendars/" + calID + "/events/" + remoteID,
		Method:             "PATCH",
		Body:               patch,
	})
	if err != nil {
		return nil, err
	}
	if !resp.Successful {
		return nil, fmt.Errorf("google patch: %s (status=%d)", resp.Error, resp.Status)
	}
	ev, err := parseGoogleEvent(resp.Data, accountID, calID)
	if err != nil {
		return nil, err
	}
	return &ev, nil
}

// Create posts a new event. draft is the raw Google-shape body.
func (g *GoogleProvider) Create(ctx context.Context, accountID, calendarID string, draft map[string]any) (*Event, error) {
	calID := calendarID
	if calID == "" {
		calID = defaultCalendarID
	}
	resp, err := g.exec.Proxy(ctx, connectors.ProxyRequest{
		ConnectedAccountID: accountID,
		Endpoint:           googleCalendarAPI + "/calendars/" + calID + "/events",
		Method:             "POST",
		Parameters: []connectors.ProxyParam{
			{Name: "sendUpdates", Value: "all", In: "query"},
		},
		Body: draft,
	})
	if err != nil {
		return nil, err
	}
	if !resp.Successful {
		return nil, fmt.Errorf("google create: %s (status=%d)", resp.Error, resp.Status)
	}
	ev, err := parseGoogleEvent(resp.Data, accountID, calID)
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
	resp, err := g.exec.Proxy(ctx, connectors.ProxyRequest{
		ConnectedAccountID: accountID,
		Endpoint:           googleCalendarAPI + "/calendars/" + calID + "/events/" + remoteID,
		Method:             "DELETE",
		Parameters: []connectors.ProxyParam{
			{Name: "sendUpdates", Value: "all", In: "query"},
		},
	})
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			return nil
		}
		return err
	}
	if !resp.Successful && resp.Status != 404 {
		return fmt.Errorf("google delete: %s (status=%d)", resp.Error, resp.Status)
	}
	return nil
}

// getEvent is the internal GET we use to round-trip an event before a
// patch (to preserve other attendees' responses on an RSVP).
func (g *GoogleProvider) getEvent(ctx context.Context, accountID, calendarID, remoteID string) (*Event, error) {
	resp, err := g.exec.Proxy(ctx, connectors.ProxyRequest{
		ConnectedAccountID: accountID,
		Endpoint:           googleCalendarAPI + "/calendars/" + calendarID + "/events/" + remoteID,
		Method:             "GET",
	})
	if err != nil {
		return nil, err
	}
	if !resp.Successful {
		return nil, fmt.Errorf("google get: %s (status=%d)", resp.Error, resp.Status)
	}
	ev, err := parseGoogleEvent(resp.Data, accountID, calendarID)
	if err != nil {
		return nil, err
	}
	return &ev, nil
}

// attendeesToGoogle reshapes our Attendee slice into the wire format
// Google expects on a PATCH body. We strip empty/derived fields to
// keep the payload small and avoid sending back nulls.
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

// parseGoogleEvent decodes one item from events.list (or one event
// resource from get/patch/create) into our Event shape. Tolerant of
// missing fields - everything is optional in Google's response.
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

	// Start/end: dateTime (timed event) or date (all-day).
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

	// Attendees + self-response extraction.
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

	// Conference data: collapse to our slim shape.
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

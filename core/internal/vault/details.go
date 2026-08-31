package vault

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
)

// The boss's personal record: who he is, where to send things, and what proves
// he is him — with a switch on each one saying whether Jarvis may hand it over.
//
// THE CATALOG IS THE FEATURE
//
// Everything specific to a detail lives in this one table of Go values: its
// human label, whether it is sealed, which checkout fields it fills, whether a
// release switch even makes sense for it. The database has no column per
// detail and the settings screen has no field per detail, so adding "company
// name" or "second shipping address" later is a line here and nothing else.
// That is Rule #1: the contract stays generic, the knowledge is data.

// Group orders the settings screen and nothing else. The screen renders
// whatever groups the catalog contains, in this order.
const (
	GroupAbout    = "about"
	GroupShipping = "shipping"
	GroupBilling  = "billing"
	GroupVerify   = "verify"
)

// DetailSpec describes one thing we know about the boss.
type DetailSpec struct {
	Key   string
	Label string
	Group string
	// Sealed details are encrypted at rest and never returned by any route.
	// The screen shows "Saved" and offers to replace, never the value.
	Sealed bool
	// Autofill are the markers a checkout uses for this field. Matched against
	// an input's autocomplete/name/id/placeholder, so one row here teaches the
	// filler a field on every merchant at once rather than one at a time.
	Autofill []string
	// Placeholder is the example shown in the empty box.
	Placeholder string
	// Releasable false means Jarvis may NEVER hand this over and the screen
	// shows no switch. The spoken password is the case: it exists to verify the
	// boss TO Jarvis, so reading it out would defeat the only thing it does.
	Releasable bool
	// Sensitive marks a value worth masking in the input even when it is clear.
	Sensitive bool
}

// Catalog is every detail the vault holds, in the order the screen shows them.
var Catalog = []DetailSpec{
	{Key: "given_name", Label: "First name", Group: GroupAbout, Releasable: true,
		Placeholder: "Khaya",
		Autofill:    []string{"given-name", "firstname", "first-name", "first_name", "fname"}},
	{Key: "family_name", Label: "Last name", Group: GroupAbout, Releasable: true,
		Placeholder: "Malabie",
		Autofill:    []string{"family-name", "lastname", "last-name", "last_name", "lname", "surname"}},
	{Key: "email", Label: "Email", Group: GroupAbout, Releasable: true,
		Placeholder: "you@example.com",
		Autofill:    []string{"email", "e-mail", "emailaddress", "email_address"}},
	{Key: "phone", Label: "Phone number", Group: GroupAbout, Releasable: true,
		Placeholder: "+16095551234",
		Autofill:    []string{"tel", "phone", "phonenumber", "phone-number", "mobile"}},

	{Key: "ship_line1", Label: "Street address", Group: GroupShipping, Releasable: true,
		Placeholder: "1600 Pennsylvania Ave",
		Autofill:    []string{"address-line1", "shipping address-line1", "street-address", "address1", "addr1", "street"}},
	{Key: "ship_line2", Label: "Apartment or suite", Group: GroupShipping, Releasable: true,
		Placeholder: "Apt 4B",
		Autofill:    []string{"address-line2", "shipping address-line2", "address2", "addr2", "apt", "suite"}},
	{Key: "ship_city", Label: "City", Group: GroupShipping, Releasable: true,
		Placeholder: "Chicago",
		Autofill:    []string{"address-level2", "city", "town", "locality"}},
	{Key: "ship_state", Label: "State", Group: GroupShipping, Releasable: true,
		Placeholder: "IL",
		Autofill:    []string{"address-level1", "state", "province", "region"}},
	{Key: "ship_postal", Label: "Zip code", Group: GroupShipping, Releasable: true,
		Placeholder: "60614",
		Autofill:    []string{"postal-code", "zip", "zipcode", "zip-code", "postcode"}},
	{Key: "ship_country", Label: "Country", Group: GroupShipping, Releasable: true,
		Placeholder: "United States",
		Autofill:    []string{"country", "country-name"}},

	{Key: "bill_line1", Label: "Street address", Group: GroupBilling, Releasable: true,
		Placeholder: "1600 Pennsylvania Ave",
		Autofill:    []string{"billing address-line1", "billing-address1", "billingaddress1"}},
	{Key: "bill_line2", Label: "Apartment or suite", Group: GroupBilling, Releasable: true,
		Placeholder: "Apt 4B",
		Autofill:    []string{"billing address-line2", "billing-address2", "billingaddress2"}},
	{Key: "bill_city", Label: "City", Group: GroupBilling, Releasable: true,
		Placeholder: "Chicago",
		Autofill:    []string{"billing address-level2", "billing-city", "billingcity"}},
	{Key: "bill_state", Label: "State", Group: GroupBilling, Releasable: true,
		Placeholder: "IL",
		Autofill:    []string{"billing address-level1", "billing-state", "billingstate"}},
	{Key: "bill_postal", Label: "Zip code", Group: GroupBilling, Releasable: true,
		Placeholder: "60614",
		Autofill:    []string{"billing postal-code", "billing-zip", "billingzip", "billingpostal"}},
	{Key: "bill_country", Label: "Country", Group: GroupBilling, Releasable: true,
		Placeholder: "United States",
		Autofill:    []string{"billing country", "billing-country", "billingcountry"}},

	{Key: "dob", Label: "Date of birth", Group: GroupVerify, Sealed: true, Releasable: true,
		Placeholder: "MM/DD/YYYY",
		Autofill:    []string{"bday", "birthdate", "birth-date", "date-of-birth", "dateofbirth"}},
	{Key: "ssn_last4", Label: "Last four of your social", Group: GroupVerify, Sealed: true, Releasable: true,
		Placeholder: "Optional"},
	{Key: "account_number", Label: "Account number", Group: GroupVerify, Sealed: true, Releasable: true,
		Placeholder: "Optional"},
	// Releasable is FALSE and has no switch, on purpose. This phrase proves the
	// boss is the boss when he rings the line. A Jarvis willing to say it out
	// loud has handed the keys to whoever asked.
	{Key: "passphrase", Label: "Spoken password", Group: GroupVerify, Sealed: true, Releasable: false,
		Placeholder: "e.g. blue falcon 22"},
}

// BillSameKey records "my billing address is my shipping address". It is a
// preference rather than a detail, so it is stored here (one place for the
// record) but never appears in the catalog, never gets a release switch, and is
// resolved before anything reads a bill_* value.
const BillSameKey = "bill_same_as_ship"

// SpecFor looks up one catalog entry.
func SpecFor(key string) (DetailSpec, bool) {
	for _, s := range Catalog {
		if s.Key == key {
			return s, true
		}
	}
	return DetailSpec{}, false
}

// Detail is what the settings screen receives. Note what is NOT here: a sealed
// value. Saved says whether there is one, and that is the most a screen is
// allowed to learn.
type Detail struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Group       string `json:"group"`
	Placeholder string `json:"placeholder,omitempty"`
	Sealed      bool   `json:"sealed"`
	Saved       bool   `json:"saved"`
	Releasable  bool   `json:"releasable"`
	// CanToggle false means this detail may never be released and the screen
	// shows no switch for it.
	CanToggle bool `json:"can_toggle"`
	// Value is present only for clear details. A sealed one is always "".
	Value string `json:"value,omitempty"`
}

// Details is the store. It hangs off the same Store so it shares the key.
type Details struct{ s *Store }

func NewDetails(s *Store) *Details { return &Details{s: s} }

// All returns the whole catalog, filled in with what is stored. Every catalog
// entry comes back whether or not it has a value, because a screen that only
// lists what is already saved gives you nowhere to type the rest.
func (d *Details) All(ctx context.Context) ([]Detail, bool, error) {
	out := make([]Detail, 0, len(Catalog))
	stored := map[string]storedDetail{}
	same := false
	if d != nil && d.s != nil && d.s.pool != nil {
		rows, err := d.s.pool.Query(ctx,
			`SELECT key, value, sealed, releasable FROM mem_vault_details`)
		if err != nil {
			return nil, false, err
		}
		defer rows.Close()
		for rows.Next() {
			var (
				k      string
				v      *string
				sealed []byte
				rel    bool
			)
			if err := rows.Scan(&k, &v, &sealed, &rel); err != nil {
				return nil, false, err
			}
			val := ""
			if v != nil {
				val = *v
			}
			if k == BillSameKey {
				same = val == "1"
				continue
			}
			stored[k] = storedDetail{value: val, saved: val != "" || len(sealed) > 0, releasable: rel}
		}
		if err := rows.Err(); err != nil {
			return nil, false, err
		}
	}

	for _, spec := range Catalog {
		got, ok := stored[spec.Key]
		det := Detail{
			Key:         spec.Key,
			Label:       spec.Label,
			Group:       spec.Group,
			Placeholder: spec.Placeholder,
			Sealed:      spec.Sealed,
			CanToggle:   spec.Releasable,
			// An unset detail defaults to the catalog's answer, so a fresh
			// install behaves the way the catalog says rather than the way an
			// absent row happens to scan.
			Releasable: spec.Releasable,
		}
		if ok {
			det.Saved = got.saved
			det.Releasable = spec.Releasable && got.releasable
			if !spec.Sealed {
				det.Value = got.value
			}
		}
		out = append(out, det)
	}
	return out, same, nil
}

type storedDetail struct {
	value      string
	saved      bool
	releasable bool
}

// Put stores one detail, sealing it when the catalog says to. An empty value
// removes the row, which is how the boss clears a field.
func (d *Details) Put(ctx context.Context, key, value string) error {
	spec, ok := SpecFor(key)
	if !ok {
		return errors.New("vault: no such detail")
	}
	if d == nil || d.s == nil || d.s.pool == nil {
		return errors.New("vault: no database")
	}
	value = strings.TrimSpace(value)
	if value == "" {
		_, err := d.s.pool.Exec(ctx, `DELETE FROM mem_vault_details WHERE key = $1`, key)
		return err
	}
	if !spec.Sealed {
		_, err := d.s.pool.Exec(ctx, `
			INSERT INTO mem_vault_details (key, value, sealed, nonce, updated_at)
			VALUES ($1, $2, NULL, NULL, NOW())
			ON CONFLICT (key) DO UPDATE
			   SET value = EXCLUDED.value, sealed = NULL, nonce = NULL, updated_at = NOW()
		`, key, value)
		return err
	}
	if !d.s.hasKey() {
		return ErrNoKey
	}
	sealed, nonce, err := d.s.seal(Secrets{Name: value})
	if err != nil {
		return err
	}
	_, err = d.s.pool.Exec(ctx, `
		INSERT INTO mem_vault_details (key, value, sealed, nonce, key_version, updated_at)
		VALUES ($1, NULL, $2, $3, $4, NOW())
		ON CONFLICT (key) DO UPDATE
		   SET value = NULL, sealed = EXCLUDED.sealed, nonce = EXCLUDED.nonce,
		       key_version = EXCLUDED.key_version, updated_at = NOW()
	`, key, sealed, nonce, keyVersion)
	return err
}

// SetReleasable flips whether Jarvis may hand a detail over.
//
// A detail the catalog marks unreleasable cannot be switched on here. That
// refusal is the point: the spoken password's whole job is to prove the boss is
// the boss, and a switch that could turn it into something Jarvis says out loud
// would be a switch for destroying it.
func (d *Details) SetReleasable(ctx context.Context, key string, on bool) error {
	spec, ok := SpecFor(key)
	if !ok {
		return errors.New("vault: no such detail")
	}
	if !spec.Releasable && on {
		return errors.New("vault: this one can never be read out")
	}
	if d == nil || d.s == nil || d.s.pool == nil {
		return errors.New("vault: no database")
	}
	_, err := d.s.pool.Exec(ctx, `
		INSERT INTO mem_vault_details (key, value, releasable, updated_at)
		VALUES ($1, '', $2, NOW())
		ON CONFLICT (key) DO UPDATE SET releasable = EXCLUDED.releasable, updated_at = NOW()
	`, key, on)
	return err
}

// SetBillingSameAsShipping records the preference.
func (d *Details) SetBillingSameAsShipping(ctx context.Context, same bool) error {
	if d == nil || d.s == nil || d.s.pool == nil {
		return errors.New("vault: no database")
	}
	v := ""
	if same {
		v = "1"
	}
	_, err := d.s.pool.Exec(ctx, `
		INSERT INTO mem_vault_details (key, value, releasable, updated_at)
		VALUES ($1, $2, TRUE, NOW())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()
	`, BillSameKey, v)
	return err
}

// Release is THE read path. Everything that hands a detail to the outside world
// goes through it: the checkout filler, the phone brief.
//
// It returns only details the boss has marked releasable. A withheld one is not
// filtered later and not left to the model's discretion, it is never loaded, so
// there is nothing downstream that could leak it. Wanting a specific key is not
// enough to get it.
//
// Billing falls back to shipping when the boss ticked "same as shipping", so a
// checkout asking for a billing address gets one without him typing it twice.
func (d *Details) Release(ctx context.Context) (map[string]string, error) {
	out := map[string]string{}
	if d == nil || d.s == nil || d.s.pool == nil {
		return out, nil
	}
	rows, err := d.s.pool.Query(ctx,
		`SELECT key, value, sealed, nonce, releasable FROM mem_vault_details`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	same := false
	type held struct {
		value         string
		sealed, nonce []byte
		releasable    bool
	}
	all := map[string]held{}
	for rows.Next() {
		var (
			k             string
			v             *string
			sealed, nonce []byte
			rel           bool
		)
		if err := rows.Scan(&k, &v, &sealed, &nonce, &rel); err != nil {
			return nil, err
		}
		val := ""
		if v != nil {
			val = *v
		}
		if k == BillSameKey {
			same = val == "1"
			continue
		}
		all[k] = held{value: val, sealed: sealed, nonce: nonce, releasable: rel}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, spec := range Catalog {
		h, ok := all[spec.Key]
		if !ok {
			continue
		}
		// Both gates, and the catalog's is the one that cannot be overridden.
		if !spec.Releasable || !h.releasable {
			continue
		}
		if spec.Sealed {
			if len(h.sealed) == 0 || !d.s.hasKey() {
				continue
			}
			sec, err := d.s.open(h.sealed, h.nonce)
			if err != nil {
				continue
			}
			if sec.Name != "" {
				out[spec.Key] = sec.Name
			}
			continue
		}
		if h.value != "" {
			out[spec.Key] = h.value
		}
	}

	if same {
		for _, f := range []string{"line1", "line2", "city", "state", "postal", "country"} {
			if v, ok := out["ship_"+f]; ok && out["bill_"+f] == "" {
				out["bill_"+f] = v
			}
		}
	}
	return out, nil
}

// Get reads ONE detail regardless of its release switch.
//
// This exists for the spoken password and things like it: values Jarvis checks
// rather than hands over. The release switch governs what he may GIVE OUT, and
// a phrase he compares against an incoming caller is not something he gives
// out. Everything that does leave the machine goes through Release instead.
func (d *Details) Get(ctx context.Context, key string) (string, error) {
	spec, ok := SpecFor(key)
	if !ok {
		return "", errors.New("vault: no such detail")
	}
	if d == nil || d.s == nil || d.s.pool == nil {
		return "", errors.New("vault: no database")
	}
	var (
		v             *string
		sealed, nonce []byte
	)
	err := d.s.pool.QueryRow(ctx,
		`SELECT value, sealed, nonce FROM mem_vault_details WHERE key = $1`, key).Scan(&v, &sealed, &nonce)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	if !spec.Sealed {
		if v == nil {
			return "", ErrNotFound
		}
		return *v, nil
	}
	if len(sealed) == 0 {
		return "", ErrNotFound
	}
	if !d.s.hasKey() {
		return "", ErrNoKey
	}
	sec, err := d.s.open(sealed, nonce)
	if err != nil {
		return "", err
	}
	return sec.Name, nil
}

// BillingSameAsShipping reports the preference, for the screen.
func (d *Details) BillingSameAsShipping(ctx context.Context) bool {
	if d == nil || d.s == nil || d.s.pool == nil {
		return false
	}
	var v string
	if err := d.s.pool.QueryRow(ctx,
		`SELECT COALESCE(value, '') FROM mem_vault_details WHERE key = $1`, BillSameKey).Scan(&v); err != nil {
		return false
	}
	return v == "1"
}

// MigrateLegacyDetails moves the old single-blob secrets into per-detail rows,
// so the date of birth and account number the boss already typed carry over
// with a release switch each instead of being one opaque lump.
//
// Idempotent, and it never overwrites a detail that already exists: a boss who
// has edited a field since is not reverted by a boot.
func (d *Details) MigrateLegacyDetails(ctx context.Context, secrets *SecretStore) (int, error) {
	if d == nil || d.s == nil || d.s.pool == nil || !d.s.Healthy() {
		return 0, nil
	}
	moved := 0
	put := func(key, value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		var exists bool
		if err := d.s.pool.QueryRow(ctx,
			`SELECT TRUE FROM mem_vault_details WHERE key = $1`, key).Scan(&exists); err == nil {
			return // already carried over, or edited since
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return
		}
		if err := d.Put(ctx, key, value); err == nil {
			moved++
		}
	}

	if raw, err := secrets.Secret(ctx, KeyIdentity); err == nil {
		var id struct {
			DOB     string `json:"dob"`
			Account string `json:"account"`
			Last4   string `json:"last4"`
			Zip     string `json:"zip"`
		}
		if json.Unmarshal([]byte(raw), &id) == nil {
			put("dob", id.DOB)
			put("account_number", id.Account)
			put("ssn_last4", id.Last4)
			// The old identity zip was a BILLING zip, which is what it is
			// called here.
			put("bill_postal", id.Zip)
		}
	}
	if v, err := secrets.Secret(ctx, KeyPassphrase); err == nil {
		put("passphrase", v)
	}
	// The cell was never sealed and lives in meta; carry it across as the
	// phone number so there is one number rather than two.
	var cell string
	if err := d.s.pool.QueryRow(ctx,
		`SELECT value FROM infinity_meta WHERE key = $1`, KeyBossCell).Scan(&cell); err == nil {
		if cell != movedSentinel {
			put("phone", cell)
		}
	}
	return moved, nil
}

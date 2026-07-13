// tools.go — the phone book's agent-facing verbs.
//
// Generic on purpose: the same two verbs serve a chat turn ("Ariana's number is
// 929-310-0906"), a voice turn, the post-call agent turn that runs the boss's
// spoken errand, and any skill. Nothing here knows about pizza, wives, or any
// other specific case. Adding a new kind of contact needs no new code.
package contacts

import (
	"context"
	"fmt"
	"strings"

	"github.com/dopesoft/infinity/core/internal/tools"
)

// RegisterTools wires the phone book into the shared registry.
func RegisterTools(reg *tools.Registry, s *Store) {
	if reg == nil || s == nil {
		return
	}
	reg.Register(&contactSave{s: s})
	reg.Register(&contactFind{s: s})
}

type contactSave struct{ s *Store }

func (t *contactSave) Name() string { return "contact_save" }

func (t *contactSave) Description() string {
	return "Save someone to the boss's phone book so he can later just say their NAME " +
		"(\"call Ariana\", \"call Goodfellas Pizza\") instead of a number. Save a contact " +
		"the moment you learn one: when the boss gives you a name and number, when you " +
		"find a business's number by searching the web, or when a call teaches you who a " +
		"number belongs to. Saving again with the same number enriches the existing entry " +
		"rather than duplicating it, so it is always safe to call. The boss sees this book " +
		"on his dashboard."
}

func (t *contactSave) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "What the boss calls them: \"Ariana\", \"Goodfellas Pizza\".",
			},
			"number": map[string]any{
				"type":        "string",
				"description": "Their number in E.164 format, e.g. +19293100906.",
			},
			"aliases": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Any other way he refers to them: \"my wife\", \"Ari\", \"the pizza place\". These all resolve to this contact.",
			},
			"kind": map[string]any{
				"type":        "string",
				"enum":        []any{"person", "org"},
				"description": "A person, or a business/organization.",
			},
			"location": map[string]any{
				"type":        "string",
				"description": "What distinguishes this one from others of the same name: \"the one on Preston Road\". Essential for chain businesses.",
			},
			"note": map[string]any{
				"type":        "string",
				"description": "Anything worth knowing next time: the relationship, their usual order, how to treat them.",
			},
		},
		"required": []string{"name", "number"},
	}
}

func (t *contactSave) Execute(ctx context.Context, in map[string]any) (string, error) {
	c := Contact{
		Name:     str(in, "name"),
		Number:   str(in, "number"),
		Kind:     str(in, "kind"),
		Location: str(in, "location"),
		Note:     str(in, "note"),
		Aliases:  strSlice(in, "aliases"),
		Source:   "agent",
	}
	if err := t.s.Upsert(ctx, c); err != nil {
		return "", fmt.Errorf("saving %q to the phone book failed: %w", c.Name, err)
	}
	return "Saved " + c.Name + " (" + c.Number + ") to the phone book. The boss can now just say the name.", nil
}

type contactFind struct{ s *Store }

func (t *contactFind) Name() string { return "contact_find" }

func (t *contactFind) Description() string {
	return "Look someone up in the boss's phone book by name or alias, before asking him " +
		"for a number he has already given you. Returns every candidate: if more than one " +
		"comes back (two branches of the same restaurant), ASK him which he means rather " +
		"than guessing. If nothing comes back, the contact is unknown: find the number " +
		"(a web search, for a business), confirm the right one with him, then contact_save it."
}

func (t *contactFind) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "The name or alias the boss used: \"Ariana\", \"my wife\", \"goodfellas\".",
			},
		},
		"required": []string{"query"},
	}
}

func (t *contactFind) Execute(ctx context.Context, in map[string]any) (string, error) {
	q := str(in, "query")
	if q == "" {
		return "", fmt.Errorf("'query' is required: the name to look up")
	}
	found, err := t.s.Resolve(ctx, q)
	if err != nil {
		return "", fmt.Errorf("looking up %q in the phone book failed: %w", q, err)
	}
	return Describe(found), nil
}

func str(in map[string]any, key string) string {
	v, _ := in[key].(string)
	return strings.TrimSpace(v)
}

func strSlice(in map[string]any, key string) []string {
	raw, _ := in[key].([]any)
	out := []string{}
	for _, v := range raw {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

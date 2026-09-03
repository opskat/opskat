// Command notebook is OpsKat's in-repo reference extension.
//
// It is deliberately not a hello-world: it crosses every seam a real extension
// crosses — an asset type with a configuration form, tools the model reaches
// through the unified `exec`, a policy face with groups that allow, ask and
// refuse, a SKILL.md the model reads, and locales the UI resolves — while
// depending on nothing outside the host. Its storage is the host KV store, so
// the whole thing can be exercised offline and its side effects survive a
// reinstall (a reinstall replaces the extension directory, not the KV rows).
//
// The guest is a WASI reactor: the host runs _initialize and never calls main,
// so every declaration happens in init(). Those registration calls are also the
// extension's entire functional declaration — manifest.json carries only the
// capability grants, and the host reads the rest back through describe().
package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	opskat "github.com/opskat/opskat/pkg/extsdk"
)

func main() {}

// notebookConfig is the asset's configuration form. Tags drive the form the host
// renders: title / placeholder / desc are i18n keys resolved against locales/,
// and a field without `,omitempty` is required.
type notebookConfig struct {
	Notebook string `json:"notebook" title:"config.notebook.title" placeholder:"config.notebook.placeholder" desc:"config.notebook.desc"`
	MaxNotes int    `json:"maxNotes,omitempty" title:"config.maxNotes.title" desc:"config.maxNotes.desc"`
}

// Tool argument types are the schema: the host reflects each one into the
// parameter table it renders in `help` and parses `--flag=value` against, and the
// handler receives the same struct — so a renamed field renames the flag.
//
// asset_id is a declared parameter rather than ambient context because the host
// does not pass the asset to a tool call; a tool that needs the asset's config
// has to be told which asset it is running against.

type listArgs struct {
	AssetID int64  `json:"asset_id" desc:"ID of the notebook asset this call runs against"`
	Prefix  string `json:"prefix,omitempty" desc:"Only list notes whose key starts with this prefix"`
}

type getArgs struct {
	AssetID int64  `json:"asset_id" desc:"ID of the notebook asset this call runs against"`
	Key     string `json:"key" desc:"Key of the note to read"`
}

type putArgs struct {
	AssetID int64    `json:"asset_id" desc:"ID of the notebook asset this call runs against"`
	Key     string   `json:"key" desc:"Key of the note to create or overwrite"`
	Content string   `json:"content" desc:"Note body"`
	Tags    []string `json:"tags,omitempty" desc:"Optional labels, e.g. --tags=runbook,postgres"`
}

type deleteArgs struct {
	AssetID int64  `json:"asset_id" desc:"ID of the notebook asset this call runs against"`
	Key     string `json:"key" desc:"Key of the note to delete"`
}

func init() {
	opskat.Extension(opskat.Meta{
		Icon:        "archive",
		DisplayName: "extension.displayName",
		Description: "extension.description",
		PolicyType:  "notebook",
	})

	opskat.AssetType[notebookConfig]("notebook").Name("assetType.notebook.name")
	opskat.RegisterConfigValidator(func(raw json.RawMessage) []opskat.ValidationError {
		var cfg notebookConfig
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return []opskat.ValidationError{{Message: err.Error()}}
		}
		return validateConfig(cfg)
	})

	// The three decisions a policy face can produce, one group each. read is
	// granted to a new asset, so listing and reading run unattended; write is not,
	// so writing asks the user; delete is denied outright by a group that is also
	// granted by default, and a denial beats every allow.
	opskat.PolicyGroup("ext:notebook:read").
		Name("policy.read.name").Description("policy.read.description").
		Allow("read").Default()
	opskat.PolicyGroup("ext:notebook:write").
		Name("policy.write.name").Description("policy.write.description").
		Allow("read", "write")
	opskat.PolicyGroup("ext:notebook:no-delete").
		Name("policy.noDelete.name").Description("policy.noDelete.description").
		Deny("delete").Default()

	opskat.Tool("note_list", listNotes).Policy("read").Doc("tools.note_list.description")
	opskat.Tool("note_get", getNote).Policy("read").Doc("tools.note_get.description")
	opskat.Tool("note_put", putNote).Policy("write").Doc("tools.note_put.description")
	opskat.Tool("note_delete", deleteNote).Policy("delete").Doc("tools.note_delete.description")
}

// noteSummary is what listing reports: enough to choose a note without shipping
// every note's body through the model's context.
type noteSummary struct {
	Key       string   `json:"key"`
	Tags      []string `json:"tags,omitempty"`
	Size      int      `json:"size"`
	UpdatedAt string   `json:"updatedAt"`
}

func listNotes(_ *opskat.ToolContext, args listArgs) (any, error) {
	cfg, doc, err := open(args.AssetID)
	if err != nil {
		return nil, err
	}
	summaries := make([]noteSummary, 0, len(doc.Notes))
	for key, n := range doc.Notes {
		if !strings.HasPrefix(key, args.Prefix) {
			continue
		}
		summaries = append(summaries, noteSummary{
			Key: key, Tags: n.Tags, Size: len(n.Content), UpdatedAt: n.UpdatedAt,
		})
	}
	sort.Slice(summaries, func(i, j int) bool { return summaries[i].Key < summaries[j].Key })
	return map[string]any{
		"notebook": cfg.Notebook,
		"count":    len(summaries),
		"notes":    summaries,
	}, nil
}

func getNote(_ *opskat.ToolContext, args getArgs) (any, error) {
	cfg, doc, err := open(args.AssetID)
	if err != nil {
		return nil, err
	}
	n, ok := doc.Notes[args.Key]
	if !ok {
		return nil, fmt.Errorf("notebook %q has no note %q", cfg.Notebook, args.Key)
	}
	return n, nil
}

func putNote(_ *opskat.ToolContext, args putArgs) (any, error) {
	cfg, doc, err := open(args.AssetID)
	if err != nil {
		return nil, err
	}
	key, err := checkNoteKey(args.Key)
	if err != nil {
		return nil, err
	}
	_, existed := doc.Notes[key]
	if !existed && cfg.MaxNotes > 0 && len(doc.Notes) >= cfg.MaxNotes {
		return nil, fmt.Errorf("notebook %q already holds %d notes (maxNotes=%d)",
			cfg.Notebook, len(doc.Notes), cfg.MaxNotes)
	}
	doc.Notes[key] = note{
		Key:       key,
		Content:   args.Content,
		Tags:      args.Tags,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := save(cfg, doc); err != nil {
		return nil, err
	}
	// The host routes guest logs into the app's structured log, which is how a
	// side effect that leaves no visible output can still be observed.
	opskat.Log("info", fmt.Sprintf("notebook %s: stored note %s (%d bytes)", cfg.Notebook, key, len(args.Content)))
	return map[string]any{
		"notebook": cfg.Notebook,
		"key":      key,
		"created":  !existed,
		"count":    len(doc.Notes),
	}, nil
}

func deleteNote(_ *opskat.ToolContext, args deleteArgs) (any, error) {
	cfg, doc, err := open(args.AssetID)
	if err != nil {
		return nil, err
	}
	if _, ok := doc.Notes[args.Key]; !ok {
		return nil, fmt.Errorf("notebook %q has no note %q", cfg.Notebook, args.Key)
	}
	delete(doc.Notes, args.Key)
	if err := save(cfg, doc); err != nil {
		return nil, err
	}
	opskat.Log("info", fmt.Sprintf("notebook %s: deleted note %s", cfg.Notebook, args.Key))
	return map[string]any{
		"notebook": cfg.Notebook,
		"key":      args.Key,
		"deleted":  true,
		"count":    len(doc.Notes),
	}, nil
}

var noteKeyRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/-]{0,63}$`)

func checkNoteKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if !noteKeyRe.MatchString(key) {
		return "", fmt.Errorf("note key %q must match %s", key, noteKeyRe.String())
	}
	return key, nil
}

// media_job.go — the ONE generic building block for producing media.
//
// Per Rule #1 / #1b: the cognition (which model, what prompt, what aspect
// ratio, which graphic to design) lives in a SKILL recipe; the MECHANICS
// (run a media-producing command on the active bridge, track it as a long
// action so the spinner survives navigation, capture/download the output,
// register it as a durable artifact, stamp it onto the run so the Studio
// Media tab can show it live) live HERE, in deterministic Go with ZERO
// per-vendor branches.
//
// A "media-producing command" is anything that emits image/video bytes:
//
//   - higgsfield generate create … --json   (result=stdout_urls, PR1)
//   - node render.js … / python assemble.py  (result=output_files, PR2/PR3)
//   - ffmpeg … out.mp4                        (result=output_files)
//
// The skill crafts the command; media_job runs it the same way every time.
// New vendor = new command in a skill, never a new Go path.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/dopesoft/infinity/core/internal/bridge"
	"github.com/dopesoft/infinity/core/internal/runs"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RegisterMediaTools registers the generic media_job runner. It needs the
// bridge router (to run the producing command on whichever bridge is active
// for the session — cloud-first, Mac when pinned/up) and the pool (to index
// produced assets into mem_artifacts). No-op without a router or pool.
func RegisterMediaTools(r *Registry, pool *pgxpool.Pool, router *bridge.Router, prefs PreferenceFetcher) {
	if r == nil || pool == nil || router == nil {
		return
	}
	r.Register(&mediaJob{pool: pool, router: router, prefs: prefs})
}

type mediaJob struct {
	pool   *pgxpool.Pool
	router *bridge.Router
	prefs  PreferenceFetcher
}

func (t *mediaJob) Name() string { return "media_job" }

func (t *mediaJob) Description() string {
	return "Produce VISUAL or AUDIO media — an image, video, or audio clip — by running a media-generating " +
		"command on the active bridge, then capture, index, and surface every asset it makes. Use this for ALL " +
		"media output — higgsfield generation, an HTML→video render, an ffmpeg cut. " +
		"NOT for documents: a report, spreadsheet, deck, PDF, Word/Excel/PowerPoint, or markdown deliverable is " +
		"a DOCUMENT — use `document_create` for those, never this, even when the ask says 'downloadable'. " +
		"The command is yours to craft; " +
		"this tool tracks it as a live job (the Media tab shows a spinner that survives refresh), pulls " +
		"the resulting files into the workspace, saves each as a Library artifact, and returns the assets. " +
		"`result=stdout_urls` extracts result URLs the command prints (e.g. higgsfield --json); " +
		"`result=output_files` globs files the command wrote (e.g. a rendered .mov)."
}

func (t *mediaJob) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "The full shell command that produces the media (run on the active bridge).",
			},
			"label": map[string]any{
				"type":        "string",
				"description": "Short human label for the job, shown next to the spinner (e.g. 'Generating product hero').",
			},
			"result": map[string]any{
				"type":        "string",
				"enum":        []string{"stdout_urls", "output_files"},
				"description": "How to find the produced media: 'stdout_urls' parses media URLs the command prints; 'output_files' globs files it wrote. Default stdout_urls.",
			},
			"output_glob": map[string]any{
				"type":        "string",
				"description": "Required when result=output_files: a shell glob of the produced files (e.g. 'projects/x/out/*.mov').",
			},
			"timeout_sec": map[string]any{
				"type":        "integer",
				"description": "Wall-clock budget for the producing command. Default 600 (10 min).",
			},
		},
		"required": []string{"command"},
	}
}

// mediaURLRe matches the media URLs higgsfield (and friends) print in --json
// output: an http(s) URL ending in a known image/video extension, optionally
// followed by a query string (CDN signing).
var mediaURLRe = regexp.MustCompile(`https?://[^\s"'` + "`" + `)\]]+\.(?:png|jpe?g|webp|gif|mp4|mov|webm|m4v)(?:\?[^\s"'` + "`" + `)\]]*)?`)

// extByMedia classifies a filename into the mem_artifacts kind + a MIME type
// by extension. The single place media typing happens — keep it data-driven.
func extByMedia(name string) (kind, mime string) {
	switch strings.ToLower(path.Ext(strings.SplitN(name, "?", 2)[0])) {
	case ".png":
		return "image", "image/png"
	case ".jpg", ".jpeg":
		return "image", "image/jpeg"
	case ".webp":
		return "image", "image/webp"
	case ".gif":
		return "image", "image/gif"
	case ".mp4", ".m4v":
		return "video", "video/mp4"
	case ".mov":
		return "video", "video/quicktime"
	case ".webm":
		return "video", "video/webm"
	}
	return "image", "application/octet-stream"
}

// bashEnvelope is the shape both bridges return from /bash.
type bashEnvelope struct {
	Output   string `json:"output"`
	ExitCode int    `json:"exit_code"`
}

// runBash executes a command on the given bridge and returns its combined
// output + exit code. transport failure (status>=300 / unreachable) surfaces
// as an error; a non-zero exit code does NOT — the caller decides.
func (t *mediaJob) runBash(ctx context.Context, b bridge.Bridge, cmd, cwd string, timeoutSec int) (bashEnvelope, error) {
	body, status, ok := b.Post(ctx, "/bash", map[string]any{
		"cmd":         cmd,
		"cwd":         cwd,
		"timeout_sec": timeoutSec,
	})
	if !ok || status >= 300 {
		return bashEnvelope{}, fmt.Errorf("bridge %s /bash failed (status=%d): %s", b.Name(), status, bridgeErrText(body))
	}
	var env bashEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		// Some paths prefix the body; fall back to a tolerant extract.
		env.Output = strings.TrimSpace(string(body))
	}
	return env, nil
}

// mediaItem is one produced asset, stamped onto mem_runs.meta.media so the
// Studio Media tab renders it live, and mirrored into mem_artifacts.
type mediaItem struct {
	ID   string `json:"id"`             // mem_artifacts id (empty if indexing failed)
	Kind string `json:"kind"`           // image | video
	Mime string `json:"mime"`           // storage MIME
	Name string `json:"name"`           // file basename
	URL  string `json:"url"`            // browser-loadable src (public CDN, or /api/workspace/download)
	Path string `json:"path,omitempty"` // durable workspace path
}

func (t *mediaJob) Execute(ctx context.Context, in map[string]any) (string, error) {
	command := strings.TrimSpace(strString(in, "command"))
	if command == "" {
		return "", fmt.Errorf("media_job: command is required")
	}
	label := strString(in, "label")
	if label == "" {
		label = "Generating media"
	}
	resultMode := strString(in, "result")
	if resultMode == "" {
		resultMode = "stdout_urls"
	}
	timeoutSec := intOrZero(in, "timeout_sec")
	if timeoutSec <= 0 {
		timeoutSec = 600
	}
	sid := SessionIDFromContext(ctx)

	// Book the long-action row up front so the Media tab shows a spinner the
	// instant the job starts — and it survives navigation / refresh / device
	// switch because the truth lives in mem_runs, not component state.
	h := runs.BeginGlobal(ctx, runs.KindMediaGenerate, sid, label, runs.SourceAgent)

	b, why, err := pickBridge(ctx, t.router, t.prefs)
	if err != nil {
		h.Finish(ctx, fmt.Errorf("no bridge to run media on: %s", why), "")
		return "", fmt.Errorf("media_job: %s", why)
	}

	// A per-session media directory under the workspace root. Relative path so
	// it resolves under /workspace on cloud and the bridge root on Mac; the
	// /api/workspace/download proxy reads the same relative path.
	short := sid
	if len(short) > 8 {
		short = short[:8]
	}
	if short == "" {
		short = "adhoc"
	}
	mediaDir := "media/" + short

	h.Progress(ctx, 0.1, "running")
	env, err := t.runBash(ctx, b, command, "", timeoutSec)
	if err != nil {
		h.Finish(ctx, err, "")
		return "", fmt.Errorf("media_job: %w", err)
	}
	if env.ExitCode != 0 {
		out := env.Output
		if len(out) > 600 {
			out = out[len(out)-600:]
		}
		jobErr := fmt.Errorf("media command exited %d: %s", env.ExitCode, strings.TrimSpace(out))
		h.Finish(ctx, jobErr, "")
		return "", fmt.Errorf("media_job: %w", jobErr)
	}

	// Resolve the list of (url-or-path, displayURL) the command produced.
	type ref struct {
		src     string // CDN url (stdout) OR workspace path (files)
		display string // browser src
		isURL   bool
	}
	var refs []ref
	switch resultMode {
	case "output_files":
		glob := strString(in, "output_glob")
		if glob == "" {
			h.Finish(ctx, fmt.Errorf("output_glob required when result=output_files"), "")
			return "", fmt.Errorf("media_job: output_glob required when result=output_files")
		}
		ls, lerr := t.runBash(ctx, b, "ls -1 "+shellQuote(glob)+" 2>/dev/null", "", 30)
		if lerr == nil {
			for _, line := range strings.Split(ls.Output, "\n") {
				p := strings.TrimSpace(line)
				if p == "" {
					continue
				}
				refs = append(refs, ref{src: p, display: "/api/workspace/download?path=" + queryEscape(p), isURL: false})
			}
		}
	default: // stdout_urls
		seen := map[string]bool{}
		for _, u := range mediaURLRe.FindAllString(env.Output, -1) {
			if seen[u] {
				continue
			}
			seen[u] = true
			refs = append(refs, ref{src: u, display: u, isURL: true})
		}
	}

	if len(refs) == 0 {
		// Fail loud — a media job that produced nothing is a failure, not a
		// silent success. Tail of the output helps the agent diagnose.
		tail := env.Output
		if len(tail) > 500 {
			tail = tail[len(tail)-500:]
		}
		jobErr := fmt.Errorf("command produced no media (result=%s). output tail: %s", resultMode, strings.TrimSpace(tail))
		h.Finish(ctx, jobErr, "")
		return "", fmt.Errorf("media_job: %w", jobErr)
	}

	items := make([]mediaItem, 0, len(refs))
	for i, rf := range refs {
		h.Progress(ctx, 0.5+0.4*float32(i)/float32(len(refs)), fmt.Sprintf("saving %d/%d", i+1, len(refs)))

		base := path.Base(strings.SplitN(rf.src, "?", 2)[0])
		if base == "" || base == "." || base == "/" {
			base = fmt.Sprintf("media-%d", i+1)
		}
		kind, mime := extByMedia(base)
		uniq := uuid.NewString()[:8]
		destPath := mediaDir + "/" + uniq + "-" + base
		display := rf.display

		if rf.isURL {
			// Download a durable copy into the workspace so the Library asset
			// survives the CDN URL's TTL. The Media tab still shows the public
			// CDN url for an instant, auth-free <img>/<video> preview.
			dl := fmt.Sprintf("mkdir -p %s && curl -fsSL -o %s %s",
				shellQuote(mediaDir), shellQuote(destPath), shellQuote(rf.src))
			if dlEnv, derr := t.runBash(ctx, b, dl, "", 180); derr != nil || dlEnv.ExitCode != 0 {
				// Download failed — keep the CDN url as the storage path so the
				// asset still exists, just non-durably. Don't drop it.
				destPath = ""
			}
		}

		// Index the asset into the Library. Storage points at the durable
		// workspace copy when we have one, else the source URL.
		storageKind := "filesystem"
		storagePath := destPath
		if destPath == "" {
			storageKind = "object_store"
			storagePath = rf.src
		}
		vpath := "/media/" + short + "/" + uniq + "-" + base
		meta := map[string]any{"job_label": label, "command": command}
		if rf.isURL {
			meta["source_url"] = rf.src
		}
		metaJSON, _ := json.Marshal(meta)

		var artID string
		_ = t.pool.QueryRow(ctx, `
			INSERT INTO mem_artifacts
				(kind, name, virtual_path, storage_kind, storage_path, storage_mime,
				 bridge, source_session_id, source_tool, metadata)
			VALUES
				($1, $2, $3, $4, NULLIF($5,''), $6,
				 $7, NULLIF($8,'')::uuid, 'media_job', $9::jsonb)
			RETURNING id::text
		`, kind, base, vpath, storageKind, storagePath, mime,
			string(b.Name()), sid, string(metaJSON)).Scan(&artID)

		items = append(items, mediaItem{
			ID: artID, Kind: kind, Mime: mime, Name: base, URL: display, Path: destPath,
		})
	}

	// Stamp the produced assets onto the run so the Media tab renders them the
	// moment the row flips to ok — one data source (useRuns), no extra API.
	h.SetMeta(ctx, map[string]any{"media": items})

	nImg, nVid := 0, 0
	for _, it := range items {
		if it.Kind == "video" {
			nVid++
		} else {
			nImg++
		}
	}
	summary := mediaSummary(nImg, nVid)
	h.Finish(ctx, nil, summary)

	out, _ := json.Marshal(map[string]any{
		"ok":     true,
		"count":  len(items),
		"media":  items,
		"run_id": h.ID(),
		"note":   summary + ". The assets are now in the Media tab and your Library.",
	})
	return string(out), nil
}

func mediaSummary(nImg, nVid int) string {
	parts := []string{}
	if nImg == 1 {
		parts = append(parts, "1 image")
	} else if nImg > 1 {
		parts = append(parts, fmt.Sprintf("%d images", nImg))
	}
	if nVid == 1 {
		parts = append(parts, "1 video")
	} else if nVid > 1 {
		parts = append(parts, fmt.Sprintf("%d videos", nVid))
	}
	if len(parts) == 0 {
		return "Generated media"
	}
	return "Generated " + strings.Join(parts, " + ")
}

// queryEscape minimally encodes a workspace path for the download proxy query
// string. Mirrors urlEscape's intent but also handles '+'.
func queryEscape(s string) string {
	r := strings.NewReplacer(
		" ", "%20", "#", "%23", "?", "%3F", "&", "%26", "=", "%3D", "+", "%2B",
	)
	return r.Replace(s)
}

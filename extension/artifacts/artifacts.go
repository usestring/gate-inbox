// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

// Package artifacts publishes documents an agent makes -- a report, a
// dashboard, a diff review -- to a URL anyone holding the link can open.
//
// It exists because the artifacts built into one CLI belong to the account
// that published them, and this board deliberately rotates work across a
// pool of accounts (see extension.AccountPoolProvider). An artifact published by a
// session on one account is therefore unreadable from a session on the
// next, which is the opposite of what an artifact is for. These tools
// publish to a store the whole team shares instead, and every session gets
// them regardless of which CLI it runs -- including the ones that have no
// artifacts of their own.
//
// The link carries its own key, scoped to one artifact and expiring on its
// own. It is a bearer credential: whoever holds the link is in, and the
// design leans on scope and expiry rather than on identity. When it matters
// who looked, this is the wrong tool.
package artifacts

import (
	"context"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/extension/mcptool"
)

// Name is the extension's config key and its name on the board.
const Name = "artifacts"

// contentTypes is what may be published. The store serves whatever it is
// given back to a browser, so this is an allowlist rather than a guess: an
// agent that mislabels a document gets a refusal, not a download. Notably
// absent is application/octet-stream, which says nothing about what the
// thing is and so cannot be served safely.
var contentTypes = map[string]bool{
	"text/html":        true,
	"text/markdown":    true,
	"text/plain":       true,
	"text/csv":         true,
	"application/json": true,
	"image/svg+xml":    true,
	"image/png":        true,
	"image/jpeg":       true,
	"image/gif":        true,
	"image/webp":       true,
	"application/pdf":  true,
}

type publishArgs struct {
	Title   string `json:"title" jsonschema:"short name for the artifact, shown in listings"`
	Content string `json:"content,omitempty" jsonschema:"the whole document to publish, most often a complete standalone HTML page; for a document already on disk, or a binary one, pass content_path instead"`
	// ContentPath is the document on disk. A page an agent has written to a
	// file would otherwise travel through its context a second time as the
	// call, and an image or a PDF cannot travel as a string at all.
	ContentPath string `json:"content_path,omitempty" jsonschema:"absolute path of the file to publish, used instead of content; the server reads it, and the media type is taken from its extension unless content_type says otherwise"`
	ContentType string `json:"content_type,omitempty" jsonschema:"media type of the document; defaults to text/html for content, and to the extension's type for content_path"`
	ArtifactID  string `json:"artifact_id,omitempty" jsonschema:"id of an artifact to replace in place, keeping links already shared working; omit to publish a new one"`
}

type readArgs struct {
	Artifact string `json:"artifact" jsonschema:"the share link you were given, or the bare id of an artifact this team published"`
	// SaveTo keeps a large or binary artifact out of the conversation: the
	// bytes land in a file and only the metadata comes back.
	SaveTo string `json:"save_to,omitempty" jsonschema:"absolute path to write the artifact's bytes to instead of returning them; use it for an image, a PDF or a page too big to read inline"`
}

// extensionTypes maps a file extension to the media type it is published
// as, so a content_path needs no content_type. Every value is in
// contentTypes; an extension outside this map is refused rather than
// guessed, for the same reason application/octet-stream is.
var extensionTypes = map[string]string{
	".html": "text/html",
	".htm":  "text/html",
	".md":   "text/markdown",
	".txt":  "text/plain",
	".csv":  "text/csv",
	".json": "application/json",
	".svg":  "image/svg+xml",
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".pdf":  "application/pdf",
}

// absPath is the one path rule both file arguments share: absolute, with ~
// as the caller's home. A relative path is refused rather than resolved
// against the server's working directory, which is not the agent's.
func absPath(name, path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("%s: %w", name, err)
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~"))
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%s must be an absolute path, got %q", name, path)
	}
	return path, nil
}

// body resolves what a publish sends: the inline content, or the file at
// content_path with its media type inferred from the extension when none
// was given. Exactly one of the two is taken.
func body(args publishArgs) ([]byte, string, error) {
	contentType := strings.TrimSpace(args.ContentType)
	if strings.TrimSpace(args.ContentPath) == "" {
		if strings.TrimSpace(args.Content) == "" {
			return nil, "", fmt.Errorf("nothing to publish: pass content or content_path")
		}
		if contentType == "" {
			contentType = "text/html"
		}
		return []byte(args.Content), contentType, nil
	}
	if strings.TrimSpace(args.Content) != "" {
		return nil, "", fmt.Errorf("pass content or content_path, not both")
	}
	path, err := absPath("content_path", args.ContentPath)
	if err != nil {
		return nil, "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("content_path: %w", err)
	}
	if len(data) == 0 {
		return nil, "", fmt.Errorf("content_path: %s is empty", path)
	}
	if contentType == "" {
		ext := strings.ToLower(filepath.Ext(path))
		contentType = extensionTypes[ext]
		if contentType == "" {
			return nil, "", fmt.Errorf("cannot tell the media type of %s from its extension; pass content_type (one of %s)", path, strings.Join(allowedTypes(), ", "))
		}
	}
	return data, contentType, nil
}

type listArgs struct {
	Limit int    `json:"limit,omitempty" jsonschema:"how many to return, newest first; defaults to 20"`
	By    string `json:"by,omitempty" jsonschema:"only artifacts published by this email"`
	// IndexLink is opt-in because the link opens every artifact listed.
	// Handed out on every list, it would sit in every conversation that
	// asked what exists.
	IndexLink bool `json:"index_link,omitempty" jsonschema:"also return a link to a page listing every artifact with who made it; valid 24 hours and opens everything listed, so share it only inside the team"`
}

// publisher is who a publish is recorded against.
type publisher struct {
	email   string
	session string
}

// The artifact store's defaults. Only limits have one: which worker to
// publish to, how to read its signing key and who is publishing belong to
// whoever deployed the worker, so they come from the operator's config or
// not at all (see config.DistributionSupplied).
const (
	defaultLinkTTL  = 30 * 24 * time.Hour
	defaultMaxBytes = 16 << 20
)

// storeSettings configures the shared artifact store: HTML and other documents
// an agent publishes to a URL a teammate can open. It is the
// [extensions.artifacts] section of the operator's config.
//
// It is off until an operator turns it on, like a plugin. Three tools in
// every session is context every agent pays whether or not it publishes,
// and a store that is not deployed yet should surface to nobody; the
// operator who wants it says so once, here.
type storeSettings struct {
	// Enabled turns the extension on. Absent means off, so a config
	// written before this section existed keeps the tool list it had.
	Enabled bool `toml:"enabled"`
	// BaseURL is the artifact worker every published link points at, with
	// no trailing slash. It has no default: left out, the extension has
	// nowhere to publish and stays off even when Enabled says otherwise.
	BaseURL string `toml:"base_url"`
	// KeySecret names the secret holding the team's artifact signing key.
	// One key does both jobs: the worker verifies publish requests with
	// it, and read links are signatures it minted. So holding a link never
	// confers the ability to publish, while everyone who can publish can
	// mint a link for anything they published.
	KeySecret string `toml:"key_secret"`
	// KeyCommand prints that secret on stdout, with {secret} standing for
	// KeySecret. It follows tools' account_command rather than reaching
	// for a cloud SDK, so an operator storing the key anywhere only has
	// to write the command that reads it. Left out, there is no key, and
	// the extension stays off.
	KeyCommand string `toml:"key_command"`
	// IdentityCommand prints who is publishing, recorded with every artifact
	// so the team can see who made what. Left out, artifacts are published
	// with no name on them.
	//
	// It is self-reported. Everyone publishes with the same key, so the
	// store records who a publisher says they are and cannot check it.
	IdentityCommand string `toml:"identity_command"`
	// LinkTTL is how long a minted read link stays valid. Links are bearer
	// credentials -- whoever holds one is in -- so they expire rather than
	// accumulate. Unset, thirty days.
	LinkTTL extension.Duration `toml:"link_ttl"`
	// MaxBytes caps one artifact's body. Unset, 16 MiB, matching what a
	// hosted artifact is allowed to be elsewhere; the worker enforces its
	// own cap regardless of what a client believes.
	MaxBytes int64 `toml:"max_bytes"`
}

func (s *storeSettings) applyDefaults() {
	s.BaseURL = strings.TrimRight(s.BaseURL, "/")
	if s.LinkTTL.Duration <= 0 {
		s.LinkTTL.Duration = defaultLinkTTL
	}
	if s.MaxBytes <= 0 {
		s.MaxBytes = defaultMaxBytes
	}
}

// Extension is the artifact store as the registry sees it.
type Extension struct {
	settings storeSettings
}

// New returns the extension, unconfigured. The client is built per
// registration from the settings Configure kept.
func New() *Extension { return &Extension{} }

func (*Extension) Descriptor() extension.Descriptor {
	return extension.Descriptor{ID: Name}
}

// Configure decodes [extensions.artifacts] and fills the defaults in. It
// reads nothing but the section: the signing key is fetched when a tool is
// called, never here.
func (e *Extension) Configure(cfg extension.Config) error {
	var settings storeSettings
	if err := cfg.Decode(&settings); err != nil {
		return err
	}
	settings.applyDefaults()
	e.settings = settings
	return nil
}

// Enabled reports whether this session should get the tools.
//
// Everything checked here is local. Whether the signing key can actually
// be read is a network question, and asking it would put a secret read on
// the launch path of every session the manager spawns; a key that turns
// out to be unreachable is reported when a tool is called instead.
func (e *Extension) Enabled() bool {
	settings := e.settings
	if !settings.Enabled {
		return false
	}
	// Enabled but unconfigured: with no worker named there is nowhere to
	// publish to.
	if strings.TrimSpace(settings.BaseURL) == "" {
		return false
	}
	// No way to reach the key means no way to publish or read, so the
	// tools are absent rather than present and failing even when asked for.
	if strings.TrimSpace(settings.KeyCommand) == "" {
		return false
	}
	_, err := exec.LookPath(strings.Fields(settings.KeyCommand)[0])
	return err == nil
}

func (e *Extension) RegisterMCP(r *extension.Registrar, ctx extension.SessionContext) error {
	settings := e.settings
	client := newClient(
		settings.BaseURL,
		newKeySource(settings.KeyCommand, settings.KeySecret),
		settings.LinkTTL.Duration,
		settings.MaxBytes,
	)
	identity := newIdentitySource(settings.IdentityCommand)
	session := ctx.SessionID

	if err := extension.AddTool(r, &mcp.Tool{
		Name: "publish_artifact",
		Description: "Publish a document to a URL you can hand to the user or to another agent: a report, a dashboard, a rendered diff, a standalone HTML page. " +
			"Use it whenever finished work is meant to be looked at by someone other than you, and would otherwise only exist in this terminal or in a file on this machine. " +
			"The returned link carries its own key, so whoever has it can open that one artifact and nothing else, and it works from any session on any account and any CLI -- which the artifacts built into a single CLI do not, since those belong to the account that made them. " +
			"A document already written to disk, or a binary one such as a PNG or a PDF, is published by naming it in content_path rather than pasting it into content; the media type follows the extension. " +
			"Pass artifact_id to revise something already published: the link already shared keeps working and shows the new version. " +
			"The link is a bearer credential with an expiry, not an identity check, so do not publish anything whose disclosure would matter if the link were forwarded.",
		Annotations: mcptool.Annotations(false, false, true),
	}, func(callCtx context.Context, _ *mcp.CallToolRequest, args publishArgs) (*mcp.CallToolResult, any, error) {
		link, err := publish(callCtx, client, publisher{email: identity.get(), session: session}, args)
		if err != nil {
			return nil, nil, err
		}
		return mcptool.Text(link), nil, nil
	}); err != nil {
		return err
	}

	if err := extension.AddTool(r, &mcp.Tool{
		Name: "read_artifact",
		Description: "Read back an artifact this team published, given the share link you were handed or a bare artifact id. " +
			"Use it when another session, another agent or the user refers you to an artifact link and you need what is in it rather than a description of it. " +
			"This is the half that makes artifacts shared rather than merely published: the session that reads need not be the one that wrote, on the same account, or even on the same CLI. " +
			"Pass save_to to write the bytes to a file instead of returning them, which is how an image, a PDF or a page too big to read inline comes back.",
		Annotations: mcptool.Annotations(true, false, true),
	}, func(callCtx context.Context, _ *mcp.CallToolRequest, args readArgs) (*mcp.CallToolResult, any, error) {
		data, meta, err := client.Fetch(callCtx, args.Artifact)
		if err != nil {
			return nil, nil, err
		}
		if strings.TrimSpace(args.SaveTo) != "" {
			saved, err := save(args.SaveTo, data)
			if err != nil {
				return nil, nil, err
			}
			return mcptool.Text(formatSaved(meta, saved)), meta, nil
		}
		return mcptool.Text(formatRead(meta, data)), meta, nil
	}); err != nil {
		return err
	}

	if err := extension.AddTool(r, &mcp.Tool{
		Name: "list_artifacts",
		Description: "List what this team has published to the artifact store, newest first, with ids, titles and who published each. " +
			"Call it to find an artifact the user half-remembers, to see what one person has published by passing their email as by, or to check whether the thing you are about to write already exists, before publishing a second copy of it.",
		Annotations: mcptool.Annotations(true, false, true),
	}, func(callCtx context.Context, _ *mcp.CallToolRequest, args listArgs) (*mcp.CallToolResult, any, error) {
		limit := args.Limit
		if limit <= 0 {
			limit = 20
		}
		listed, err := client.List(callCtx, limit, args.By)
		if err != nil {
			return nil, nil, err
		}
		out := formatList(listed)
		if args.IndexLink {
			link, err := client.IndexLink()
			if err != nil {
				return nil, nil, err
			}
			out += "\n\nBrowse all, valid 24h (opens every artifact listed; keep it inside the team):\n" + link
		}
		return mcptool.Text(out), listed, nil
	}); err != nil {
		return err
	}

	return nil
}

func publish(ctx context.Context, client *storeClient, by publisher, args publishArgs) (string, error) {
	if strings.TrimSpace(args.Title) == "" {
		return "", fmt.Errorf("an artifact needs a title")
	}
	content, contentType, err := body(args)
	if err != nil {
		return "", err
	}
	if !contentTypes[contentType] {
		return "", fmt.Errorf("cannot publish %s; allowed: %s", contentType, strings.Join(allowedTypes(), ", "))
	}

	id := strings.TrimSpace(args.ArtifactID)
	if id == "" {
		fresh, err := newID()
		if err != nil {
			return "", err
		}
		id = fresh
	}

	link, err := client.Publish(ctx, id, content, artifactMeta{
		Title:       args.Title,
		ContentType: contentType,
		Email:       by.email,
		Session:     by.session,
		PublishedAt: time.Now().UTC().Format(time.RFC3339),
		ID:          id,
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("published %q\nid: %s\n%s", args.Title, id, link), nil
}

func allowedTypes() []string {
	return slices.Sorted(maps.Keys(contentTypes))
}

func formatRead(meta artifactMeta, body []byte) string {
	return fmt.Sprintf("%s\n\n%s", readHeader(meta), body)
}

func formatSaved(meta artifactMeta, path string) string {
	return fmt.Sprintf("%s\nsaved to %s", readHeader(meta), path)
}

func readHeader(meta artifactMeta) string {
	header := meta.Title
	if header == "" {
		header = meta.ID
	}
	return fmt.Sprintf("%s (%s, %d bytes)", header, meta.ContentType, meta.Bytes)
}

// save writes a fetched artifact where the caller asked. The file is the
// caller's alone: it may hold whatever the artifact did, and the link that
// fetched it was scoped to one reader.
func save(to string, data []byte) (string, error) {
	path, err := absPath("save_to", to)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("save_to: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("save_to: %w", err)
	}
	return path, nil
}

func formatList(listed []artifactMeta) string {
	if len(listed) == 0 {
		return "no artifacts published yet"
	}
	var out strings.Builder
	for _, meta := range listed {
		by := orUnknown(meta.Email)
		fmt.Fprintf(&out, "%s  %s  by %s", meta.ID, meta.Title, by)
		if meta.PublishedAt != "" {
			fmt.Fprintf(&out, "  (%s)", meta.PublishedAt)
		}
		if meta.Revisions > 1 {
			fmt.Fprintf(&out, "  · %d versions, last by %s", meta.Revisions, orUnknown(meta.UpdatedBy))
		}
		out.WriteString("\n")
	}
	return strings.TrimRight(out.String(), "\n")
}

func orUnknown(email string) string {
	if email == "" {
		return "unknown publisher"
	}
	return email
}

package artifacts

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// requestTimeout bounds one call to the worker. An artifact is a document,
// not a job, so a call that has not finished by now is a call that failed.
const requestTimeout = 30 * time.Second

// readLimit caps what Fetch will pull into a tool result regardless of what
// the worker serves, so a large artifact cannot blow up the context of the
// session that reads it. An artifact over it is refused rather than
// truncated: half an HTML document reads as a whole one, and an agent
// cannot tell it was shortened.
const readLimit = 2 << 20

// Meta travels beside the body. It is what list shows and what a reader is
// told about an artifact it did not publish.
type Meta struct {
	Title       string `json:"title,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	// Email is who published it, as their identity command reported, and
	// Session the board session they published from. Both are stored with
	// the artifact rather than in the link, since a link may leave the team
	// and these should not.
	Email       string `json:"email,omitempty"`
	Session     string `json:"session,omitempty"`
	PublishedAt string `json:"published_at,omitempty"`
	// UpdatedBy and UpdatedAt are the latest revision's, and Revisions how
	// many there have been. The store reports them; a publish never sends
	// them, since who made an artifact and who last changed it are the
	// index's to record rather than a client's to claim.
	UpdatedBy string `json:"updated_by,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
	Revisions int    `json:"revisions,omitempty"`
	Bytes     int64  `json:"bytes,omitempty"`
	ID        string `json:"id,omitempty"`
}

// indexScope is what an index link is signed for: the page listing every
// artifact. It is too short to be an artifact id, so the two kinds of key
// never open each other's pages.
const indexScope = "__index__"

// indexTTL is how long an index link lives. It opens every artifact it
// lists, so it is kept to a day where an artifact link lasts a month.
const indexTTL = 24 * time.Hour

// Client talks to the artifact worker. Its zero value is not usable; build
// one with newClient.
type Client struct {
	baseURL string
	keys    *keySource
	ttl     time.Duration
	maxBody int64
	http    *http.Client
	now     func() time.Time
}

func newClient(baseURL string, keys *keySource, ttl time.Duration, maxBody int64) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		keys:    keys,
		ttl:     ttl,
		maxBody: maxBody,
		http:    &http.Client{Timeout: requestTimeout},
		now:     time.Now,
	}
}

// NewID returns an artifact id.
//
// 128 bits from crypto/rand, so the id is not guessable even before the
// key is checked. That is defence in depth rather than the control: the
// signature on the link is what actually gates a read.
func NewID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return b64(raw), nil
}

// Link is the shareable URL for one artifact: the address plus its key.
func (c *Client) Link(id string) (string, error) {
	key, err := c.keys.get()
	if err != nil {
		return "", err
	}
	token, err := MintLink(key, id, c.now().Add(c.ttl))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s/a/%s?%s=%s", c.baseURL, id, LinkParam, token), nil
}

// IndexLink is a link to the page listing every artifact, with who made
// each. The links on the page expire with it.
func (c *Client) IndexLink() (string, error) {
	key, err := c.keys.get()
	if err != nil {
		return "", err
	}
	token, err := MintLink(key, indexScope, c.now().Add(indexTTL))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s/?%s=%s", c.baseURL, LinkParam, token), nil
}

// Publish stores one artifact and returns its shareable link. An id that
// already exists is replaced, which is how an artifact is revised without
// invalidating the link already handed out.
func (c *Client) Publish(ctx context.Context, id string, body []byte, meta Meta) (string, error) {
	if int64(len(body)) > c.maxBody {
		return "", fmt.Errorf("artifact is %d bytes, over the %d byte limit", len(body), c.maxBody)
	}
	key, err := c.keys.get()
	if err != nil {
		return "", err
	}
	path := "/a/" + id
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(meta)
	if err != nil {
		return "", err
	}
	// Metadata rides as one base64 header rather than several plain ones:
	// a title is user text, and a raw header value cannot carry a newline
	// or anything outside latin-1 without corrupting the request.
	req.Header.Set("X-Artifact-Meta", b64(encoded))
	req.Header.Set("Content-Type", meta.ContentType)
	req.Header.Set(AuthHeader, SignRequest(key, http.MethodPut, path, c.now(), body))

	if err := c.do(req, nil); err != nil {
		return "", err
	}
	return c.Link(id)
}

// Fetch reads an artifact back, taking either a full share link or a bare
// id. A link is what another session was handed; an id is what this
// session published itself, and it can mint its own key for that.
func (c *Client) Fetch(ctx context.Context, target string) ([]byte, Meta, error) {
	address, err := c.address(target)
	if err != nil {
		return nil, Meta{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, Meta{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, Meta{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, Meta{}, statusError(resp)
	}
	// One byte past the limit, so hitting it is distinguishable from
	// landing exactly on it.
	body, err := io.ReadAll(io.LimitReader(resp.Body, readLimit+1))
	if err != nil {
		return nil, Meta{}, err
	}
	if len(body) > readLimit {
		return nil, Meta{}, fmt.Errorf("artifact is larger than %d bytes, too large to read into this conversation; open it at %s", readLimit, address)
	}
	meta := Meta{ContentType: resp.Header.Get("Content-Type"), Bytes: int64(len(body))}
	if raw := resp.Header.Get("X-Artifact-Meta"); raw != "" {
		if decoded, err := decodeMeta(raw); err == nil {
			decoded.Bytes = int64(len(body))
			meta = decoded
		}
	}
	return body, meta, nil
}

// List returns what the team has published, newest first, narrowed to one
// publisher when by is an email.
func (c *Client) List(ctx context.Context, limit int, by string) ([]Meta, error) {
	key, err := c.keys.get()
	if err != nil {
		return nil, err
	}
	path := "/a"
	query := url.Values{}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	if by = strings.TrimSpace(by); by != "" {
		query.Set("by", by)
	}
	address := c.baseURL + path
	if len(query) > 0 {
		address += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	// The path is signed without the query string, so the signature covers
	// what is being asked for rather than how much of it.
	req.Header.Set(AuthHeader, SignRequest(key, http.MethodGet, path, c.now(), nil))

	var listed struct {
		Artifacts []Meta `json:"artifacts"`
	}
	if err := c.do(req, &listed); err != nil {
		return nil, err
	}
	return listed.Artifacts, nil
}

// address turns a share link or a bare id into a URL this client can GET,
// minting a key when it was given an id.
func (c *Client) address(target string) (string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", fmt.Errorf("no artifact given")
	}
	if !strings.Contains(target, "/") {
		return c.Link(target)
	}
	parsed, err := url.Parse(target)
	if err != nil {
		return "", fmt.Errorf("not an artifact link: %s", target)
	}
	if parsed.Query().Get(LinkParam) == "" {
		// A link with no key is one someone stripped, or an id pasted
		// with its host. Either way this session may be able to mint a
		// key for it, so fall back to the id rather than refusing.
		id := strings.TrimPrefix(parsed.EscapedPath(), "/a/")
		if id == "" || strings.Contains(id, "/") {
			return "", fmt.Errorf("not an artifact link: %s", target)
		}
		return c.Link(id)
	}
	return target, nil
}

func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return statusError(resp)
	}
	if out == nil {
		_, err := io.Copy(io.Discard, io.LimitReader(resp.Body, readLimit))
		return err
	}
	return json.NewDecoder(io.LimitReader(resp.Body, readLimit)).Decode(out)
}

func decodeMeta(header string) (Meta, error) {
	raw, err := decodeB64(header)
	if err != nil {
		return Meta{}, err
	}
	var meta Meta
	err = json.Unmarshal(raw, &meta)
	return meta, err
}

// statusError reports what the worker said, trimmed, so a failure names a
// cause rather than a number.
func statusError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	detail := strings.TrimSpace(string(body))
	if detail == "" {
		return fmt.Errorf("artifact store returned %s", resp.Status)
	}
	return fmt.Errorf("artifact store returned %s: %s", resp.Status, detail)
}

package readwisereader

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/http/httputil"
	"os"
	"strconv"
	"time"

	"github.com/google/go-querystring/query"
)

const (
	addr = "https://readwise.io/api/v3"
)

type Client struct {
	client http.Client
	token  string
}

func NewClient(token string) *Client {
	return &Client{
		client: http.Client{
			Transport: &authTransport{
				Transport:           http.DefaultTransport.(*http.Transport),
				authorizationHeader: fmt.Sprintf("Token %s", token),
			},
		},
		token: token,
	}
}

func (c *Client) List(ctx context.Context, params ListParams) (*ListResponse, error) {
	resp, err := c.list(ctx, params)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		retryAfter := resp.Header.Get("Retry-After")
		seconds, err := strconv.Atoi(retryAfter)
		if err != nil {
			return nil, fmt.Errorf("invalid retry-after header: %v: %w", retryAfter, err)
		}

		errRateLimited := &ErrorRateLimited{
			RetryAfter: time.Duration(seconds) * time.Second,
		}

		return nil, errRateLimited
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var lr listResponse
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		return nil, err
	}

	r := lr.toListResponse()
	return &r, nil
}

func (c *Client) ListPaginate(ctx context.Context, params ListParams) iter.Seq2[Page, error] {
	return func(yield func(Page, error) bool) {
		cursor := params.PageCursor
		for {
			params.PageCursor = cursor
			resp, err := c.List(ctx, params)
			var rle *ErrorRateLimited
			if errors.As(err, &rle) {
				if ctx.Err() != nil {
					yield(Page{}, ctx.Err())
					return
				}

				// TODO: make this configurable or smth, a callback maybe?
				select {
				case <-time.After(rle.RetryAfter):
					continue
				case <-ctx.Done():
					yield(Page{}, ctx.Err())
					return
				}
			}

			if err != nil {
				yield(Page{}, err)
				return
			}

			if !yield(resp.Page, nil) {
				return
			}

			if resp.NextPageCursor == "" {
				return
			}

			cursor = resp.NextPageCursor
		}
	}
}

func (c *Client) list(ctx context.Context, params ListParams) (*http.Response, error) {
	const url = addr + "/list"
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	q, err := query.Values(params)
	if err != nil {
		return nil, err
	}

	req.URL.RawQuery = q.Encode()

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do: %w", err)
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("readall: %w", err)
	}

	resp.Body = io.NopCloser(bytes.NewReader(b))
	return resp, nil
}

type authTransport struct {
	*http.Transport
	authorizationHeader string
}

var _ http.RoundTripper = (*authTransport)(nil)

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", t.authorizationHeader)
	// TODO: use slog
	debug := os.Getenv("READWISE_DEBUG") != ""
	resp, err := t.Transport.RoundTrip(req)

	if debug {
		reqdump, _ := httputil.DumpRequestOut(req, true)
		fmt.Println(string(reqdump))

		respdump, _ := httputil.DumpResponse(resp, true)
		fmt.Println(string(respdump))
	}

	return resp, err
}

type Location string

const (
	LocationNew       = "new"
	LocationLater     = "later"
	LocationShortList = "shortlist"
	LocationArchive   = "archive"
	LocationFeed      = "feed"
)

type Category string

const (
	CategoryArticle   = "article"
	CategoryEmail     = "email"
	CategoryRSS       = "rss"
	CategoryHighlight = "highlight"
	CategoryNote      = "note"
	CategoryPDF       = "pdf"
	CategoryEPUB      = "epub"
	CategoryTweet     = "tweet"
	CategoryVideo     = "video"
)

type ListParams struct {
	ID              string    `url:"id,omitempty"`
	UpdatedAfter    time.Time `url:"updatedAfter,omitempty"`
	Location        Location  `url:"location,omitempty"`
	Category        Category  `url:"category,omitempty"`
	PageCursor      string    `url:"pageCursor,omitempty"`
	WithHTMLContent bool      `url:"withHTMLContent,omitempty"`
}

type listResponse struct {
	Count          int        `json:"count"`
	NextPageCursor string     `json:"nextPageCursor"`
	Results        []document `json:"results"`
}

func (lr *listResponse) toListResponse() ListResponse {
	results := make([]Document, 0, len(lr.Results))
	for _, dr := range lr.Results {
		results = append(results, dr.toDocument())
	}

	return ListResponse{
		NextPageCursor: lr.NextPageCursor,
		Page: Page{
			Count:   lr.Count,
			Results: results,
		},
	}
}

type ListResponse struct {
	// Cursor for the next page of results
	NextPageCursor string
	Page
}

type Page struct {
	// Total number of documents
	Count int
	// List of documents in the current page
	Results []Document
}

type Document struct {
	ID              string
	URL             string
	SourceURL       string
	Title           string
	Author          string
	Source          string
	Category        Category
	Location        Location
	Tags            map[string]any
	SiteName        string
	WordCount       int
	CreatedAt       time.Time
	UpdatedAt       time.Time
	Notes           string
	PublishedDate   time.Time
	Summary         string
	ImageURL        string
	ParentID        string
	ReadingProgress float64
	FirstOpenedAt   time.Time
	LastOpenedAt    time.Time
	SavedAt         time.Time
	LastMovedAt     time.Time
}

type document struct {
	ID        string   `json:"id"`
	URL       string   `json:"url"`
	SourceURL string   `json:"source_url"`
	Title     string   `json:"title"`
	Author    string   `json:"author"`
	Source    string   `json:"source"`
	Category  Category `json:"category"`
	Location  Location `json:"location"`
	// NOTE: Doc says this is a []string, but actual response is an object with
	// some metadata. Roll with any who cares.
	Tags            map[string]any `json:"tags"`
	SiteName        string         `json:"site_name"`
	WordCount       int            `json:"word_count"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	Notes           string         `json:"notes"`
	PublishedDate   dateValue      `json:"published_date"`
	Summary         string         `json:"summary"`
	ImageURL        string         `json:"image_url"`
	ParentID        string         `json:"parent_id"`
	ReadingProgress float64        `json:"reading_progress"`
	FirstOpenedAt   time.Time      `json:"first_opened_at"`
	LastOpenedAt    time.Time      `json:"last_opened_at"`
	SavedAt         time.Time      `json:"saved_at"`
	LastMovedAt     time.Time      `json:"last_moved_at"`
}

func (dr *document) toDocument() Document {
	return Document{
		ID:              dr.ID,
		URL:             dr.URL,
		SourceURL:       dr.SourceURL,
		Title:           dr.Title,
		Author:          dr.Author,
		Source:          dr.Source,
		Category:        dr.Category,
		Location:        dr.Location,
		Tags:            dr.Tags,
		SiteName:        dr.SiteName,
		WordCount:       dr.WordCount,
		CreatedAt:       dr.CreatedAt,
		UpdatedAt:       time.Time(dr.UpdatedAt),
		Notes:           dr.Notes,
		PublishedDate:   time.Time(dr.PublishedDate),
		Summary:         dr.Summary,
		ImageURL:        dr.ImageURL,
		ParentID:        dr.ParentID,
		ReadingProgress: dr.ReadingProgress,
		FirstOpenedAt:   time.Time(dr.FirstOpenedAt),
		LastOpenedAt:    time.Time(dr.LastOpenedAt),
		SavedAt:         time.Time(dr.SavedAt),
		LastMovedAt:     time.Time(dr.LastMovedAt),
	}
}

type dateValue time.Time

func (tv *dateValue) UnmarshalJSON(data []byte) error {
	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}

	switch value := v.(type) {
	case float64:
		*tv = dateValue(time.UnixMilli(int64(value)))
	case string:
		t, err := time.Parse("2006-01-02", value)
		if err != nil {
			return err
		}
		*tv = dateValue(t)
	case nil:
		*tv = dateValue(time.Time{})
	default:
		return fmt.Errorf("invalid time value type: %T", v)
	}

	return nil
}

type ErrorRateLimited struct {
	RetryAfter time.Duration
}

var _ error = (*ErrorRateLimited)(nil)

func (e *ErrorRateLimited) Error() string {
	return fmt.Sprintf("rate limited, retry after: %s", e.RetryAfter)
}

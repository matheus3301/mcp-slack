package slackclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/slack-go/slack"
)

// NewCollaboration constructs the same pinned SDK with transport bounds. There
// is no automatic network retry; only explicit HTTP 429s may be retried by Call.
func NewCollaboration(token string, opts ...Option) *slack.Client {
	o := options{httpClient: &http.Client{Timeout: 30 * time.Second}}
	for _, fn := range opts {
		fn(&o)
	}
	client := collaborationHTTPClient(o.httpClient)
	options := []slack.Option{slack.OptionHTTPClient(client)}
	if o.apiURL != "" {
		options = append(options, slack.OptionAPIURL(o.apiURL))
	}
	return slack.New(token, options...)
}
func collaborationHTTPClient(source *http.Client) *http.Client {
	client := *source
	client.Timeout = 30 * time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client.Transport = boundedTransport{base: transport}
	return &client
}

// Call retries only explicit rate-limit rejections, not timeouts, transport
// errors, server errors, or entire multi-step operations.
func Call(ctx context.Context, fn func() error) error {
	return withRetry(ctx, DefaultRetryPolicy, nil, fn)
}

const maxResponseBytes = 8 << 20

type boundedTransport struct{ base http.RoundTripper }

func (b boundedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := b.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if resp.ContentLength > maxResponseBytes {
		_ = resp.Body.Close() // Preserve the size-limit error if closing also fails.
		return nil, errors.New("response too large")
	}
	resp.Body = &boundedBody{ReadCloser: resp.Body, remaining: maxResponseBytes}
	return resp, nil
}

type boundedBody struct {
	io.ReadCloser
	remaining int64
}

func (b *boundedBody) Read(p []byte) (int, error) {
	if b.remaining <= 0 {
		return 0, errors.New("response too large")
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, e := b.ReadCloser.Read(p)
	b.remaining -= int64(n)
	return n, e
}

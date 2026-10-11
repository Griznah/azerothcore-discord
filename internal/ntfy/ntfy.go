// Package ntfy publishes notifications to an ntfy server (https://docs.ntfy.sh).
//
// Publishing is a plain HTTP POST per https://docs.ntfy.sh/publish/: the
// topic is the URL path, the message is the body, and title/priority/tags
// ride in headers.
package ntfy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client publishes messages to one ntfy server.
type Client struct {
	server string
	token  string
	http   *http.Client
}

// New builds a client for server (e.g. https://ntfy.sh) with an optional
// Bearer token for protected topics.
func New(server, token string) *Client {
	return &Client{
		server: server,
		token:  token,
		http:   &http.Client{Timeout: 10 * time.Second},
	}
}

// Publish sends message to topic. title, priority and tags map to the ntfy
// Title, Priority and Tags headers; priority and tags may be empty to omit them.
func (c *Client) Publish(ctx context.Context, topic, title, message, priority, tags string) error {
	url := strings.TrimSuffix(c.server, "/") + "/" + topic
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(message))
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Title", title)
	if priority != "" {
		req.Header.Set("Priority", priority)
	}
	if tags != "" {
		req.Header.Set("Tags", tags)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("ntfy publish to %s failed: %s", url, resp.Status)
	}
	return nil
}

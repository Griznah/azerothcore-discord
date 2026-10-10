// Package soap is a minimal AzerothCore SOAP (remote access) client.
//
// Per https://www.azerothcore.org/wiki/remote-access: HTTP POST to
// http://host:port/ with Basic auth, XML envelope, answer in <result>.
package soap

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const envelope = `<?xml version="1.0" encoding="UTF-8"?>
<SOAP-ENV:Envelope xmlns:SOAP-ENV="http://schemas.xmlsoap.org/soap/envelope/"
 xmlns:SOAP-ENC="http://schemas.xmlsoap.org/soap/encoding/"
 xmlns:xsi="http://www.w3.org/1999/XMLSchema-instance"
 xmlns:xsd="http://www.w3.org/1999/XMLSchema"
 xmlns:ns1="urn:AC">
<SOAP-ENV:Body><ns1:executeCommand><command>%s</command></ns1:executeCommand></SOAP-ENV:Body>
</SOAP-ENV:Envelope>`

var (
	resultRe = regexp.MustCompile(`(?s)<result>(.*?)</result>`)
	faultRe  = regexp.MustCompile(`(?s)<faultstring>(.*?)</faultstring>`)
)

// Client talks to one worldserver SOAP endpoint.
type Client struct {
	url  string
	user string
	pass string
	http *http.Client
}

// New builds a client for http://host:port/ with the given credentials and
// per-request timeout.
func New(host string, port int, user, pass string, timeout time.Duration) *Client {
	return &Client{
		url:  fmt.Sprintf("http://%s:%d/", host, port),
		user: user,
		pass: pass,
		http: &http.Client{Timeout: timeout},
	}
}

// Execute sends one console command and returns the normalized response text.
// SOAP faults and HTTP errors become errors; fault text is preserved.
func (c *Client) Execute(ctx context.Context, command string) (string, error) {
	body := fmt.Sprintf(envelope, escapeCommand(command))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, strings.NewReader(body))
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(c.user, c.pass)
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("cannot reach %s: %w", c.url, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	xml := string(data)

	// SOAP faults can arrive with any status code; parse before giving up.
	if m := faultRe.FindStringSubmatch(xml); m != nil {
		return "", fmt.Errorf("soap fault: %s", Normalize(m[1]))
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d from %s", resp.StatusCode, c.url)
	}

	m := resultRe.FindStringSubmatch(xml)
	if m == nil {
		return "", fmt.Errorf("no <result> in response: %.500s", xml)
	}
	return Normalize(m[1]), nil
}

func escapeCommand(cmd string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(cmd)
}

// Normalize converts SOAP line endings to plain \n and drops empty lines.
// AC sends &#xD; markers (sometimes mixed with literal \r\n).
func Normalize(text string) string {
	text = strings.ReplaceAll(text, "&#xD;", "\n")
	text = strings.ReplaceAll(text, "&#xd;", "\n")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	out := lines[:0]
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

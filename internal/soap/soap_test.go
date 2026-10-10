package soap

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// newTestClient wires a Client at a local httptest server URL.
func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse test server url: %v", err)
	}
	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	return New(host, port, "soapuser", "soappass", 5*time.Second)
}

const soapResponse = `<SOAP-ENV:Envelope xmlns:SOAP-ENV="http://schemas.xmlsoap.org/soap/envelope/"><SOAP-ENV:Body><ns1:executeCommandResponse><return>%s</return></ns1:executeCommandResponse></SOAP-ENV:Body></SOAP-ENV:Envelope>`

func TestExecuteBasicAuthPostAndResult(t *testing.T) {
	var gotMethod, gotUser, gotPass, gotBody, gotContentType string
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotUser, gotPass, _ = r.BasicAuth()
		gotContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		fmt.Fprintf(w, soapResponse,
			`<result>Server uptime: 2 Hours&#xD;Connected players: 5 (peak: 6).&#xD;&#xD;</result>`)
	})

	out, err := client.Execute(context.Background(), "server info")
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Fatalf("expected POST, got %s", gotMethod)
	}
	if gotUser != "soapuser" || gotPass != "soappass" {
		t.Fatalf("expected basic auth soapuser/soappass, got %s/%s", gotUser, gotPass)
	}
	if !strings.Contains(gotContentType, "text/xml") {
		t.Fatalf("expected text/xml content type, got %q", gotContentType)
	}
	if !strings.Contains(gotBody, "<command>server info</command>") {
		t.Fatalf("envelope must carry the command, got: %.300s", gotBody)
	}
	if !strings.Contains(gotBody, `xmlns:ns1="urn:AC"`) {
		t.Fatal("envelope must declare urn:AC")
	}

	// &#xD; normalized to \n, empty lines dropped
	want := "Server uptime: 2 Hours\nConnected players: 5 (peak: 6)."
	if out != want {
		t.Fatalf("Execute output = %q, want %q", out, want)
	}
}

func TestExecuteCommandEscaping(t *testing.T) {
	var gotBody string
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		fmt.Fprintf(w, soapResponse, `<result>ok</result>`)
	})

	if _, err := client.Execute(context.Background(), "a<b&c>d"); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(gotBody, "<command>a&lt;b&amp;c&gt;d</command>") {
		t.Fatalf("command must be XML-escaped, got: %.300s", gotBody)
	}
}

func TestExecuteFault(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, soapResponse, `<faultstring>That command could not be&#xD;found</faultstring>`)
	})

	_, err := client.Execute(context.Background(), "server info")
	if err == nil {
		t.Fatal("expected error on SOAP fault")
	}
	if !strings.Contains(err.Error(), "soap fault") {
		t.Fatalf("error should carry fault prefix: %v", err)
	}
	// fault text is normalized
	if want := "soap fault: That command could not be\nfound"; err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

func TestExecuteHTTPErrorWithoutFault(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})

	_, err := client.Execute(context.Background(), "server info")
	if err == nil || !strings.Contains(err.Error(), "HTTP 502") {
		t.Fatalf("expected HTTP 502 error, got: %v", err)
	}
}

func TestExecuteNoResult(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<html>not soap</html>")
	})

	_, err := client.Execute(context.Background(), "server info")
	if err == nil || !strings.Contains(err.Error(), "no <result>") {
		t.Fatalf("expected no-result error, got: %v", err)
	}
}

func TestExecuteUnreachable(t *testing.T) {
	// port 1 on localhost: connection refused
	client := New("127.0.0.1", 1, "u", "p", 500*time.Millisecond)
	if _, err := client.Execute(context.Background(), "server info"); err == nil {
		t.Fatal("expected error for unreachable server")
	}
}

// Fixture: current AC master shape with a queue.
const fixtureWithQueue = `AzerothCore rev. abc1234 2026-09-27 (win)
Connected players: 130 (peak: 140). Queue: 7.
Characters in world: 123 (peak: 150, lifetime: 200).
Update time diff: 4ms. Last 50 diffs summary:
Server uptime: 1 Day 2 Hours 15 Minutes`

// Fixture: shape without a queue line (AC omits it when the queue is empty).
const fixtureWithoutQueue = `AzerothCore rev. abc1234 2026-09-27 (win)
Connected players: 42 (peak: 50).
Characters in world: 40 (peak: 55, lifetime: 90).
Server uptime: 2 Hours`

func TestParseServerInfoWithQueue(t *testing.T) {
	info := ParseServerInfo(fixtureWithQueue)
	if info.Uptime != "1 Day 2 Hours 15 Minutes" {
		t.Fatalf("uptime = %q", info.Uptime)
	}
	if info.Queue == nil || *info.Queue != 7 {
		t.Fatalf("queue = %v, want 7", info.Queue)
	}
	if info.PlayersOnline == nil || *info.PlayersOnline != 130 {
		t.Fatalf("players = %v, want 130", info.PlayersOnline)
	}
}

func TestParseServerInfoWithoutQueue(t *testing.T) {
	info := ParseServerInfo(fixtureWithoutQueue)
	if info.Uptime != "2 Hours" {
		t.Fatalf("uptime = %q", info.Uptime)
	}
	if info.Queue != nil {
		t.Fatalf("queue must be nil without a queue line, got %v", *info.Queue)
	}
	if info.PlayersOnline == nil || *info.PlayersOnline != 42 {
		t.Fatalf("players = %v, want 42", info.PlayersOnline)
	}
}

func TestParseServerInfoLegacyShapes(t *testing.T) {
	info := ParseServerInfo("Players online: 12\nServer uptime: 1d 2h")
	if info.PlayersOnline == nil || *info.PlayersOnline != 12 {
		t.Fatalf("players = %v, want 12", info.PlayersOnline)
	}

	info = ParseServerInfo("Player count: 3 (Queue: 2)")
	if info.PlayersOnline == nil || *info.PlayersOnline != 3 {
		t.Fatalf("players = %v, want 3", info.PlayersOnline)
	}
	if info.Queue == nil || *info.Queue != 2 {
		t.Fatalf("queue = %v, want 2", info.Queue)
	}
}

func TestParseServerInfoEmpty(t *testing.T) {
	info := ParseServerInfo("")
	if info.Uptime != "" || info.Queue != nil || info.PlayersOnline != nil {
		t.Fatalf("empty input must yield zero-value info, got %+v", info)
	}
}

func TestNormalize(t *testing.T) {
	got := Normalize("a&#xD;b\r\nc\n\n&#xd;d\n")
	if want := "a\nb\nc\nd"; got != want {
		t.Fatalf("Normalize = %q, want %q", got, want)
	}
}

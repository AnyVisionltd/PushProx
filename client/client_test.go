// Copyright 2020 The Prometheus Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package client

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/common/promslog"
)

func prepareTest() (*httptest.Server, *Coordinator) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "GET /index.html HTTP/1.0\n\nOK")
	}))
	c, _ := NewCoordinator(promslog.NewNopLogger(), nil, ts.Client(), ts.URL, ts.URL)

	return ts, c
}

func TestDoScrape(t *testing.T) {
	ts, c := prepareTest()
	defer ts.Close()

	req, err := http.NewRequest("GET", ts.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Add("X-Prometheus-Scrape-Timeout-Seconds", "10.0")
	c.doScrape(req)
}

func TestHandleErr(t *testing.T) {
	ts, c := prepareTest()
	defer ts.Close()

	req, err := http.NewRequest("GET", ts.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.handleErr(req, errors.New("test error"))
}

func TestLoop(t *testing.T) {
	ts, c := prepareTest()
	defer ts.Close()
	if err := c.doPoll(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestDoPollRejectsNonOKResponseBeforeParsing(t *testing.T) {
	const responseBody = `{"message":"Service Unavailable"}`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, responseBody)
	}))
	defer ts.Close()

	var logs bytes.Buffer
	c, err := NewCoordinator(slog.New(slog.NewTextHandler(&logs, nil)), nil, ts.Client(), "test.example", ts.URL)
	if err != nil {
		t.Fatal(err)
	}

	err = c.doPoll(context.Background())
	if err == nil {
		t.Fatal("doPoll() returned nil error for a non-200 response")
	}
	if got, want := err.Error(), `poll returned HTTP 503: {"message":"Service Unavailable"}`; got != want {
		t.Fatalf("doPoll() error = %q, want %q", got, want)
	}
	if got := logs.String(); !strings.Contains(got, "Poll request rejected by proxy") ||
		!strings.Contains(got, "status=503") ||
		!strings.Contains(got, `body="{\"message\":\"Service Unavailable\"}"`) {
		t.Fatalf("doPoll() log does not contain the response status and body: %s", got)
	}
	if got := logs.String(); strings.Contains(got, "Error reading request") || strings.Contains(got, "malformed HTTP request") {
		t.Fatalf("doPoll() attempted to parse a non-200 response as an HTTP request: %s", got)
	}
}

func TestDoPollTruncatesRejectedResponseBody(t *testing.T) {
	const maxBodySnippetLength = 512
	responseBody := strings.Repeat("a", maxBodySnippetLength) + "not-included"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, responseBody)
	}))
	defer ts.Close()

	var logs bytes.Buffer
	c, err := NewCoordinator(slog.New(slog.NewTextHandler(&logs, nil)), nil, ts.Client(), "test.example", ts.URL)
	if err != nil {
		t.Fatal(err)
	}

	err = c.doPoll(context.Background())
	if err == nil {
		t.Fatal("doPoll() returned nil error for a non-200 response")
	}
	if got := err.Error(); got != "poll returned HTTP 403: "+strings.Repeat("a", maxBodySnippetLength) {
		t.Fatalf("doPoll() returned an unexpected truncated body: %q", got)
	}
	if strings.Contains(logs.String(), "not-included") {
		t.Fatalf("doPoll() logged more than %d response-body bytes: %s", maxBodySnippetLength, logs.String())
	}
}

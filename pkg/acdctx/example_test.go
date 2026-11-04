package acdctx_test

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/fsvxavier/accordion-context/pkg/acdctx"
)

// ExampleClient_Turn shows the library flow of a custom agent: ingest the transcript,
// read the window, then compress a finished range and read the window again.
func ExampleClient_Turn() {
	dir, err := os.MkdirTemp("", "acdctx-example")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	ctx := context.Background()

	c, err := acdctx.Open(ctx, acdctx.Options{
		DBPath: dir + "/acd-ctx.db", Cwd: dir, ConfigPath: dir + "/none.yaml", Model: "claude-example",
	})
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	msgs := []acdctx.Message{
		{Role: acdctx.RoleUser, Kind: acdctx.KindText, Text: "Refactor the parser. " + strings.Repeat("details ", 400)},
	}
	for i := 2; i <= 24; i++ {
		role := acdctx.RoleAssistant
		if i%2 == 1 {
			role = acdctx.RoleUser
		}
		msgs = append(msgs, acdctx.Message{Role: role, Kind: acdctx.KindText, Text: strings.Repeat("step ", 500)})
	}

	turn, err := c.Turn(ctx, msgs, 9_000)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("before:", len(turn.Visible) > 0)

	_, err = c.Compress(ctx, acdctx.CompressArgs{Ranges: []acdctx.CompressRange{{
		StartRef: "m00002", EndRef: "m00013",
		Summary: "The parser steps 2 to 13 were applied and the tests passed.",
		Topic:   "parser",
	}}})
	if err != nil {
		log.Fatal(err)
	}
	turn, err = c.Turn(ctx, msgs, 6_000)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("after compress, fewer messages:", len(turn.Visible) < 25)
	// Output:
	// before: true
	// after compress, fewer messages: true
}

// ExampleNewTransport shows an SDK user swapping the HTTP client only. The upstream
// is a local fake, so the example runs offline.
func ExampleNewTransport() {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"type":"message","role":"assistant","model":"claude-example",`+
			`"content":[{"type":"text","text":"done"}],"stop_reason":"end_turn",`+
			`"usage":{"input_tokens":3,"output_tokens":1}}`)
	}))
	defer upstream.Close()

	dir, err := os.MkdirTemp("", "acdctx-example")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)

	tr, err := acdctx.NewTransport(nil, acdctx.Options{
		DBPath: dir + "/acd-ctx.db", Cwd: dir, ConfigPath: dir + "/none.yaml",
		Upstreams: []string{upstream.URL},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer tr.Close()

	client := &http.Client{Transport: tr}
	body := `{"model":"claude-example","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`
	resp, err := client.Post(upstream.URL+"/v1/messages", "application/json", strings.NewReader(body))
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	answer, _ := io.ReadAll(resp.Body)
	fmt.Println(resp.StatusCode, strings.Contains(string(answer), `"done"`))
	// Output:
	// 200 true
}

// Package acdctx is the public Go surface of Accordion Context (spec §19.2 mode 6
// and §19.9 "Harnesses e SDKs").
//
// It offers two ways to use the context-compaction model without running the
// proxy as a separate process:
//
//   - Library flow: Open a Client and drive the turn loop yourself. Turn returns
//     the visible window and an optional nudge, Compress records summaries,
//     Decompress and Recall bring original messages back. See Client.
//   - HTTP middleware: NewTransport returns an http.RoundTripper that compacts
//     Anthropic, OpenAI (Chat Completions and Responses) and Gemini requests in
//     flight, so an SDK user only swaps the HTTP client. Handler embeds the same
//     proxy handler in an existing server.
//
// Both use the same store, session and kernel code as the proxy, so the
// compaction behaves the same in every mode. Internal types are not exported;
// the public Message, Role and Kind types map to them.
package acdctx

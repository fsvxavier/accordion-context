package proxy

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/fsvxavier/accordion-context/internal/kernel"
)

// stubRetrieve matches the retrieve hint that kernel.Stub writes into a placeholder.
var stubRetrieve = regexp.MustCompile(`→ retrieve\("(m[0-9]+)"\) para o conteúdo completo`)

// msgRef matches a message reference such as m00451.
var msgRef = regexp.MustCompile(`^m[0-9]{5,}$`)

// reduceWindow applies CCR and crush (spec §14) to the tool results of a window.
// Only results above threshold tokens change. The result is a pure function of
// each message's text and reference, so the same message gets the same bytes on
// every turn, which keeps the upstream prompt cache valid. The input slice is not
// changed. With enabled false the window is returned as it is.
func reduceWindow(msgs []kernel.Message, threshold int, enabled bool) []kernel.Message {
	out := make([]kernel.Message, len(msgs))
	copy(out, msgs)
	if !enabled {
		return out
	}
	if threshold <= 0 {
		threshold = kernel.DefaultCCRThreshold
	}
	for i := range out {
		m := &out[i]
		if m.Kind != kernel.KindToolResult || m.Ref == "" || m.Tokens <= threshold {
			continue
		}
		r := kernel.Reduce(kernel.StubInput{Ref: m.Ref, Tool: m.ToolName, Text: m.Text}, threshold)
		if r.Visible == m.Text {
			continue
		}
		vis := stubRetrieve.ReplaceAllString(r.Visible, `→ decompress(blockId="$1", mode="context") para o conteúdo completo`)
		if r.Stored && !strings.HasPrefix(r.Visible, "[stored ") {
			// Lossy inline crush: the original is kept, so the model is told how to get it.
			vis += fmt.Sprintf("\n[saída reduzida · original: decompress(blockId=%q, mode=\"context\")]", m.Ref)
		}
		m.Text = vis
		m.Tokens = kernel.EstimateTokens(vis)
		m.CCRStored = r.Stored
	}
	return out
}

// sumTokens adds the token estimates of msgs.
func sumTokens(msgs []kernel.Message) int {
	n := 0
	for _, m := range msgs {
		n += m.Tokens
	}
	return n
}

// refExcerpt renders one stored message for decompress, filtered by peek and
// query like a block is. It is capped at maxToolOutput.
func refExcerpt(ref, text string, args toolArgs) string {
	var b strings.Builder
	if args.mode == "peek" && args.query != "" {
		q := strings.ToLower(args.query)
		for _, line := range strings.Split(text, "\n") {
			if strings.Contains(strings.ToLower(line), q) {
				fmt.Fprintf(&b, "[%s] %s\n", ref, line)
			}
		}
	} else {
		fmt.Fprintf(&b, "[%s] %s\n", ref, text)
	}
	out := strings.ToValidUTF8(b.String(), "")
	if len(out) > maxToolOutput {
		out = out[:maxToolOutput] + "\n[truncated]"
	}
	if out == "" {
		out = "(no matching excerpts)"
	}
	return out
}

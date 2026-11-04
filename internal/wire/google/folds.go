package google

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// CompressFold is one fold of a compress call (spec §12): the active blocks one
// tier below Tier, from StartBlock to EndBlock, become one block of Tier.
type CompressFold struct {
	Tier       int
	StartBlock string
	EndBlock   string
	Summary    string
	Topic      string
}

// parseFolds reads the optional "folds" array of a compress object. It returns
// nil when the object has no folds.
func parseFolds(m map[string]any) ([]CompressFold, error) {
	raw, ok := firstKey(m, "folds", "fold")
	if !ok {
		return nil, nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil, errors.New("folds must be an array")
	}
	out := make([]CompressFold, 0, len(arr))
	for i, f := range arr {
		fm, ok := f.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("fold %d: expected object", i)
		}
		tierV, ok := firstKey(fm, "tier", "level")
		if !ok {
			return nil, fmt.Errorf("fold %d: missing tier", i)
		}
		tier, err := tierValue(tierV)
		if err != nil {
			return nil, fmt.Errorf("fold %d: tier: %w", i, err)
		}
		start, err := stringValue(fm, "startBlock", "start_block", "start")
		if err != nil {
			return nil, fmt.Errorf("fold %d: %w", i, err)
		}
		end, err := stringValue(fm, "endBlock", "end_block", "end")
		if err != nil {
			return nil, fmt.Errorf("fold %d: %w", i, err)
		}
		summary, err := stringValue(fm, "summary", "text", "content")
		if err != nil {
			return nil, fmt.Errorf("fold %d: %w", i, err)
		}
		topic, err := stringValue(fm, "topic", "title", "label")
		if err != nil {
			return nil, fmt.Errorf("fold %d: %w", i, err)
		}
		out = append(out, CompressFold{Tier: tier, StartBlock: start, EndBlock: end, Summary: summary, Topic: topic})
	}
	return out, nil
}

// tierValue reads a tier given as a number, or as text such as "2" or "T2".
func tierValue(v any) (int, error) {
	var s string
	switch x := v.(type) {
	case json.Number:
		s = x.String()
	case string:
		s = x
	default:
		return 0, fmt.Errorf("unsupported tier type %T", v)
	}
	s = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(s), "T"), "t")
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("invalid tier %q", s)
	}
	return n, nil
}

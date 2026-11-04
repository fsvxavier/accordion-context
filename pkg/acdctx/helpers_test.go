package acdctx_test

import (
	"fmt"
	"strings"
)

// bigText returns a message body of about 2400 characters (about 600 tokens)
// that starts with a marker unique to the message number n.
func bigText(n int, role string) string {
	return fmt.Sprintf("<<%s-%02d>> ", role, n) + strings.Repeat("lorem ipsum dolor sit amet ", 90)
}

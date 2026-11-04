package anthropic

import "github.com/fsvxavier/accordion-context/internal/wire/toolargs"

// SearchArgs is the normalized input of a search_context call.
type SearchArgs = toolargs.Search

// RetrieveArgs is the normalized input of a retrieve call.
type RetrieveArgs = toolargs.Retrieve

// ParseSearchArgs parses the arguments of a search_context call. The recovery
// rules are those of toolargs.ParseSearch.
func ParseSearchArgs(args []byte) (SearchArgs, error) { return toolargs.ParseSearch(args) }

// ParseRetrieveArgs parses the arguments of a retrieve call. The recovery rules
// are those of toolargs.ParseRetrieve.
func ParseRetrieveArgs(args []byte) (RetrieveArgs, error) { return toolargs.ParseRetrieve(args) }

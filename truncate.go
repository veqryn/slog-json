package slogjson

import (
	jsonv1 "encoding/json"
	"fmt"
	"log/slog"
	"unicode/utf8"

	jsonv2 "github.com/go-json-experiment/json"
	jsonv2text "github.com/go-json-experiment/json/jsontext"
)

// ReplaceAttrTruncate is a replacement function that examines attributes before
// they are logged and if necessary truncates them.
// AWS Cloudwatch has a limit of 256kb, GCP Stackdriver is 100kb, Azure is 32kb total and 8kb per
// field, docker is 16kb, some Java based systems have a max of 8221.
// Since there can be multiple fields, and it is a lot harder to control total length, set the field
// length a bit shorter.
func ReplaceAttrTruncate(maxLogFieldLength int, jsonOptions jsonv2.Options) func(group []string, a slog.Attr) slog.Attr {
	// Add a default marshaler for all `error` types
	jsonOptions = appendErrorMarshaler(jsonOptions)

	return func(group []string, a slog.Attr) slog.Attr {
		switch a.Value.Kind() {
		case slog.KindString:
			// Truncate strings
			if s := a.Value.String(); len(s) > maxLogFieldLength {
				return slog.String(a.Key, fmt.Sprintf("replaced: true; original_length: %d; truncated: %s", len(s), truncateByBytes(s, maxLogFieldLength)))
			}

		case slog.KindAny:
			value := a.Value.Any()
			if value == nil {
				return a
			}

			// Convert []byte to a string for readability and truncate:
			if b, ok := value.([]byte); ok {
				if len(b) > maxLogFieldLength {
					return slog.String(a.Key, fmt.Sprintf("replaced: true; original_length: %d; truncated: %s", len(b), truncateByBytes(string(b), maxLogFieldLength)))
				}
				return slog.String(a.Key, string(b)) // []byte's are unreadable, so cast to string
			}

			// Now we want to do 2 things:
			// * Use any custom or third party marshallers defined in the json options.
			//   Example: convert protobuf messages to JSON using the canonical protobuf<->json spec
			//   (because otherwise things like timestamps get turned into garbage).
			//   Even if the protobuf is nested instead a struct or a slice ([]any, []*Proto, etc).
			//   This is difficult because any slice type could contain a protobuf nested in it.
			// * Truncate any large structs, slices, strings, etc.
			//
			// In order to accomplish the above in the most flexible manner, without using reflect,
			// we will pre-marshal the value into a jsonv1.RawMessage([]byte), using the same
			// marshaller options our slog handler is using, then truncate if necessary.
			sjson, err := jsonv2.Marshal(value, jsonOptions)
			if err == nil {
				a = slog.Any(a.Key, jsonv1.RawMessage(sjson))
			}

			// Truncate really long raw json and []byte's
			switch b := a.Value.Any().(type) {
			// TODO: see if there is an existing way to truncate json while keeping it valid json
			case jsonv1.RawMessage:
				if len(b) > maxLogFieldLength {
					return slog.Any(a.Key, replaced{
						Replaced: true,
						Length:   len(b),
						// Annoying to have it escaped and embedded, but can still be read if needed
						Truncated: truncateByBytes(string(b), maxLogFieldLength),
					})
				}
			case jsonv2text.Value:
				if len(b) > maxLogFieldLength {
					return slog.Any(a.Key, replaced{
						Replaced:  true,
						Length:    len(b),
						Truncated: truncateByBytes(string(b), maxLogFieldLength),
					})
				}
			}
		}
		return a
	}
}

type replaced struct {
	Replaced  bool   `json:"replaced"`
	Length    int    `json:"length"`
	Truncated string `json:"truncated"`
}

// truncateByBytes truncates based on the number of bytes, making sure to cut
// the string before the start of any multi-byte unicode characters.
func truncateByBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

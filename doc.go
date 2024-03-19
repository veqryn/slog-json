/*
Package slogjson lets you format your Golang structured logging [log/slog] usingthe JSON v2
library [github.com/go-json-experiment/json], with optional single-line pretty-printing.

This is so much easier to read than the default json:

	{"time":"2000-01-02T03:04:05Z", "level":"INFO", "msg":"m", "attr":{"nest":1234}}

or

	{"time": "2000-01-02T03:04:05Z", "level": "INFO", "msg": "m", "attr": {"nest": 1234}}

Versus the default standard library JSON Handler:

	{"time":"2000-01-02T03:04:05Z","level":"INFO","msg":"m","attr":{"nest":"1234"}}

Additional benefits:
	* JSON v2 is faster than the stdlib JSON v1 (up to 9x faster).
	* Make use of all marshaling and encoding options JSON v2 has available.
	* Improved correctness and behavior with JSON v2. See: https://github.com/golang/go/discussions/63397
*/
package slogjson

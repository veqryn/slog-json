# slog-json
[![tag](https://img.shields.io/github/tag/veqryn/slog-json.svg)](https://github.com/veqryn/slog-json/releases)
![Go Version](https://img.shields.io/badge/Go-%3E%3D%201.21-%23007d9c)
[![GoDoc](https://godoc.org/github.com/veqryn/slog-json?status.svg)](https://pkg.go.dev/github.com/veqryn/slog-json)
![Build Status](https://github.com/veqryn/slog-json/actions/workflows/build_and_test.yml/badge.svg)
[![Go report](https://goreportcard.com/badge/github.com/veqryn/slog-json)](https://goreportcard.com/report/github.com/veqryn/slog-json)
[![Coverage](https://img.shields.io/codecov/c/github/veqryn/slog-json)](https://codecov.io/gh/veqryn/slog-json)
[![Contributors](https://img.shields.io/github/contributors/veqryn/slog-json)](https://github.com/veqryn/slog-json/graphs/contributors)
[![License](https://img.shields.io/github/license/veqryn/slog-json)](./LICENSE)

Format your Golang structured logging (slog) using the [JSON v2](https://github.com/golang/go/discussions/63397)
[library](https://github.com/go-json-experiment/json), with optional single-line pretty-printing.

This is so much easier to read than the default json:
```text
{"time":"2000-01-02T03:04:05Z", "level":"INFO", "msg":"m", "attr":{"nest":1234}}
```

or
```text
{"time": "2000-01-02T03:04:05Z", "level": "INFO", "msg": "m", "attr": {"nest": 1234}}
```

Versus the default standard library JSON Handler:
```text
{"time":"2000-01-02T03:04:05Z","level":"INFO","msg":"m","attr":{"nest":"1234"}}
```

Additional benefits:
* JSON v2 is faster than the stdlib JSON v1 ([up to 9x faster](https://github.com/go-json-experiment/jsonbench)).
* Can make use of all marshaling and encoding options JSON v2 has available.
* Improved correctness and behavior with [JSON v2](https://github.com/golang/go/discussions/63397).

### Other Great SLOG Utilities
- [slogctx](https://github.com/veqryn/slog-context): Add attributes to context and have them automatically added to all log lines. Work with a logger stored in context.
- [slogotel](https://github.com/veqryn/slog-context/tree/main/otel): Automatically extract and add [OpenTelemetry](https://opentelemetry.io/) TraceID's to all log lines.
- [sloggrpc](https://github.com/veqryn/slog-context/tree/main/grpc): Instrument [GRPC](https://grpc.io/) with automatic logging of all requests and responses.
- [slogdedup](https://github.com/veqryn/slog-dedup): Middleware that deduplicates and sorts attributes. Particularly useful for JSON logging. Format logs for aggregators (Graylog, GCP/Stackdriver, etc).
- [slogbugsnag](https://github.com/veqryn/slog-bugsnag): Middleware that pipes Errors to [Bugsnag](https://www.bugsnag.com/).
- [slogjson](https://github.com/veqryn/slog-json): Formatter that uses the [JSON v2](https://github.com/golang/go/discussions/63397) [library](https://github.com/go-json-experiment/json), with optional single-line pretty-printing.


## Install
`go get github.com/veqryn/slog-json`

```go
import (
	slogjson "github.com/veqryn/slog-json"
)
```

## Usage
```go
package main

import (
	"log/slog"
	"os"

	"github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"
	slogjson "github.com/veqryn/slog-json"
)

func main() {
	h := slogjson.NewHandler(os.Stdout, &slogjson.HandlerOptions{
		AddSource:   false,
		Level:       slog.LevelInfo,
		ReplaceAttr: nil, // Same signature and behavior as stdlib JSONHandler
		JSONOptions: json.JoinOptions(
			// Options from the json v2 library (these are the defaults)
			json.Deterministic(true),
			jsontext.AllowDuplicateNames(true),
			jsontext.AllowInvalidUTF8(true),
			jsontext.EscapeForJS(true),
			jsontext.SpaceAfterColon(false),
			jsontext.SpaceAfterComma(true),
		),
	})
	slog.SetDefault(slog.New(h))

	slog.Info("hello world")
	// {"time":"2024-03-18T03:27:20Z", "level":"INFO", "msg":"hello world"}

	slog.Error("oh no!", slog.String("foo", "bar"), slog.Int("num", 98), slog.Any("custom", Nested{Nest: "my value"}))
	// {"time":"2024-03-18T03:27:20Z", "level":"ERROR", "msg":"oh no!", "foo":"bar", "num":98, "custom":{"nest":"my value"}}
}

type Nested struct {
	Nest any `json:"nest"`
}
```

## Complex Usage
```go
package main

import (
	"log/slog"
	"os"
	"time"

	"github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"
	slogmulti "github.com/samber/slog-multi"
	slogctx "github.com/veqryn/slog-context"
	slogotel "github.com/veqryn/slog-context/otel"
	slogdedup "github.com/veqryn/slog-dedup"
	slogjson "github.com/veqryn/slog-json"
	"google.golang.org/protobuf/encoding/protojson"
)

func main() {
	// slogmulti chains slog middlewares
	slog.SetDefault(slog.New(slogmulti.
		// slogctx allows putting attributes into the context and have them show up in the logs,
		// and allows putting the slog logger in the context as well.
		Pipe(slogctx.NewMiddleware(&slogctx.HandlerOptions{
			Appenders: []slogctx.AttrExtractor{
				slogctx.ExtractAppended,
				slogotel.ExtractTraceSpanID, // Automatically add the OTEL trace/span ID to all log lines
			},
		})).

		// slogdedup removes duplicates (which can cause invalid json)
		Pipe(slogdedup.NewOverwriteMiddleware(nil)).

		// slogjson uses the future json v2 golang stdlib package,
		// which is faster, more correct, and allows single-line-pretty-printing
		Handler(slogjson.NewHandler(os.Stdout, &slogjson.HandlerOptions{
			AddSource:   false,
			Level:       slog.LevelDebug,
			JSONOptions: jsonOpts,

			// ReplaceAttr intercepts some attributes before they are logged to change their format
			ReplaceAttr: replaceAttr(),
		})),
	))
}

const (
	// AWS Cloudwatch sorts by time as a string, so force it to a constant size
	RFC3339NanoConstantSize = "2006-01-02T15:04:05.000000000Z07:00"

	// AWS Cloudwatch has a limit of 256kb, GCP Stackdriver is 100kb, Azure is 32kb total and 8kb per field, docker is 16kb, many Java based systems have a max of 8221.
	// Since there can be multiple attributes and the truncation is happening per attribute, and it is a lot harder to control total length, set the field length a bit shorter.
	maxLogFieldLength = 4000
)

// json options to use by the log handler
var jsonOpts = json.JoinOptions(
	json.Deterministic(true),
	jsontext.AllowDuplicateNames(true), // No need to slow down the marshaller when our middleware is doing it for us already
	jsontext.AllowInvalidUTF8(true),
	jsontext.EscapeForJS(false),
	jsontext.SpaceAfterColon(false),
	jsontext.SpaceAfterComma(true),

	// WithMarshalers will handle values nested inside structs and slices.
	json.WithMarshalers(json.JoinMarshalers(
		// []byte's are unreadable, so cast to string.
		json.MarshalToFunc(func(encoder *jsontext.Encoder, b []byte) error {
			return encoder.WriteToken(jsontext.String(string(b)))
		}),

		// We like time.Duration to be written out a certain way
		json.MarshalToFunc(func(e *jsontext.Encoder, t time.Duration) error {
			return e.WriteToken(jsontext.String(t.String()))
		}),

		// Convert protobuf messages into JSON using the canonical protobuf<->json spec
		json.MarshalFunc((&protojson.MarshalOptions{UseProtoNames: true}).Marshal),
	)),
)

// replaceAttr returns a replacement function that will reformat the log time,
// as well as truncate very long attributes (>4000 bytes).
func replaceAttr() func([]string, slog.Attr) slog.Attr {
	truncator := slogjson.ReplaceAttrTruncate(maxLogFieldLength, jsonOpts)
	return func(groups []string, a slog.Attr) slog.Attr {
		// Output the top level time argument with a specific format,
		// Because AWS Cloudwatch sorts time as a string instead of as a time.
		if groups == nil && a.Value.Kind() == slog.KindTime {
			return slog.String(a.Key, a.Value.Time().Format(RFC3339NanoConstantSize))
		}

		// Truncate the attribute value if necessary
		return truncator(groups, a)
	}
}
```

### slog-multi Middleware
This library can interoperate with [github.com/samber/slog-multi](https://github.com/samber/slog-multi),
in order to easily setup slog workflows such as pipelines, fanout, routing, failover, etc.
```go
slog.SetDefault(slog.New(slogmulti.
	Pipe(slogctx.NewMiddleware(&slogctx.HandlerOptions{})).
	Pipe(slogdedup.NewOverwriteMiddleware(&slogdedup.OverwriteHandlerOptions{})).
	Handler(slogjson.NewHandler(os.Stdout, &slogjson.HandlerOptions{})),
))
```

### Benchmarks
Compared with the stdlib `log/slog.JSONHandler` using the `encoding/json` v1 package,
this `slogjson.Handler` using JSON v2 is about 7% faster, using fewer bytes per op.

The benchmark code is identical; written by the Golang authors for slog handlers.

Benchmarks were run on an Macbook M1 Pro.

The underlying JSON v2 encoder is up to 9x faster than the stdlib v1 encoder,
as seen in [these benchmarks](https://github.com/go-json-experiment/jsonbench).

`slogjson.Handler` Benchmarks:
```text
BenchmarkJSONHandler/defaults-10         	 1654575	       718.0 ns/op	       0 B/op	       0 allocs/op
BenchmarkJSONHandler/time_format-10      	  918249	      1258 ns/op	      56 B/op	       4 allocs/op
BenchmarkJSONHandler/time_unix-10        	 1000000	      1106 ns/op	      24 B/op	       3 allocs/op
BenchmarkPreformatting/separate-10         	 1662286	       714.1 ns/op	       0 B/op	       0 allocs/op
BenchmarkPreformatting/struct-10           	 1685990	       717.8 ns/op	       0 B/op	       0 allocs/op
BenchmarkPreformatting/struct_file-10      	  540447	      2593 ns/op	       0 B/op	       0 allocs/op
```

`slog.JSONHandler` Benchmarks:
```text
BenchmarkJSONHandler/defaults-10         	 1562847	       768.7 ns/op	       0 B/op	       0 allocs/op
BenchmarkJSONHandler/time_format-10      	  840888	      1349 ns/op	     152 B/op	       4 allocs/op
BenchmarkJSONHandler/time_unix-10        	 1000000	      1165 ns/op	     120 B/op	       3 allocs/op
BenchmarkPreformatting/separate-10         	 1550346	       778.6 ns/op	       0 B/op	       0 allocs/op
BenchmarkPreformatting/struct-10           	 1572177	       766.1 ns/op	       0 B/op	       0 allocs/op
BenchmarkPreformatting/struct_file-10      	  508678	      2631 ns/op	       0 B/op	       0 allocs/op
```

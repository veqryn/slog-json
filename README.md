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

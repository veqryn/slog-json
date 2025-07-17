package slogjson

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"
	"github.com/veqryn/slog-json/internal/buffer"
)

// This code is mostly borrowed then lightly modified from:
// https://github.com/golang/go/blob/68d3a9e417344c11426f158c7a6f3197a0890ff1/src/log/slog/handler_test.go
// https://github.com/golang/go/blob/68d3a9e417344c11426f158c7a6f3197a0890ff1/src/log/slog/json_handler_test.go

var testTime = time.Date(2000, 1, 2, 3, 4, 5, 0, time.UTC)

func TestConcurrentWrites(t *testing.T) {
	ctx := context.Background()
	count := 1000
	var buf bytes.Buffer
	h := NewHandler(&buf, nil)
	sub1 := h.WithAttrs([]slog.Attr{slog.Bool("sub1", true)})
	sub2 := h.WithAttrs([]slog.Attr{slog.Bool("sub2", true)})
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		sub1Record := slog.NewRecord(time.Time{}, slog.LevelInfo, "hello from sub1", 0)
		sub1Record.AddAttrs(slog.Int("i", i))
		sub2Record := slog.NewRecord(time.Time{}, slog.LevelInfo, "hello from sub2", 0)
		sub2Record.AddAttrs(slog.Int("i", i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := sub1.Handle(ctx, sub1Record); err != nil {
				t.Error(err)
			}
			if err := sub2.Handle(ctx, sub2Record); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	for i := 1; i <= 2; i++ {
		want := "hello from sub" + strconv.Itoa(i)
		n := strings.Count(buf.String(), want)
		if n != count {
			t.Fatalf("want %d occurrences of %q, got %d", count, want, n)
		}
	}
}

// Verify parts of Handler.
func TestHandler(t *testing.T) {
	// remove all Attrs
	removeAll := func(_ []string, a slog.Attr) slog.Attr { return slog.Attr{} }

	attrs := []slog.Attr{slog.String("a", "one"), slog.Int("b", 2), slog.Any("", nil)}
	preAttrs := []slog.Attr{slog.Int("pre", 3), slog.String("x", "y")}

	for _, test := range []struct {
		name      string
		replace   func([]string, slog.Attr) slog.Attr
		addSource bool
		with      func(slog.Handler) slog.Handler
		preAttrs  []slog.Attr
		attrs     []slog.Attr
		wantJSON  string
	}{
		{
			name:     "basic",
			attrs:    attrs,
			wantJSON: `{"time":"2000-01-02T03:04:05Z", "level":"INFO", "msg":"message", "a":"one", "b":2}`,
		},
		{
			name:     "empty key",
			attrs:    append(slices.Clip(attrs), slog.Any("", "v")),
			wantJSON: `{"time":"2000-01-02T03:04:05Z", "level":"INFO", "msg":"message", "a":"one", "b":2, "":"v"}`,
		},
		{
			name:     "cap keys",
			replace:  upperCaseKey,
			attrs:    attrs,
			wantJSON: `{"TIME":"2000-01-02T03:04:05Z", "LEVEL":"INFO", "MSG":"message", "A":"one", "B":2}`,
		},
		{
			name:     "remove all",
			replace:  removeAll,
			attrs:    attrs,
			wantJSON: `{}`,
		},
		{
			name:     "preformatted",
			with:     func(h slog.Handler) slog.Handler { return h.WithAttrs(preAttrs) },
			preAttrs: preAttrs,
			attrs:    attrs,
			wantJSON: `{"time":"2000-01-02T03:04:05Z", "level":"INFO", "msg":"message", "pre":3, "x":"y", "a":"one", "b":2}`,
		},
		{
			name:     "preformatted cap keys",
			replace:  upperCaseKey,
			with:     func(h slog.Handler) slog.Handler { return h.WithAttrs(preAttrs) },
			preAttrs: preAttrs,
			attrs:    attrs,
			wantJSON: `{"TIME":"2000-01-02T03:04:05Z", "LEVEL":"INFO", "MSG":"message", "PRE":3, "X":"y", "A":"one", "B":2}`,
		},
		{
			name:     "preformatted remove all",
			replace:  removeAll,
			with:     func(h slog.Handler) slog.Handler { return h.WithAttrs(preAttrs) },
			preAttrs: preAttrs,
			attrs:    attrs,
			wantJSON: "{}",
		},
		{
			name:     "remove built-in",
			replace:  removeKeys(slog.TimeKey, slog.LevelKey, slog.MessageKey),
			attrs:    attrs,
			wantJSON: `{"a":"one", "b":2}`,
		},
		{
			name:     "preformatted remove built-in",
			replace:  removeKeys(slog.TimeKey, slog.LevelKey, slog.MessageKey),
			with:     func(h slog.Handler) slog.Handler { return h.WithAttrs(preAttrs) },
			attrs:    attrs,
			wantJSON: `{"pre":3, "x":"y", "a":"one", "b":2}`,
		},
		{
			name:    "groups",
			replace: removeKeys(slog.TimeKey, slog.LevelKey), // to simplify the result
			attrs: []slog.Attr{
				slog.Int("a", 1),
				slog.Group("g",
					slog.Int("b", 2),
					slog.Group("h", slog.Int("c", 3)),
					slog.Int("d", 4)),
				slog.Int("e", 5),
			},
			wantJSON: `{"msg":"message", "a":1, "g":{"b":2, "h":{"c":3}, "d":4}, "e":5}`,
		},
		{
			name:     "empty group",
			replace:  removeKeys(slog.TimeKey, slog.LevelKey),
			attrs:    []slog.Attr{slog.Group("g"), slog.Group("h", slog.Int("a", 1))},
			wantJSON: `{"msg":"message", "h":{"a":1}}`,
		},
		{
			name:    "nested empty group",
			replace: removeKeys(slog.TimeKey, slog.LevelKey),
			attrs: []slog.Attr{
				slog.Group("g",
					slog.Group("h",
						slog.Group("i"), slog.Group("j"))),
			},
			wantJSON: `{"msg":"message"}`,
		},
		{
			name:    "nested non-empty group",
			replace: removeKeys(slog.TimeKey, slog.LevelKey),
			attrs: []slog.Attr{
				slog.Group("g",
					slog.Group("h",
						slog.Group("i"), slog.Group("j", slog.Int("a", 1)))),
			},
			wantJSON: `{"msg":"message", "g":{"h":{"j":{"a":1}}}}`,
		},
		{
			name:    "escapes",
			replace: removeKeys(slog.TimeKey, slog.LevelKey),
			attrs: []slog.Attr{
				slog.String("a b", "x\t\n\000y"),
				slog.Group(" b.c=\"\\x2E\t",
					slog.String("d=e", "f.g\""),
					slog.Int("m.d", 1)), // dot is not escaped
			},
			wantJSON: `{"msg":"message", "a b":"x\t\n\u0000y", " b.c=\"\\x2E\t":{"d=e":"f.g\"", "m.d":1}}`,
		},
		{
			name:    "LogValuer",
			replace: removeKeys(slog.TimeKey, slog.LevelKey),
			attrs: []slog.Attr{
				slog.Int("a", 1),
				slog.Any("name", logValueName{"Ren", "Hoek"}),
				slog.Int("b", 2),
			},
			wantJSON: `{"msg":"message", "a":1, "name":{"first":"Ren", "last":"Hoek"}, "b":2}`,
		},
		{
			// Test resolution when there is no ReplaceAttr function.
			name: "resolve",
			attrs: []slog.Attr{
				slog.Any("", &replace{slog.Value{}}), // should be elided
				slog.Any("name", logValueName{"Ren", "Hoek"}),
			},
			wantJSON: `{"time":"2000-01-02T03:04:05Z", "level":"INFO", "msg":"message", "name":{"first":"Ren", "last":"Hoek"}}`,
		},
		{
			name:     "with-group",
			replace:  removeKeys(slog.TimeKey, slog.LevelKey),
			with:     func(h slog.Handler) slog.Handler { return h.WithAttrs(preAttrs).WithGroup("s") },
			attrs:    attrs,
			wantJSON: `{"msg":"message", "pre":3, "x":"y", "s":{"a":"one", "b":2}}`,
		},
		{
			name:    "preformatted with-groups",
			replace: removeKeys(slog.TimeKey, slog.LevelKey),
			with: func(h slog.Handler) slog.Handler {
				return h.WithAttrs([]slog.Attr{slog.Int("p1", 1)}).
					WithGroup("s1").
					WithAttrs([]slog.Attr{slog.Int("p2", 2)}).
					WithGroup("s2").
					WithAttrs([]slog.Attr{slog.Int("p3", 3)})
			},
			attrs:    attrs,
			wantJSON: `{"msg":"message", "p1":1, "s1":{"p2":2, "s2":{"p3":3, "a":"one", "b":2}}}`,
		},
		{
			name:    "two with-groups",
			replace: removeKeys(slog.TimeKey, slog.LevelKey),
			with: func(h slog.Handler) slog.Handler {
				return h.WithAttrs([]slog.Attr{slog.Int("p1", 1)}).
					WithGroup("s1").
					WithGroup("s2")
			},
			attrs:    attrs,
			wantJSON: `{"msg":"message", "p1":1, "s1":{"s2":{"a":"one", "b":2}}}`,
		},
		{
			name:    "empty with-groups",
			replace: removeKeys(slog.TimeKey, slog.LevelKey),
			with: func(h slog.Handler) slog.Handler {
				return h.WithGroup("x").WithGroup("y")
			},
			wantJSON: `{"msg":"message"}`,
		},
		{
			name:    "empty with-groups, no non-empty attrs",
			replace: removeKeys(slog.TimeKey, slog.LevelKey),
			with: func(h slog.Handler) slog.Handler {
				return h.WithGroup("x").WithAttrs([]slog.Attr{slog.Group("g")}).WithGroup("y")
			},
			wantJSON: `{"msg":"message"}`,
		},
		{
			name:    "one empty with-group",
			replace: removeKeys(slog.TimeKey, slog.LevelKey),
			with: func(h slog.Handler) slog.Handler {
				return h.WithGroup("x").WithAttrs([]slog.Attr{slog.Int("a", 1)}).WithGroup("y")
			},
			attrs:    []slog.Attr{slog.Group("g", slog.Group("h"))},
			wantJSON: `{"msg":"message", "x":{"a":1}}`,
		},
		{
			name:     "GroupValue as Attr value",
			replace:  removeKeys(slog.TimeKey, slog.LevelKey),
			attrs:    []slog.Attr{{"v", slog.AnyValue(slog.IntValue(3))}},
			wantJSON: `{"msg":"message", "v":3}`,
		},
		{
			name:     "byte slice",
			replace:  removeKeys(slog.TimeKey, slog.LevelKey),
			attrs:    []slog.Attr{slog.Any("bs", []byte{1, 2, 3, 4})},
			wantJSON: `{"msg":"message", "bs":"AQIDBA=="}`,
		},
		{
			name:     "json.RawMessage",
			replace:  removeKeys(slog.TimeKey, slog.LevelKey),
			attrs:    []slog.Attr{slog.Any("bs", jsontext.Value([]byte("1234")))},
			wantJSON: `{"msg":"message", "bs":1234}`,
		},
		{
			name:    "inline group",
			replace: removeKeys(slog.TimeKey, slog.LevelKey),
			attrs: []slog.Attr{
				slog.Int("a", 1),
				slog.Group("", slog.Int("b", 2), slog.Int("c", 3)),
				slog.Int("d", 4),
			},
			wantJSON: `{"msg":"message", "a":1, "b":2, "c":3, "d":4}`,
		},
		{
			name: "Source",
			replace: func(gs []string, a slog.Attr) slog.Attr {
				if a.Key == slog.SourceKey {
					s := a.Value.Any().(*slog.Source)
					s.File = filepath.Base(s.File)
					return slog.Any(a.Key, s)
				}
				return removeKeys(slog.TimeKey, slog.LevelKey)(gs, a)
			},
			addSource: true,
			wantJSON:  `{"source":{"function":"github.com/veqryn/slog-json.TestHandler", "file":"handler_test.go", "line":$LINE}, "msg":"message"}`,
		},
		{
			name: "replace built-in with group",
			replace: func(_ []string, a slog.Attr) slog.Attr {
				if a.Key == slog.TimeKey {
					return slog.Group(slog.TimeKey, "mins", 3, "secs", 2)
				}
				if a.Key == slog.LevelKey {
					return slog.Attr{}
				}
				return a
			},
			wantJSON: `{"time":{"mins":3, "secs":2}, "msg":"message"}`,
		},
		{
			name:     "replace empty",
			replace:  func([]string, slog.Attr) slog.Attr { return slog.Attr{} },
			attrs:    []slog.Attr{slog.Group("g", slog.Int("a", 1))},
			wantJSON: `{}`,
		},
		{
			name: "replace empty 1",
			with: func(h slog.Handler) slog.Handler {
				return h.WithGroup("g").WithAttrs([]slog.Attr{slog.Int("a", 1)})
			},
			replace:  func([]string, slog.Attr) slog.Attr { return slog.Attr{} },
			attrs:    []slog.Attr{slog.Group("h", slog.Int("b", 2))},
			wantJSON: `{}`,
		},
		{
			name: "replace empty 2",
			with: func(h slog.Handler) slog.Handler {
				return h.WithGroup("g").WithAttrs([]slog.Attr{slog.Int("a", 1)}).WithGroup("h").WithAttrs([]slog.Attr{slog.Int("b", 2)})
			},
			replace:  func([]string, slog.Attr) slog.Attr { return slog.Attr{} },
			attrs:    []slog.Attr{slog.Group("i", slog.Int("c", 3))},
			wantJSON: `{}`,
		},
		{
			name:     "replace empty 3",
			with:     func(h slog.Handler) slog.Handler { return h.WithGroup("g") },
			replace:  func([]string, slog.Attr) slog.Attr { return slog.Attr{} },
			attrs:    []slog.Attr{slog.Int("a", 1)},
			wantJSON: `{}`,
		},
		{
			name: "replace empty inline",
			with: func(h slog.Handler) slog.Handler {
				return h.WithGroup("g").WithAttrs([]slog.Attr{slog.Int("a", 1)}).WithGroup("h").WithAttrs([]slog.Attr{slog.Int("b", 2)})
			},
			replace:  func([]string, slog.Attr) slog.Attr { return slog.Attr{} },
			attrs:    []slog.Attr{slog.Group("", slog.Int("c", 3))},
			wantJSON: `{}`,
		},
		{
			name: "replace partial empty attrs 1",
			with: func(h slog.Handler) slog.Handler {
				return h.WithGroup("g").WithAttrs([]slog.Attr{slog.Int("a", 1)}).WithGroup("h").WithAttrs([]slog.Attr{slog.Int("b", 2)})
			},
			replace: func(groups []string, attr slog.Attr) slog.Attr {
				return removeKeys(slog.TimeKey, slog.LevelKey, slog.MessageKey, "a")(groups, attr)
			},
			attrs:    []slog.Attr{slog.Group("i", slog.Int("c", 3))},
			wantJSON: `{"g":{"h":{"b":2, "i":{"c":3}}}}`,
		},
		{
			name: "replace partial empty attrs 2",
			with: func(h slog.Handler) slog.Handler {
				return h.WithGroup("g").WithAttrs([]slog.Attr{slog.Int("a", 1)}).WithAttrs([]slog.Attr{slog.Int("n", 4)}).WithGroup("h").WithAttrs([]slog.Attr{slog.Int("b", 2)})
			},
			replace: func(groups []string, attr slog.Attr) slog.Attr {
				return removeKeys(slog.TimeKey, slog.LevelKey, slog.MessageKey, "a", "b")(groups, attr)
			},
			attrs:    []slog.Attr{slog.Group("i", slog.Int("c", 3))},
			wantJSON: `{"g":{"n":4, "h":{"i":{"c":3}}}}`,
		},
		{
			name: "replace partial empty attrs 3",
			with: func(h slog.Handler) slog.Handler {
				return h.WithGroup("g").WithAttrs([]slog.Attr{slog.Int("x", 0)}).WithAttrs([]slog.Attr{slog.Int("a", 1)}).WithAttrs([]slog.Attr{slog.Int("n", 4)}).WithGroup("h").WithAttrs([]slog.Attr{slog.Int("b", 2)})
			},
			replace: func(groups []string, attr slog.Attr) slog.Attr {
				return removeKeys(slog.TimeKey, slog.LevelKey, slog.MessageKey, "a", "c")(groups, attr)
			},
			attrs:    []slog.Attr{slog.Group("i", slog.Int("c", 3))},
			wantJSON: `{"g":{"x":0, "n":4, "h":{"b":2}}}`,
		},
		{
			name: "replace resolved group",
			replace: func(groups []string, a slog.Attr) slog.Attr {
				if a.Value.Kind() == slog.KindGroup {
					return slog.Attr{"bad", slog.IntValue(1)}
				}
				return removeKeys(slog.TimeKey, slog.LevelKey, slog.MessageKey)(groups, a)
			},
			attrs:    []slog.Attr{slog.Any("name", logValueName{"Perry", "Platypus"})},
			wantJSON: `{"name":{"first":"Perry", "last":"Platypus"}}`,
		},
	} {
		r := slog.NewRecord(testTime, slog.LevelInfo, "message", callerPC(2))
		line := strconv.Itoa(recordSource(r).Line)
		r.AddAttrs(test.attrs...)
		var buf bytes.Buffer
		opts := HandlerOptions{ReplaceAttr: test.replace, AddSource: test.addSource}
		t.Run(test.name, func(t *testing.T) {
			var h slog.Handler = NewHandler(&buf, &opts)
			if test.with != nil {
				h = test.with(h)
			}
			buf.Reset()
			if err := h.Handle(nil, r); err != nil {
				t.Fatal(err)
			}
			want := strings.ReplaceAll(test.wantJSON, "$LINE", line)
			got := strings.TrimSuffix(buf.String(), "\n")
			if got != want {
				t.Errorf("\ngot  %s\nwant %s\n", got, want)
			}
		})
	}
}

type replace struct {
	v slog.Value
}

func (r *replace) LogValue() slog.Value { return r.v }

// callerPC returns the program counter at the given stack depth.
func callerPC(depth int) uintptr {
	var pcs [1]uintptr
	runtime.Callers(depth, pcs[:])
	return pcs[0]
}

// removeKeys returns a function suitable for HandlerOptions.ReplaceAttr
// that removes all Attrs with the given keys.
func removeKeys(keys ...string) func([]string, slog.Attr) slog.Attr {
	return func(_ []string, a slog.Attr) slog.Attr {
		for _, k := range keys {
			if a.Key == k {
				return slog.Attr{}
			}
		}
		return a
	}
}

func upperCaseKey(_ []string, a slog.Attr) slog.Attr {
	a.Key = strings.ToUpper(a.Key)
	return a
}

type logValueName struct {
	first, last string
}

func (n logValueName) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("first", n.first),
		slog.String("last", n.last))
}

func TestHandlerEnabled(t *testing.T) {
	levelVar := func(l slog.Level) *slog.LevelVar {
		var al slog.LevelVar
		al.Set(l)
		return &al
	}

	for _, test := range []struct {
		leveler slog.Leveler
		want    bool
	}{
		{nil, true},
		{slog.LevelWarn, false},
		{&slog.LevelVar{}, true}, // defaults to Info
		{levelVar(slog.LevelWarn), false},
		{slog.LevelDebug, true},
		{levelVar(slog.LevelDebug), true},
	} {
		h := &Handler{opts: HandlerOptions{Level: test.leveler}}
		got := h.Enabled(nil, slog.LevelInfo)
		if got != test.want {
			t.Errorf("%v: got %t, want %t", test.leveler, got, test.want)
		}
	}
}

func TestSecondWith(t *testing.T) {
	// Verify that a second call to Logger.With does not corrupt
	// the original.
	var buf bytes.Buffer
	h := NewHandler(&buf, &HandlerOptions{ReplaceAttr: removeKeys(slog.TimeKey)})
	logger := slog.New(h).With(
		slog.String("app", "playground"),
		slog.String("role", "tester"),
		slog.Int("data_version", 2),
	)
	appLogger := logger.With("type", "log") // this becomes type=met
	_ = logger.With("type", "metric")
	appLogger.Info("foo")
	got := strings.TrimSpace(buf.String())
	want := `{"level":"INFO", "msg":"foo", "app":"playground", "role":"tester", "data_version":2, "type":"log"}`
	if got != want {
		t.Errorf("\ngot  %s\nwant %s", got, want)
	}
}

func TestReplaceAttrGroups(t *testing.T) {
	// Verify that ReplaceAttr is called with the correct groups.
	type ga struct {
		groups string
		key    string
		val    string
	}

	var got []ga

	h := NewHandler(io.Discard, &HandlerOptions{ReplaceAttr: func(gs []string, a slog.Attr) slog.Attr {
		v := a.Value.String()
		if a.Key == slog.TimeKey {
			v = "<now>"
		}
		got = append(got, ga{strings.Join(gs, ","), a.Key, v})
		return a
	}})
	slog.New(h).
		With(slog.Int("a", 1)).
		WithGroup("g1").
		With(slog.Int("b", 2)).
		WithGroup("g2").
		With(
			slog.Int("c", 3),
			slog.Group("g3", slog.Int("d", 4)),
			slog.Int("e", 5)).
		Info("m",
			slog.Int("f", 6),
			slog.Group("g4", slog.Int("h", 7)),
			slog.Int("i", 8))

	want := []ga{
		{"", "a", "1"},
		{"g1", "b", "2"},
		{"g1,g2", "c", "3"},
		{"g1,g2,g3", "d", "4"},
		{"g1,g2", "e", "5"},
		{"", "time", "<now>"},
		{"", "level", "INFO"},
		{"", "msg", "m"},
		{"g1,g2", "f", "6"},
		{"g1,g2,g4", "h", "7"},
		{"g1,g2", "i", "8"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("\ngot  %v\nwant %v", got, want)
	}
}

const rfc3339Millis = "2006-01-02T15:04:05.000Z07:00"

func TestJSONHandler(t *testing.T) {
	for _, test := range []struct {
		name string
		opts HandlerOptions
		want string
	}{
		{
			"none",
			HandlerOptions{},
			`{"time":"2000-01-02T03:04:05Z", "level":"INFO", "msg":"m", "a":1, "m":{"b":2}}`,
		},
		{
			"replace",
			HandlerOptions{ReplaceAttr: upperCaseKey},
			`{"TIME":"2000-01-02T03:04:05Z", "LEVEL":"INFO", "MSG":"m", "A":1, "M":{"b":2}}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var buf bytes.Buffer
			h := NewHandler(&buf, &test.opts)
			r := slog.NewRecord(testTime, slog.LevelInfo, "m", 0)
			r.AddAttrs(slog.Int("a", 1), slog.Any("m", map[string]int{"b": 2}))
			if err := h.Handle(context.Background(), r); err != nil {
				t.Fatal(err)
			}
			got := strings.TrimSuffix(buf.String(), "\n")
			if got != test.want {
				t.Errorf("\ngot  %s\nwant %s", got, test.want)
			}
		})
	}
}

// for testing json.Marshaler
type jsonMarshaler struct {
	s string
}

func (j jsonMarshaler) String() string { return j.s } // should be ignored

func (j jsonMarshaler) MarshalJSON() ([]byte, error) {
	if j.s == "" {
		return nil, errors.New("json: empty string")
	}
	return []byte(fmt.Sprintf(`[%q]`, j.s)), nil
}

type jsonMarshalerError struct {
	jsonMarshaler
}

func (jsonMarshalerError) Error() string { return "oops" }

func TestAppendJSONValue(t *testing.T) {
	// jsonAppendAttrValue should always agree with json.Marshal.
	for i, value := range []any{
		"hello\r\n\t\a",
		`"[{escape}]"`,
		"<escapeHTML&>",
		// \u2028\u2029 is an edge case in JavaScript vs JSON.
		"\u03B8\u2028\u2029\uFFFF",
		// \xF6 is an incomplete encoding.
		// "\xF6",
		`-123`,
		int64(-9_200_123_456_789_123_456),
		uint64(9_200_123_456_789_123_456),
		-12.75,
		1.23e-9,
		false,
		time.Minute,
		testTime,
		jsonMarshaler{"xyz"},
		jsonMarshalerError{jsonMarshaler{"pqr"}},
		slog.LevelWarn,
	} {
		got := jsonValueString(slog.AnyValue(value))
		want, err := marshalJSON(value)
		if err != nil {
			t.Fatal(i, err)
		}
		if got != want {
			t.Errorf("%d: %v: got %s, want %s", i, value, got, want)
		}
	}
}

func marshalJSON(x any) (string, error) {
	var buf bytes.Buffer
	err := json.MarshalWrite(&buf, x, jsontext.AllowInvalidUTF8(true), jsontext.EscapeForJS(true))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()), nil
}

func TestJSONAppendAttrValueSpecial(t *testing.T) {
	// Attr values that render differently from json.Marshal.
	for _, test := range []struct {
		value any
		want  string
	}{
		{math.NaN(), `"!ERROR:json: cannot marshal from Go float64: unsupported value: NaN"`},
		{math.Inf(+1), `"!ERROR:json: cannot marshal from Go float64: unsupported value: +Inf"`},
		{math.Inf(-1), `"!ERROR:json: cannot marshal from Go float64: unsupported value: -Inf"`},
		{io.EOF, `"EOF"`},
	} {
		got := jsonValueString(slog.AnyValue(test.value))
		got = strings.ReplaceAll(got, "unable to", "cannot")
		if got != test.want {
			t.Errorf("%v: got %s, want %s", test.value, got, test.want)
		}
	}
}

func jsonValueString(v slog.Value) string {
	var buf []byte
	s := &handleState{h: &Handler{opts: HandlerOptions{JSONOptions: json.WithMarshalers(errorMarshaler(nil))}}, buf: (*buffer.Buffer)(&buf)}
	if err := appendJSONValue(s, v); err != nil {
		s.appendError(err)
	}
	return string(buf)
}

func TestMoreFormatting(t *testing.T) {
	type Nested struct {
		Nest any `json:"nest"`
	}

	overwrittenOpts := &HandlerOptions{
		JSONOptions: json.JoinOptions(
			json.StringifyNumbers(true),         // changed
			jsontext.AllowDuplicateNames(false), // changed, should be overwritten later with true
			jsontext.EscapeForHTML(true),        // changed
			jsontext.Multiline(true),            // changed, should be overwritten later with false
		),
	}

	moreWhitespaceOpts := &HandlerOptions{
		JSONOptions: json.JoinOptions(
			defaultJSONOptions,
			jsontext.SpaceAfterColon(true), // changed
		),
	}

	var buf bytes.Buffer
	for _, test := range []struct {
		opts  *HandlerOptions
		value any
		want  string
	}{
		{overwrittenOpts, int(1234), `{"time":"2000-01-02T03:04:05Z","level":"INFO","msg":"m","attr":"1234"}`},
		{overwrittenOpts, Nested{Nest: int(1234)}, `{"time":"2000-01-02T03:04:05Z","level":"INFO","msg":"m","attr":{"nest":"1234"}}`},
		{overwrittenOpts, uint64(6789), `{"time":"2000-01-02T03:04:05Z","level":"INFO","msg":"m","attr":"6789"}`},
		{overwrittenOpts, Nested{Nest: uint64(6789)}, `{"time":"2000-01-02T03:04:05Z","level":"INFO","msg":"m","attr":{"nest":"6789"}}`},
		// The difference in the next two is a little interesting
		{overwrittenOpts, float32(1234.6789), `{"time":"2000-01-02T03:04:05Z","level":"INFO","msg":"m","attr":"1234.678955078125"}`},
		{overwrittenOpts, Nested{Nest: float32(1234.6789)}, `{"time":"2000-01-02T03:04:05Z","level":"INFO","msg":"m","attr":{"nest":"1234.679"}}`},
		// These next two are a TODO to fix up for root level attributes
		{overwrittenOpts, "<a>&amp;</a>", `{"time":"2000-01-02T03:04:05Z","level":"INFO","msg":"m","attr":"<a>&amp;</a>"}`},
		{overwrittenOpts, Nested{Nest: "<a>&amp;</a>"}, `{"time":"2000-01-02T03:04:05Z","level":"INFO","msg":"m","attr":{"nest":"\u003ca\u003e\u0026amp;\u003c/a\u003e"}}`},
		{moreWhitespaceOpts, Nested{Nest: int(1234)}, `{"time": "2000-01-02T03:04:05Z", "level": "INFO", "msg": "m", "attr": {"nest": 1234}}`},
	} {
		h := NewHandler(&buf, test.opts)
		buf.Reset()
		r := slog.NewRecord(testTime, slog.LevelInfo, "m", callerPC(2))
		r.AddAttrs(slog.Any("attr", test.value))
		if err := h.Handle(context.Background(), r); err != nil {
			t.Errorf("%v: got error: %v", test.value, err)
			continue
		}
		if got := buf.String()[:len(buf.String())-1]; got != test.want {
			t.Errorf("%v: got %s, want %s", test.value, got, test.want)
		}
	}
}

type DescriptiveError struct {
	description string
	name        string
}

func NewDescriptiveError(name string, description string) *DescriptiveError {
	return &DescriptiveError{description: description, name: name}
}

func (de *DescriptiveError) Error() string {
	return de.name
}

func (de *DescriptiveError) Description() string {
	return de.description
}

var jsonOpts = json.JoinOptions(
	json.Deterministic(true),
	jsontext.AllowDuplicateNames(true),
	jsontext.AllowInvalidUTF8(true),
	jsontext.SpaceAfterComma(true),
	json.WithMarshalers(json.JoinMarshalers(
		json.MarshalToFunc(func(encoder *jsontext.Encoder, de *DescriptiveStruct) error {
			return encoder.WriteToken(jsontext.String(de.Description()))
		}),
		json.MarshalToFunc(func(encoder *jsontext.Encoder, de *DescriptiveError) error {
			return encoder.WriteToken(jsontext.String(de.Description()))
		}),
	)),
)

func TestWithMarshalers(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	logger := slog.New(NewHandler(buf, &HandlerOptions{
		Level:       slog.LevelDebug,
		JSONOptions: jsonOpts,
	}))

	basicErr := errors.New("basic error")
	dErr := NewDescriptiveError("myError", "My Description")

	logger.Error("Oh no", "basic", basicErr, "descriptive", dErr)
	// t.Log(buf.String())

	expectedSuffix := `"level":"ERROR", "msg":"Oh no", "basic":"basic error", "descriptive":"My Description"}`
	if !strings.HasSuffix(strings.TrimSpace(buf.String()), expectedSuffix) {
		t.Fatalf("Got: %s\nWant ending with:%s", buf.String(), expectedSuffix)
	}
}

func BenchmarkJSONHandler(b *testing.B) {
	for _, bench := range []struct {
		name string
		opts HandlerOptions
	}{
		{"defaults", HandlerOptions{}},
		{"time format", HandlerOptions{
			ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
				v := a.Value
				if v.Kind() == slog.KindTime {
					return slog.String(a.Key, v.Time().Format(rfc3339Millis))
				}
				if a.Key == "level" {
					return slog.Attr{"severity", a.Value}
				}
				return a
			},
		}},
		{"time unix", HandlerOptions{
			ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
				v := a.Value
				if v.Kind() == slog.KindTime {
					return slog.Int64(a.Key, v.Time().UnixNano())
				}
				if a.Key == "level" {
					return slog.Attr{"severity", a.Value}
				}
				return a
			},
		}},
	} {
		b.Run(bench.name, func(b *testing.B) {
			ctx := context.Background()
			l := slog.New(NewHandler(io.Discard, &bench.opts)).With(
				slog.String("program", "my-test-program"),
				slog.String("package", "log/slog"),
				slog.String("traceID", "2039232309232309"),
				slog.String("URL", "https://pkg.go.dev/golang.org/x/log/slog"))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				l.LogAttrs(ctx, slog.LevelInfo, "this is a typical log message",
					slog.String("module", "github.com/google/go-cmp"),
					slog.String("version", "v1.23.4"),
					slog.Int("count", 23),
					slog.Int("number", 123456),
				)
			}
		})
	}
}

func BenchmarkPreformatting(b *testing.B) {
	type req struct {
		Method  string
		URL     string
		TraceID string
		Addr    string
	}

	structAttrs := []any{
		slog.String("program", "my-test-program"),
		slog.String("package", "log/slog"),
		slog.Any("request", &req{
			Method:  "GET",
			URL:     "https://pkg.go.dev/golang.org/x/log/slog",
			TraceID: "2039232309232309",
			Addr:    "127.0.0.1:8080",
		}),
	}

	outFile, err := os.Create(filepath.Join(b.TempDir(), "bench.log"))
	if err != nil {
		b.Fatal(err)
	}
	defer func() {
		if err := outFile.Close(); err != nil {
			b.Fatal(err)
		}
	}()

	for _, bench := range []struct {
		name  string
		wc    io.Writer
		attrs []any
	}{
		{"separate", io.Discard, []any{
			slog.String("program", "my-test-program"),
			slog.String("package", "log/slog"),
			slog.String("method", "GET"),
			slog.String("URL", "https://pkg.go.dev/golang.org/x/log/slog"),
			slog.String("traceID", "2039232309232309"),
			slog.String("addr", "127.0.0.1:8080"),
		}},
		{"struct", io.Discard, structAttrs},
		{"struct file", outFile, structAttrs},
	} {
		ctx := context.Background()
		b.Run(bench.name, func(b *testing.B) {
			l := slog.New(NewHandler(bench.wc, nil)).With(bench.attrs...)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				l.LogAttrs(ctx, slog.LevelInfo, "this is a typical log message",
					slog.String("module", "github.com/google/go-cmp"),
					slog.String("version", "v1.23.4"),
					slog.Int("count", 23),
					slog.Int("number", 123456),
				)
			}
		})
	}
}

func BenchmarkJSONEncoding(b *testing.B) {
	value := 3.14
	buf := buffer.New()
	defer buf.Free()
	b.Run("json.Marshal", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			by, err := json.Marshal(value)
			if err != nil {
				b.Fatal(err)
			}
			buf.Write(by)
			*buf = (*buf)[:0]
		}
	})
	b.Run("Encoder.Encode", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if err := json.MarshalWrite(buf, value); err != nil {
				b.Fatal(err)
			}
			*buf = (*buf)[:0]
		}
	})
	_ = buf
}

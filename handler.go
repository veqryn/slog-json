package slogjson

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"
	"github.com/veqryn/slog-json/internal/buffer"
)

// This code is mostly borrowed then lightly modified from:
// https://github.com/golang/go/blob/68d3a9e417344c11426f158c7a6f3197a0890ff1/src/log/slog/handler.go
// https://github.com/golang/go/blob/68d3a9e417344c11426f158c7a6f3197a0890ff1/src/log/slog/json_handler.go
// https://github.com/golang/go/tree/68d3a9e417344c11426f158c7a6f3197a0890ff1/src/log/slog/internal/buffer

// HandlerOptions are options for a [Handler].
// A zero HandlerOptions consists entirely of default values.
type HandlerOptions struct {
	// AddSource causes the handler to compute the source code position
	// of the log statement and add a SourceKey attribute to the output.
	AddSource bool

	// Level reports the minimum record level that will be logged.
	// The handler discards records with lower levels.
	// If Level is nil, the handler assumes LevelInfo.
	// The handler calls Level.Level for each record processed;
	// to adjust the minimum level dynamically, use a LevelVar.
	Level slog.Leveler

	// ReplaceAttr is called to rewrite each non-group attribute before it is logged.
	// The attribute's value has been resolved (see [Value.Resolve]).
	// If ReplaceAttr returns a zero Attr, the attribute is discarded.
	//
	// The built-in attributes with keys "time", "level", "source", and "msg"
	// are passed to this function, except that time is omitted
	// if zero, and source is omitted if AddSource is false.
	//
	// The first argument is a list of currently open groups that contain the
	// Attr. It must not be retained or modified. ReplaceAttr is never called
	// for Group attributes, only their contents. For example, the attribute
	// list
	//
	//     Int("a", 1), Group("g", Int("b", 2)), Int("c", 3)
	//
	// results in consecutive calls to ReplaceAttr with the following arguments:
	//
	//     nil, Int("a", 1)
	//     []string{"g"}, Int("b", 2)
	//     nil, Int("c", 3)
	//
	// ReplaceAttr can be used to change the default keys of the built-in
	// attributes, convert types (for example, to replace a `time.Time` with the
	// integer seconds since the Unix epoch), sanitize personal information, or
	// remove attributes from the output.
	ReplaceAttr func(groups []string, a slog.Attr) slog.Attr

	// JSONOptions is a set of options created with [json.JoinOptions] for
	// configuring the json v2 library.
	// If not configured, the defaults will be:
	// 	json.Deterministic(true),
	// 	json.DiscardUnknownMembers(false),
	// 	json.FormatNilMapAsNull(false),
	// 	json.FormatNilSliceAsNull(false),
	// 	json.MatchCaseInsensitiveNames(false),
	// 	json.StringifyNumbers(false),
	// 	json.RejectUnknownMembers(false),
	// 	jsontext.AllowDuplicateNames(true),
	// 	jsontext.AllowInvalidUTF8(true),
	// 	jsontext.EscapeForHTML(false),
	// 	jsontext.EscapeForJS(false),
	// 	jsontext.Multiline(false),
	// 	jsontext.SpaceAfterColon(false),
	// 	jsontext.SpaceAfterComma(true),
	JSONOptions jsontext.Options
}

// Handler is a [log/slog.Handler] that writes Records to an [io.Writer] as
// line-delimited JSON objects.
type Handler struct {
	stringifyNumbers  bool
	attrSep           string
	keySep            string
	opts              HandlerOptions
	preformattedAttrs []byte
	groups            []string // all groups started from WithGroup
	nOpenGroups       int      // the number of groups opened in preformattedAttrs
	mu                *sync.Mutex
	w                 io.Writer
}

// NewHandler creates a [Handler] that writes to w, using the given options.
// If opts is nil, the default options are used.
func NewHandler(w io.Writer, opts *HandlerOptions) *Handler {
	if opts == nil {
		// Defaults
		opts = &HandlerOptions{JSONOptions: json.JoinOptions(
			json.Deterministic(true),
			json.DiscardUnknownMembers(false),
			json.FormatNilMapAsNull(false),
			json.FormatNilSliceAsNull(false),
			json.MatchCaseInsensitiveNames(false),
			json.StringifyNumbers(false),
			json.RejectUnknownMembers(false),
			jsontext.AllowDuplicateNames(true),
			jsontext.AllowInvalidUTF8(true),
			jsontext.EscapeForHTML(false),
			jsontext.EscapeForJS(false),
			jsontext.Multiline(false),
			jsontext.SpaceAfterColon(false),
			jsontext.SpaceAfterComma(true),
		)}
	}

	// Warn about how to properly avoid duplicates (without throwing errors)
	if b, ok := json.GetOption(opts.JSONOptions, jsontext.AllowDuplicateNames); ok && !b {
		slog.Warn("slog-json: jsontext.AllowDuplicateNames(false) is not supported; instead use: github.com/veqryn/slog-dedup")
	}
	if b, ok := json.GetOption(opts.JSONOptions, jsontext.Multiline); ok && b {
		slog.Warn("slog-json: jsontext.Multiline(true) is not supported yet")
	}

	// Overwrite several options with what we currently support
	opts.JSONOptions = json.JoinOptions(
		opts.JSONOptions,
		jsontext.AllowDuplicateNames(true),
		jsontext.Multiline(false),
	)

	// TODO: handle the following options:
	// jsontext.AllowInvalidUTF8(false) // root keys and string values
	// jsontext.EscapeForHTML(true) // root keys and string values
	// jsontext.EscapeForJS(true) // root keys and string values
	// jsontext.Multiline(true) // root level structure
	// jsontext.WithIndent("...") // root level structure
	// jsontext.WithIndentPrefix("...") // root level structure

	stringifyNumbers := false
	if b, _ := json.GetOption(opts.JSONOptions, json.StringifyNumbers); b {
		stringifyNumbers = true
	}

	attrSep := ","
	if b, _ := json.GetOption(opts.JSONOptions, jsontext.SpaceAfterComma); b {
		attrSep = ", "
	}

	keySep := ":"
	if b, _ := json.GetOption(opts.JSONOptions, jsontext.SpaceAfterColon); b {
		keySep = ": "
	}

	return &Handler{
		stringifyNumbers: stringifyNumbers,
		attrSep:          attrSep,
		keySep:           keySep,
		w:                w,
		opts:             *opts,
		mu:               &sync.Mutex{},
	}
}

func (h *Handler) clone() *Handler {
	// We can't use assignment because we can't copy the mutex.
	return &Handler{
		stringifyNumbers:  h.stringifyNumbers,
		attrSep:           h.attrSep,
		keySep:            h.keySep,
		opts:              h.opts,
		preformattedAttrs: slices.Clip(h.preformattedAttrs),
		groups:            slices.Clip(h.groups),
		nOpenGroups:       h.nOpenGroups,
		w:                 h.w,
		mu:                h.mu, // mutex shared among all clones of this handler
	}
}

// Enabled reports whether the handler handles records at the given level.
// The handler ignores records whose level is lower.
func (h *Handler) Enabled(_ context.Context, l slog.Level) bool {
	minLevel := slog.LevelInfo
	if h.opts.Level != nil {
		minLevel = h.opts.Level.Level()
	}
	return l >= minLevel
}

// WithAttrs returns a new [Handler] whose attributes consists
// of h's attributes followed by attrs.
func (h *Handler) WithAttrs(as []slog.Attr) slog.Handler {
	// We are going to ignore empty groups, so if the entire slice consists of
	// them, there is nothing to do.
	if countEmptyGroups(as) == len(as) {
		return h
	}
	h2 := h.clone()
	// Pre-format the attributes as an optimization.
	state := h2.newHandleState((*buffer.Buffer)(&h2.preformattedAttrs), false, "")
	defer state.free()
	if pfa := h2.preformattedAttrs; len(pfa) > 0 {
		state.sep = h.attrSep
		if pfa[len(pfa)-1] == '{' {
			state.sep = ""
		}
	}
	// Remember the position in the buffer, in case all attrs are empty.
	pos := state.buf.Len()
	state.openGroups()
	if !state.appendAttrs(as) {
		state.buf.SetLen(pos)
	} else {
		// Remember how many opened groups are in preformattedAttrs,
		// so we don't open them again when we handle a Record.
		h2.nOpenGroups = len(h2.groups)
	}
	return h2
}

// WithGroup returns a new [Handler] who will put any future attributes inside
// the group.
func (h *Handler) WithGroup(name string) slog.Handler {
	h2 := h.clone()
	h2.groups = append(h2.groups, name)
	return h2
}

// Handle formats its argument [Record] as a JSON object.
//
// If the Record's time is zero, the time is omitted.
// Otherwise, the key is "time"
// and the value is output as with json.Marshal.
//
// The level's key is "level" and the value of [Level.String] is output.
//
// If the AddSource option is set and source information is available,
// the key is "source", and the value is a record of type [Source].
//
// The message's key is "msg".
//
// To modify these or other attributes, or remove them from the output, use
// [HandlerOptions.ReplaceAttr].
//
// Values are formatted using the provided [json.Options], with two exceptions.
//
// First, an Attr whose Value is of type error is formatted as a string, by
// calling its Error method. Only errors in Attrs receive this special treatment,
// not errors embedded in structs, slices, maps or other data structures that
// are processed by the [json] package.
//
// Second, an encoding failure does not cause Handle to return an error.
// Instead, the error message is formatted as a string.
//
// Each call to Handle results in a single serialized call to io.Writer.Write.
func (h *Handler) Handle(_ context.Context, r slog.Record) error {
	state := h.newHandleState(buffer.New(), true, "")
	defer state.free()
	state.buf.WriteByte('{')

	// Built-in attributes. They are not in a group.
	stateGroups := state.groups
	state.groups = nil // So ReplaceAttrs sees no groups instead of the pre groups.
	rep := h.opts.ReplaceAttr
	// time
	if !r.Time.IsZero() {
		key := slog.TimeKey
		val := r.Time.Round(0) // strip monotonic to match Attr behavior
		if rep == nil {
			state.appendKey(key)
			appendJSONTime(&state, val)
		} else {
			state.appendAttr(slog.Time(key, val))
		}
	}
	// level
	key := slog.LevelKey
	val := r.Level
	if rep == nil {
		state.appendKey(key)
		state.appendString(val.String())
	} else {
		state.appendAttr(slog.Any(key, val))
	}
	// source
	if h.opts.AddSource {
		state.appendAttr(slog.Any(slog.SourceKey, recordSource(r)))
	}
	key = slog.MessageKey
	msg := r.Message
	if rep == nil {
		state.appendKey(key)
		state.appendString(msg)
	} else {
		state.appendAttr(slog.String(key, msg))
	}
	state.groups = stateGroups // Restore groups passed to ReplaceAttrs.
	state.appendNonBuiltIns(r)
	state.buf.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.w.Write(*state.buf)
	return err
}

// source returns a Source for the log event.
// If the Record was created without the necessary information,
// or if the location is unavailable, it returns a non-nil *Source
// with zero fields.
func recordSource(r slog.Record) *slog.Source {
	fs := runtime.CallersFrames([]uintptr{r.PC})
	f, _ := fs.Next()
	return &slog.Source{
		Function: f.Function,
		File:     f.File,
		Line:     f.Line,
	}
}

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
	// 	jsontext.EscapeForJS(true),
	// 	jsontext.Multiline(false),
	// 	jsontext.SpaceAfterColon(false),
	// 	jsontext.SpaceAfterComma(true),
	JSONOptions jsontext.Options
}

var defaultJSONOptions = json.JoinOptions(
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
	jsontext.EscapeForJS(true),
	jsontext.Multiline(false),
	jsontext.SpaceAfterColon(false),
	jsontext.SpaceAfterComma(true),
)

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
		opts = &HandlerOptions{}
	}
	// Defaults
	if opts.JSONOptions == nil {
		opts.JSONOptions = defaultJSONOptions
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
	_ = state.buf.WriteByte('{')

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
	_ = state.buf.WriteByte('\n')

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

func (s *handleState) appendNonBuiltIns(r slog.Record) {
	// preformatted Attrs
	if pfa := s.h.preformattedAttrs; len(pfa) > 0 {
		_, _ = s.buf.WriteString(s.sep)
		_, _ = s.buf.Write(pfa)
		s.sep = s.h.attrSep
		if pfa[len(pfa)-1] == '{' {
			s.sep = ""
		}
	}
	// Attrs in Record -- unlike the built-in ones, they are in groups started
	// from WithGroup.
	// If the record has no Attrs, don't output any groups.
	nOpenGroups := s.h.nOpenGroups
	if r.NumAttrs() > 0 {
		// The group may turn out to be empty even though it has attrs (for
		// example, ReplaceAttr may delete all the attrs).
		// So remember where we are in the buffer, to restore the position
		// later if necessary.
		pos := s.buf.Len()
		s.openGroups()
		nOpenGroups = len(s.h.groups)
		empty := true
		r.Attrs(func(a slog.Attr) bool {
			if s.appendAttr(a) {
				empty = false
			}
			return true
		})
		if empty {
			s.buf.SetLen(pos)
			nOpenGroups = s.h.nOpenGroups
		}
	}

	// Close all open groups.
	for range s.h.groups[:nOpenGroups] {
		_ = s.buf.WriteByte('}')
	}
	// Close the top-level object.
	_ = s.buf.WriteByte('}')
}

// handleState holds state for a single call to Handler.handle.
// The initial value of sep determines whether to emit a separator
// before the next key, after which it stays true.
type handleState struct {
	h       *Handler
	buf     *buffer.Buffer
	freeBuf bool      // should buf be freed?
	sep     string    // separator to write before next key
	groups  *[]string // pool-allocated slice of active groups, for ReplaceAttr
}

var groupPool = sync.Pool{New: func() any {
	s := make([]string, 0, 10)
	return &s
}}

func (h *Handler) newHandleState(buf *buffer.Buffer, freeBuf bool, sep string) handleState {
	s := handleState{
		h:       h,
		buf:     buf,
		freeBuf: freeBuf,
		sep:     sep,
	}
	if h.opts.ReplaceAttr != nil {
		s.groups = groupPool.Get().(*[]string)
		*s.groups = append(*s.groups, h.groups[:h.nOpenGroups]...)
	}
	return s
}

func (s *handleState) free() {
	if s.freeBuf {
		s.buf.Free()
	}
	if gs := s.groups; gs != nil {
		*gs = (*gs)[:0]
		groupPool.Put(gs)
	}
}

func (s *handleState) openGroups() {
	for _, n := range s.h.groups[s.h.nOpenGroups:] {
		s.openGroup(n)
	}
}

// openGroup starts a new group of attributes
// with the given name.
func (s *handleState) openGroup(name string) {
	s.appendKey(name)
	_ = s.buf.WriteByte('{')
	s.sep = ""

	// Collect group names for ReplaceAttr.
	if s.groups != nil {
		*s.groups = append(*s.groups, name)
	}
}

// closeGroup ends the group with the given name.
func (s *handleState) closeGroup(name string) {
	_ = s.buf.WriteByte('}')

	s.sep = s.h.attrSep
	if s.groups != nil {
		*s.groups = (*s.groups)[:len(*s.groups)-1]
	}
}

// appendAttrs appends the slice of Attrs.
// It reports whether something was appended.
func (s *handleState) appendAttrs(as []slog.Attr) bool {
	nonEmpty := false
	for _, a := range as {
		if s.appendAttr(a) {
			nonEmpty = true
		}
	}
	return nonEmpty
}

// isEmpty reports whether a has an empty key and a nil value.
// That can be written as Attr{} or Any("", nil).
func attrIsEmpty(a slog.Attr) bool {
	return a.Key == "" && a.Value.Equal(slog.Value{})
}

// group returns the non-zero fields of s as a slice of attrs.
// It is similar to a LogValue method, but we don't want Source
// to implement LogValuer because it would be resolved before
// the ReplaceAttr function was called.
func sourceGroup(s *slog.Source) slog.Value {
	var as []slog.Attr
	if s.Function != "" {
		as = append(as, slog.String("function", s.Function))
	}
	if s.File != "" {
		as = append(as, slog.String("file", s.File))
	}
	if s.Line != 0 {
		as = append(as, slog.Int("line", s.Line))
	}
	return slog.GroupValue(as...)
}

// appendAttr appends the Attr's key and value.
// It handles replacement and checking for an empty key.
// It reports whether something was appended.
func (s *handleState) appendAttr(a slog.Attr) bool {
	a.Value = a.Value.Resolve()
	if rep := s.h.opts.ReplaceAttr; rep != nil && a.Value.Kind() != slog.KindGroup {
		var gs []string
		if s.groups != nil {
			gs = *s.groups
		}
		// a.Value is resolved before calling ReplaceAttr, so the user doesn't have to.
		a = rep(gs, a)
		// The ReplaceAttr function may return an unresolved Attr.
		a.Value = a.Value.Resolve()
	}
	// Elide empty Attrs.
	if attrIsEmpty(a) {
		return false
	}
	// Special case: Source.
	if v := a.Value; v.Kind() == slog.KindAny {
		if src, ok := v.Any().(*slog.Source); ok {
			a.Value = sourceGroup(src)
		}
	}
	if a.Value.Kind() == slog.KindGroup {
		attrs := a.Value.Group()
		// Output only non-empty groups.
		if len(attrs) > 0 {
			// The group may turn out to be empty even though it has attrs (for
			// example, ReplaceAttr may delete all the attrs).
			// So remember where we are in the buffer, to restore the position
			// later if necessary.
			pos := s.buf.Len()
			// Inline a group with an empty key.
			if a.Key != "" {
				s.openGroup(a.Key)
			}
			if !s.appendAttrs(attrs) {
				s.buf.SetLen(pos)
				return false
			}
			if a.Key != "" {
				s.closeGroup(a.Key)
			}
		}
	} else {
		s.appendKey(a.Key)
		s.appendValue(a.Value)
	}
	return true
}

func (s *handleState) appendError(err error) {
	s.appendString(fmt.Sprintf("!ERROR:%v", err))
}

func (s *handleState) appendKey(key string) {
	_, _ = s.buf.WriteString(s.sep)
	s.appendString(key)
	_, _ = s.buf.WriteString(s.h.keySep)
	s.sep = s.h.attrSep
}

func (s *handleState) appendString(str string) {
	_ = s.buf.WriteByte('"')
	*s.buf = appendEscapedJSONString(*s.buf, str)
	_ = s.buf.WriteByte('"')
}

func (s *handleState) appendValue(v slog.Value) {
	defer func() {
		if r := recover(); r != nil {
			// If it panics with a nil pointer, the most likely cases are
			// an encoding.TextMarshaler or error fails to guard against nil,
			// in which case "<nil>" seems to be the feasible choice.
			//
			// Adapted from the code in fmt/print.go.
			if v := reflect.ValueOf(v.Any()); v.Kind() == reflect.Pointer && v.IsNil() {
				s.appendString("<nil>")
				return
			}

			// Otherwise just print the original panic message.
			s.appendString(fmt.Sprintf("!PANIC: %v", r))
		}
	}()

	err := appendJSONValue(s, v)
	if err != nil {
		s.appendError(err)
	}
}

// Adapted from time.Time.MarshalJSON to avoid allocation.
func appendJSONTime(s *handleState, t time.Time) {
	if y := t.Year(); y < 0 || y >= 10000 {
		// RFC 3339 is clear that years are 4 digits exactly.
		// See golang.org/issue/4556#c15 for more discussion.
		s.appendError(errors.New("time.Time year outside of range [0,9999]"))
	}
	_ = s.buf.WriteByte('"')
	*s.buf = t.AppendFormat(*s.buf, time.RFC3339Nano)
	_ = s.buf.WriteByte('"')
}

func appendJSONValue(s *handleState, v slog.Value) error {
	switch v.Kind() {
	case slog.KindString:
		s.appendString(v.String())
	case slog.KindInt64:
		if s.h.stringifyNumbers {
			s.appendString(strconv.FormatInt(v.Int64(), 10))
		} else {
			*s.buf = strconv.AppendInt(*s.buf, v.Int64(), 10)
		}
	case slog.KindUint64:
		if s.h.stringifyNumbers {
			s.appendString(strconv.FormatUint(v.Uint64(), 10))
		} else {
			*s.buf = strconv.AppendUint(*s.buf, v.Uint64(), 10)
		}
	case slog.KindFloat64:
		// json.Marshal is funny about floats; it doesn't
		// always match strconv.AppendFloat. So just call it.
		// That's expensive, but floats are rare.
		if err := appendJSONMarshal(s.buf, v.Float64(), s.h.opts.JSONOptions); err != nil {
			return err
		}
	case slog.KindBool:
		*s.buf = strconv.AppendBool(*s.buf, v.Bool())
	case slog.KindDuration:
		// json v2 will return a duration like 1m5s,
		// json v1 will return the number of nanoseconds
		s.appendString(v.Duration().String())
	case slog.KindTime:
		appendJSONTime(s, v.Time())
	case slog.KindAny:
		a := v.Any()
		_, jm := a.(json.Marshaler)
		_, jm2 := a.(json.MarshalerTo)
		if err, ok := a.(error); ok && !jm && !jm2 {
			s.appendString(err.Error())
		} else {
			return appendJSONMarshal(s.buf, a, s.h.opts.JSONOptions)
		}
	default:
		panic(fmt.Sprintf("bad kind: %s", v.Kind()))
	}
	return nil
}

func appendJSONMarshal(buf *buffer.Buffer, v any, opts jsontext.Options) error {
	// Do not stream write, so we can keep valid json in case of an error
	bs, err := json.Marshal(v, opts)
	if err != nil {
		return err
	}
	_, _ = buf.Write(bs)
	return nil
}

// appendEscapedJSONString escapes s for JSON and appends it to buf.
// It does not surround the string in quotation marks.
//
// Modified from encoding/json/encode.go:encodeState.string,
// with escapeHTML set to false.
// TODO: align with json v2
func appendEscapedJSONString(buf []byte, s string) []byte {
	char := func(b byte) { buf = append(buf, b) }
	str := func(s string) { buf = append(buf, s...) }

	start := 0
	for i := 0; i < len(s); {
		if b := s[i]; b < utf8.RuneSelf {
			if safeSet[b] {
				i++
				continue
			}
			if start < i {
				str(s[start:i])
			}
			char('\\')
			switch b {
			case '\\', '"':
				char(b)
			case '\n':
				char('n')
			case '\r':
				char('r')
			case '\t':
				char('t')
			default:
				// This encodes bytes < 0x20 except for \t, \n and \r.
				str(`u00`)
				char(hex[b>>4])
				char(hex[b&0xF])
			}
			i++
			start = i
			continue
		}
		c, size := utf8.DecodeRuneInString(s[i:])
		if c == utf8.RuneError && size == 1 {
			if start < i {
				str(s[start:i])
			}
			str(`\ufffd`)
			i += size
			start = i
			continue
		}
		// U+2028 is LINE SEPARATOR.
		// U+2029 is PARAGRAPH SEPARATOR.
		// They are both technically valid characters in JSON strings,
		// but don't work in JSONP, which has to be evaluated as JavaScript,
		// and can lead to security holes there. It is valid JSON to
		// escape them, so we do so unconditionally.
		// See http://timelessrepo.com/json-isnt-a-javascript-subset for discussion.
		if c == '\u2028' || c == '\u2029' {
			if start < i {
				str(s[start:i])
			}
			str(`\u202`)
			char(hex[c&0xF])
			i += size
			start = i
			continue
		}
		i += size
	}
	if start < len(s) {
		str(s[start:])
	}
	return buf
}

const hex = "0123456789abcdef"

// Copied from encoding/json/tables.go.
//
// safeSet holds the value true if the ASCII character with the given array
// position can be represented inside a JSON string without any further
// escaping.
//
// All values are true except for the ASCII control characters (0-31), the
// double quote ("), and the backslash character ("\").
// TODO: align with json v2
var safeSet = [utf8.RuneSelf]bool{
	' ':      true,
	'!':      true,
	'"':      false,
	'#':      true,
	'$':      true,
	'%':      true,
	'&':      true,
	'\'':     true,
	'(':      true,
	')':      true,
	'*':      true,
	'+':      true,
	',':      true,
	'-':      true,
	'.':      true,
	'/':      true,
	'0':      true,
	'1':      true,
	'2':      true,
	'3':      true,
	'4':      true,
	'5':      true,
	'6':      true,
	'7':      true,
	'8':      true,
	'9':      true,
	':':      true,
	';':      true,
	'<':      true,
	'=':      true,
	'>':      true,
	'?':      true,
	'@':      true,
	'A':      true,
	'B':      true,
	'C':      true,
	'D':      true,
	'E':      true,
	'F':      true,
	'G':      true,
	'H':      true,
	'I':      true,
	'J':      true,
	'K':      true,
	'L':      true,
	'M':      true,
	'N':      true,
	'O':      true,
	'P':      true,
	'Q':      true,
	'R':      true,
	'S':      true,
	'T':      true,
	'U':      true,
	'V':      true,
	'W':      true,
	'X':      true,
	'Y':      true,
	'Z':      true,
	'[':      true,
	'\\':     false,
	']':      true,
	'^':      true,
	'_':      true,
	'`':      true,
	'a':      true,
	'b':      true,
	'c':      true,
	'd':      true,
	'e':      true,
	'f':      true,
	'g':      true,
	'h':      true,
	'i':      true,
	'j':      true,
	'k':      true,
	'l':      true,
	'm':      true,
	'n':      true,
	'o':      true,
	'p':      true,
	'q':      true,
	'r':      true,
	's':      true,
	't':      true,
	'u':      true,
	'v':      true,
	'w':      true,
	'x':      true,
	'y':      true,
	'z':      true,
	'{':      true,
	'|':      true,
	'}':      true,
	'~':      true,
	'\u007f': true,
}

// countEmptyGroups returns the number of empty group values in its argument.
func countEmptyGroups(as []slog.Attr) int {
	n := 0
	for _, a := range as {
		if valueIsEmptyGroup(a.Value) {
			n++
		}
	}
	return n
}

// isEmptyGroup reports whether v is a group that has no attributes.
func valueIsEmptyGroup(v slog.Value) bool {
	if v.Kind() != slog.KindGroup {
		return false
	}
	// We do not need to recursively examine the group's Attrs for emptiness,
	// because GroupValue removed them when the group was constructed, and
	// groups are immutable.
	return len(v.Group()) == 0
}

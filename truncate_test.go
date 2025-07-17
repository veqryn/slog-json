package slogjson

import (
	"bytes"
	"context"
	jsonv1 "encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"testing"
	"time"

	jsonv2 "github.com/go-json-experiment/json"
	jsonv2text "github.com/go-json-experiment/json/jsontext"
)

func TestReplaceAttrTruncateLong(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	buf := &bytes.Buffer{}

	maxLogFieldLength := 999

	logger := slog.New(NewHandler(buf, &HandlerOptions{
		AddSource:   false,
		Level:       slog.LevelDebug,
		JSONOptions: jsonOpts,

		// ReplaceAttr intercepts some attributes before they are logged to change their format
		ReplaceAttr: ReplaceAttrTruncate(maxLogFieldLength, jsonOpts),
	}))

	longValue := "abcdefghijklmnopqrstuvwxyz \"こんにちは世界\\/\t0123456789\r\n"
	for i := 0; i < 10; i++ {
		longValue += longValue
	}

	longValueJSON, err := jsonv2.Marshal(longValue, jsonOpts)
	if err != nil {
		t.Fatal(err)
	}

	longStruct := ValueStruct{Value: longValue}
	longStructJSON, err := jsonv2.Marshal(longStruct, jsonOpts)
	if err != nil {
		t.Fatal(err)
	}

	customMarshalled := NewDescriptiveStruct("descriptiveStruct", longValue)
	customMarshalledJSON, err := jsonv2.Marshal(customMarshalled, jsonOpts)
	if err != nil {
		t.Fatal(err)
	}

	logger.WarnContext(ctx, "hello world",
		slog.Any("nil", nil),
		slog.String("string", longValue),
		slog.Any("string2", longValue),
		slog.Any("bytes", []byte(longValue)),
		slog.Any("struct", longStruct),
		slog.Any("slice", []any{"slice", longStruct}),
		slog.Any("json.RawMessage", jsonv1.RawMessage(longStructJSON)),
		slog.Any("jsontext.Value", jsonv2text.Value(longStructJSON)),
		slog.Any("descriptiveStruct", customMarshalled),
		slog.Any("descriptiveStructs", []any{"de", customMarshalled}),
		slog.Any("error", errors.New(longValue)),
		slog.Any("descriptiveError", NewDescriptiveError("my error", longValue)),
	)

	// t.Log(len(longValue))
	// t.Log(string(longStructJSON))
	// t.Log(string(customMarshalledJSON))
	// t.Log(buf.String())

	var output simpleOutputTruncated
	err = jsonv2.Unmarshal(buf.Bytes(), &output)
	if err != nil {
		t.Fatal(err)
	}

	expected := simpleOutputTruncated{
		Time:    output.Time,
		Level:   "WARN",
		Msg:     "hello world",
		Any:     nil,
		String:  fmt.Sprintf(`replaced: true; original_length: %d; truncated: %s`, len(longValue), truncateByBytes(longValue, maxLogFieldLength)),
		String2: fmt.Sprintf(`replaced: true; original_length: %d; truncated: %s`, len(longValue), truncateByBytes(longValue, maxLogFieldLength)),
		Bytes:   fmt.Sprintf(`replaced: true; original_length: %d; truncated: %s`, len(longValue), truncateByBytes(longValue, maxLogFieldLength)),
		Struct: replaced{
			Replaced:  true,
			Length:    len(string(longStructJSON)),
			Truncated: truncateByBytes(string(longStructJSON), maxLogFieldLength),
		},
		Slice: replaced{
			Replaced:  true,
			Length:    len(string(longStructJSON)) + 11,
			Truncated: `["slice", ` + truncateByBytes(string(longStructJSON), maxLogFieldLength-10),
		},
		JsonRawMessage: replaced{
			Replaced:  true,
			Length:    len(string(longStructJSON)),
			Truncated: truncateByBytes(string(longStructJSON), maxLogFieldLength),
		},
		JsontextValue: replaced{
			Replaced:  true,
			Length:    len(string(longStructJSON)),
			Truncated: truncateByBytes(string(longStructJSON), maxLogFieldLength),
		},
		DescriptiveStruct: replaced{
			Replaced:  true,
			Length:    len(string(customMarshalledJSON)),
			Truncated: truncateByBytes(string(customMarshalledJSON), maxLogFieldLength),
		},
		DescriptiveStructs: replaced{
			Replaced:  true,
			Length:    len(string(customMarshalledJSON)) + 8,
			Truncated: `["de", ` + truncateByBytes(string(customMarshalledJSON), maxLogFieldLength-7),
		},
		Err: replaced{
			Replaced:  true,
			Length:    len(longValueJSON),
			Truncated: truncateByBytes(string(longValueJSON), maxLogFieldLength),
		},
		DescriptiveError: replaced{
			Replaced:  true,
			Length:    len(longValueJSON),
			Truncated: truncateByBytes(string(longValueJSON), maxLogFieldLength),
		},
	}

	if !reflect.DeepEqual(output, expected) {
		t.Fatalf("Expected:\n%#v\n\nGot:\n%#v\n\nRaw:%s", expected, output, buf.String())
	}
}

type simpleOutputTruncated struct {
	Time  time.Time `json:"time"`
	Level string    `json:"level"`
	Msg   string    `json:"msg"`

	Any                any      `json:"any"`
	String             string   `json:"string"`
	String2            string   `json:"string2"`
	Bytes              string   `json:"bytes"`
	Struct             replaced `json:"struct"`
	Slice              replaced `json:"slice"`
	JsonRawMessage     replaced `json:"json.RawMessage"`
	JsontextValue      replaced `json:"jsontext.Value"`
	DescriptiveStruct  replaced `json:"descriptiveStruct"`
	DescriptiveStructs replaced `json:"descriptiveStructs"`
	Err                replaced `json:"error"`
	DescriptiveError   replaced `json:"descriptiveError"`
}

func TestReplaceAttrTruncateShort(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	buf := &bytes.Buffer{}

	logger := slog.New(NewHandler(buf, &HandlerOptions{
		AddSource:   false,
		Level:       slog.LevelDebug,
		JSONOptions: jsonOpts,

		// ReplaceAttr intercepts some attributes before they are logged to change their format
		ReplaceAttr: ReplaceAttrTruncate(999, jsonOpts),
	}))

	longValue := "abcdefghijklmnopqrstuvwxyz \"こんにちは世界\\/\t0123456789\r\n"
	for i := 0; i < 2; i++ {
		longValue += longValue
	}

	longStruct := ValueStruct{Value: longValue}
	longStructJSON, err := jsonv2.Marshal(longStruct, jsonOpts)
	if err != nil {
		t.Fatal(err)
	}

	customMarshalled := NewDescriptiveStruct("descriptiveStruct", longValue)

	logger.WarnContext(ctx, "hello world",
		slog.Any("nil", nil),
		slog.String("string", longValue),
		slog.Any("string2", longValue),
		slog.Any("bytes", []byte(longValue)),
		slog.Any("struct", longStruct),
		slog.Any("slice", []any{"slice", longStruct}),
		slog.Any("json.RawMessage", jsonv1.RawMessage(longStructJSON)),
		slog.Any("jsontext.Value", jsonv2text.Value(longStructJSON)),
		slog.Any("descriptiveStruct", customMarshalled),
		slog.Any("descriptiveStructs", []any{"de", customMarshalled}),
		slog.Any("error", errors.New(longValue)),
		slog.Any("descriptiveError", NewDescriptiveError("my error", longValue)),
	)

	// t.Log(len(longValue))
	// t.Log(string(longStructJSON))
	// t.Log(string(customMarshalledJSON))
	// t.Log(buf.String())

	var output simpleOutputNonTruncated
	err = jsonv2.Unmarshal(buf.Bytes(), &output)
	if err != nil {
		t.Fatal(err)
	}

	// Confirm nothing was truncated because it was short enough
	expected := simpleOutputNonTruncated{
		Time:               output.Time,
		Level:              "WARN",
		Msg:                "hello world",
		Any:                nil,
		String:             longValue,
		String2:            longValue,
		Bytes:              longValue,
		Struct:             ValueStruct{Value: longValue},
		Slice:              []any{"slice", map[string]any{"value": longValue}},
		JsonRawMessage:     ValueStruct{Value: longValue},
		JsontextValue:      ValueStruct{Value: longValue},
		DescriptiveStruct:  longValue,
		DescriptiveStructs: []any{"de", longValue},
		Err:                longValue,
		DescriptiveError:   longValue,
	}

	if !reflect.DeepEqual(output, expected) {
		t.Fatalf("Expected:\n%#v\n\nGot:\n%#v\n\nRaw:%s", expected, output, buf.String())
	}
}

type simpleOutputNonTruncated struct {
	Time  time.Time `json:"time"`
	Level string    `json:"level"`
	Msg   string    `json:"msg"`

	Any                any         `json:"any"`
	String             string      `json:"string"`
	String2            string      `json:"string2"`
	Bytes              string      `json:"bytes"`
	Struct             ValueStruct `json:"struct"`
	Slice              []any       `json:"slice"`
	JsonRawMessage     ValueStruct `json:"json.RawMessage"`
	JsontextValue      ValueStruct `json:"jsontext.Value"`
	DescriptiveStruct  string      `json:"descriptiveStruct"`
	DescriptiveStructs []any       `json:"descriptiveStructs"`
	Err                string      `json:"error"`
	DescriptiveError   string      `json:"descriptiveError"`
}

type DescriptiveStruct struct {
	description string
	name        string
}

func NewDescriptiveStruct(name string, description string) *DescriptiveStruct {
	return &DescriptiveStruct{description: description, name: name}
}

func (de *DescriptiveStruct) Description() string {
	return de.description
}

type ValueStruct struct {
	Value string `json:"value"`
}

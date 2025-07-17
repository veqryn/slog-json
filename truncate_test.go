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

func TestReplaceAttrTruncate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	buf := &bytes.Buffer{}

	maxLogFieldLength := 99

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

	type structJSON struct {
		Value string `json:"value"`
	}

	longStruct := structJSON{Value: longValue}
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

	var output simpleOutput
	err = jsonv2.Unmarshal(buf.Bytes(), &output)
	if err != nil {
		t.Fatal(err)
	}

	expected := simpleOutput{
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

type simpleOutput struct {
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

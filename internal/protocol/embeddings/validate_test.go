package embeddings

import (
	"encoding/json"
	"testing"

	"nvidia-router/internal/modelcatalog"
)

func TestParseRejectsOversizedBody(t *testing.T) {
	payload := `{"model":"m","input":"x"}`
	oversized := "[" + payload + "," + `"` + string(make([]byte, MaxRequestBytes)) + `"` + "]"
	if _, err := Parse([]byte(oversized)); err == nil {
		t.Fatal("expected error for oversized body")
	}
}

func TestParseAcceptsStringInput(t *testing.T) {
	request, err := Parse([]byte(`{"model":"public-embed","input":"hello"}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if request.PublicModelID() != "public-embed" {
		t.Fatalf("model = %q", request.PublicModelID())
	}
	if len(request.inputs) != 1 || request.inputs[0] != "hello" {
		t.Fatalf("inputs = %#v", request.inputs)
	}
	if request.Requirements().Kind != modelcatalog.KindEmbedding {
		t.Fatalf("kind = %q", request.Requirements().Kind)
	}
}

func TestParseAcceptsStringArrayInput(t *testing.T) {
	request, err := Parse([]byte(`{"model":"public-embed","input":["a","b","c"]}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(request.inputs) != 3 {
		t.Fatalf("inputs = %#v", request.inputs)
	}
}

func TestParseRejectsEmptyAndWhitespaceInput(t *testing.T) {
	cases := []string{
		`{"model":"m","input":""}`,
		`{"model":"m","input":"   "}`,
		`{"model":"m","input":[]}`,
		`{"model":"m","input":["ok",""]}`,
	}
	for _, payload := range cases {
		if _, err := Parse([]byte(payload)); err == nil {
			t.Fatalf("expected rejection for %s", payload)
		}
	}
}

func TestParseRejectsInvalidInputShape(t *testing.T) {
	cases := []string{
		`{"model":"m","input":123}`,
		`{"model":"m","input":true}`,
		`{"model":"m","input":{"obj":true}}`,
		`{"model":"m","input":[1,2]}`,
	}
	for _, payload := range cases {
		if _, err := Parse([]byte(payload)); err == nil {
			t.Fatalf("expected rejection for %s", payload)
		}
	}
}

func TestParseRejectsMissingFields(t *testing.T) {
	if _, err := Parse([]byte(`{"input":"hi"}`)); err == nil {
		t.Fatal("expected missing model rejection")
	}
	if _, err := Parse([]byte(`{"model":"m"}`)); err == nil {
		t.Fatal("expected missing input rejection")
	}
}

func TestMarshalForMapsModelAndPreservesUnknown(t *testing.T) {
	request, err := Parse([]byte(`{"model":"public-embed","input":"hi","encoding_format":"float","dimensions":256,"vendor":{"kept":true}}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	body, err := request.MarshalFor(modelcatalog.Model{UpstreamID: "vendor/embed"})
	if err != nil {
		t.Fatalf("MarshalFor: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if fields["model"] != "vendor/embed" {
		t.Fatalf("model = %v", fields["model"])
	}
	if fields["encoding_format"] != "float" {
		t.Fatalf("encoding_format = %v", fields["encoding_format"])
	}
	if fields["dimensions"] != float64(256) {
		t.Fatalf("dimensions = %v", fields["dimensions"])
	}
	if _, ok := fields["vendor"]; !ok {
		t.Fatal("unknown field vendor was dropped")
	}
	if fields["input"] != "hi" {
		t.Fatalf("input = %v", fields["input"])
	}
}

func TestMarshalForIsDeterministic(t *testing.T) {
	request, err := Parse([]byte(`{"model":"public-embed","input":"hi","z":1,"a":2}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	first, err := request.MarshalFor(modelcatalog.Model{UpstreamID: "vendor/embed"})
	if err != nil {
		t.Fatalf("first marshal: %v", err)
	}
	second, err := request.MarshalFor(modelcatalog.Model{UpstreamID: "vendor/embed"})
	if err != nil {
		t.Fatalf("second marshal: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("non-deterministic marshal:\n%s\n%s", first, second)
	}
}

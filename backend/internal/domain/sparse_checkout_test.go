package domain

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestSparseCheckoutConfigValidate(t *testing.T) {
	many := make([]string, MaxSparsePaths+1)
	for i := range many {
		many[i] = fmt.Sprintf("dir%d", i)
	}
	cases := []struct {
		name    string
		cfg     SparseCheckoutConfig
		wantErr bool
	}{
		{"zero", SparseCheckoutConfig{}, false},
		{"enabled with includes", SparseCheckoutConfig{Enabled: true, AlwaysInclude: []string{"scripts", "pkg/shared"}}, false},
		{"absolute", SparseCheckoutConfig{Enabled: true, AlwaysInclude: []string{"/etc"}}, true},
		{"escaping", SparseCheckoutConfig{AlwaysInclude: []string{"../other"}}, true},
		{"empty entry", SparseCheckoutConfig{AlwaysInclude: []string{" "}}, true},
		{"repo root", SparseCheckoutConfig{AlwaysInclude: []string{"."}}, true},
		{"too many", SparseCheckoutConfig{AlwaysInclude: many}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ProjectConfig{SparseCheckout: tc.cfg}.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestSparseCheckoutConfigJSONRoundTrip(t *testing.T) {
	in := ProjectConfig{SparseCheckout: SparseCheckoutConfig{Enabled: true, AlwaysInclude: []string{"scripts"}}}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"sparseCheckout":{"enabled":true,"alwaysInclude":["scripts"]}`) {
		t.Fatalf("json = %s", raw)
	}
	var out ProjectConfig
	if err := json.Unmarshal(raw, &out); err != nil || !reflect.DeepEqual(out, in) {
		t.Fatalf("round trip = %+v err=%v", out, err)
	}
	empty, _ := json.Marshal(ProjectConfig{})
	if !strings.Contains(string(empty), `"sparseCheckout":{}`) {
		t.Fatalf("zero config json = %s", empty)
	}
}

func TestValidateSparsePaths(t *testing.T) {
	if err := ValidateSparsePaths([]string{"a", "b/c"}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]string{{""}, {"/abs"}, {"a/../.."}, {"."}} {
		if err := ValidateSparsePaths(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}

func TestEffectiveSparsePaths(t *testing.T) {
	got := EffectiveSparsePaths([]string{"src/api/", "b", "a"}, []string{"scripts", "a", " "})
	if !reflect.DeepEqual(got, []string{"a", "b", "scripts", "src/api"}) {
		t.Fatalf("got %#v", got)
	}
	if got := EffectiveSparsePaths(nil, []string{"scripts"}); got != nil {
		t.Fatalf("no requested paths must stay a full checkout, got %#v", got)
	}
}

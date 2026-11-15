package jsonc

import (
	"encoding/json"
	"testing"
)

func TestStripKeepsStringsAndDropsCommentsAndTrailingCommas(t *testing.T) {
	raw := []byte(`{
  // a comment
  "url": "https://gw.example.com/v1", /* block */
  "list": [1, 2,],
}`)
	var got struct {
		URL  string `json:"url"`
		List []int  `json:"list"`
	}
	if err := json.Unmarshal(Strip(raw), &got); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, Strip(raw))
	}
	if got.URL != "https://gw.example.com/v1" || len(got.List) != 2 {
		t.Fatalf("got %+v", got)
	}
}

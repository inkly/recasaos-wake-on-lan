package route

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/inkly/recasaos-wake-on-lan/service"
)

// The bounds of the card format v1, as the dashboard reads them.
const (
	fmtTitleMax    = 40
	fmtStatusMax   = 40
	fmtRowsMax     = 8
	fmtLabelMax    = 40
	fmtValueMax    = 40
	fmtActionsMax  = 2
	fmtActLabelMax = 24
	fmtBodyMax     = 1024
	fmtPathMax     = 200
	fmtRefreshMin  = 30
	fmtRefreshMax  = 3600
)

func okPath(p string) bool {
	if !strings.HasPrefix(p, "/") || len(p) > fmtPathMax || strings.Contains(p, "..") || strings.Contains(p, "//") ||
		strings.ContainsAny(p, "?#\\") {
		return false
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func n(s string) int { return utf8.RuneCountInString(s) }

// strictCard reads the JSON the way the dashboard does: no field it does not know.
type strictCard struct {
	Schema int    `json:"schema"`
	Title  string `json:"title"`
	Icon   string `json:"icon"`
	Status *struct {
		Level string `json:"level"`
		Text  string `json:"text"`
	} `json:"status"`
	Rows []struct {
		Label  string `json:"label"`
		Value  string `json:"value"`
		Action *struct {
			Label  string          `json:"label"`
			Method string          `json:"method"`
			Path   string          `json:"path"`
			Body   json.RawMessage `json:"body"`
		} `json:"action"`
	} `json:"rows"`
	Link *struct {
		Label string `json:"label"`
		Path  string `json:"path"`
	} `json:"link"`
	Refresh int `json:"refresh"`
}

func checkFormat(t *testing.T, raw []byte) strictCard {
	t.Helper()
	var c strictCard
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&c); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	if c.Schema != 1 || n(c.Title) < 1 || n(c.Title) > fmtTitleMax || c.Icon != "network-outline" {
		t.Errorf("head: %+v", c)
	}
	if c.Status == nil || c.Status.Level != "info" || n(c.Status.Text) > fmtStatusMax {
		t.Errorf("status: %+v", c.Status)
	}
	if len(c.Rows) > fmtRowsMax {
		t.Errorf("%d rows", len(c.Rows))
	}
	for i, r := range c.Rows {
		if n(r.Label) < 1 || n(r.Label) > fmtLabelMax || n(r.Value) > fmtValueMax {
			t.Errorf("row %d: label %q value %q", i, r.Label, r.Value)
		}
		if a := r.Action; a != nil {
			if n(a.Label) < 1 || n(a.Label) > fmtActLabelMax || a.Method != "POST" || !okPath(a.Path) || len(a.Body) > fmtBodyMax {
				t.Errorf("row %d: action %+v", i, a)
			}
		}
	}
	if c.Link == nil || n(c.Link.Label) < 1 || n(c.Link.Label) > fmtActLabelMax || !okPath(c.Link.Path) {
		t.Errorf("link: %+v", c.Link)
	}
	if c.Refresh < fmtRefreshMin || c.Refresh > fmtRefreshMax {
		t.Errorf("refresh %d", c.Refresh)
	}
	_ = fmtActionsMax // the card has no card-level action
	return c
}

func devicesOf(count, nameLen int) []service.Device {
	out := make([]service.Device, count)
	for i := range out {
		name := fmt.Sprintf("%02d", 99-i) + strings.Repeat("x", nameLen)
		out[i] = service.Device{Name: string([]rune(name)[:nameLen]), MAC: fmt.Sprintf("aa:bb:cc:dd:ee:%02x", i)}
	}
	return out
}

func TestCardKeepsTheFormat(t *testing.T) {
	for _, tc := range []struct{ count, nameLen, rows int }{{0, 5, 1}, {1, 5, 1}, {2, 64, 2}, {8, 64, 8}, {9, 64, 8}, {20, 64, 8}, {20, 5, 8}} {
		raw, _ := json.Marshal(buildCard(devicesOf(tc.count, tc.nameLen)))
		c := checkFormat(t, raw)
		if len(c.Rows) != tc.rows {
			t.Errorf("%d devices: %d rows, want %d", tc.count, len(c.Rows), tc.rows)
		}
	}
}

func TestCardContent(t *testing.T) {
	c := buildCard([]service.Device{{Name: "nas", MAC: "aa:bb:cc:dd:ee:02"}, {Name: "Desk PC", MAC: "aa:bb:cc:dd:ee:01"}})
	if c.Status.Text != "2 devices" || c.Rows[0].Label != "Desk PC" || c.Rows[1].Label != "nas" {
		t.Errorf("%+v", c)
	}
	if a := c.Rows[0].Action; a.Label != "Wake" || a.Path != "/wake" || a.Body["mac"] != "aa:bb:cc:dd:ee:01" {
		t.Errorf("%+v", a)
	}
	if c := buildCard(devicesOf(1, 5)); c.Status.Text != "1 device" {
		t.Errorf("%q", c.Status.Text)
	}
	c = buildCard(devicesOf(20, 5))
	if last := c.Rows[7]; last.Label != "…and 13 more" || last.Action != nil || c.Rows[6].Action == nil {
		t.Errorf("%+v", c.Rows)
	}
	if c.Rows[0].Label >= c.Rows[1].Label { // by name
		t.Errorf("not sorted: %v", c.Rows[:2])
	}
	c = buildCard(devicesOf(1, 64))
	if got := c.Rows[0].Label; n(got) != 40 || !strings.HasSuffix(got, "…") {
		t.Errorf("%q", got)
	}
	if c := buildCard(nil); c.Rows[0].Label != "No device yet" || c.Rows[0].Action != nil || c.Status.Text != "0 devices" {
		t.Errorf("%+v", c)
	}
}

func TestCardRoute(t *testing.T) {
	for role := range map[string]bool{"admin": true, "user": false} {
		token := box(t, role, true)
		r := newFixture(t).send(http.MethodGet, "/v1/wake_on_lan/card", token, "", lan)
		if r.Code != http.StatusOK || !strings.HasPrefix(r.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("%s: %d %s", role, r.Code, r.Header())
		}
		checkFormat(t, r.Body.Bytes())
	}
}
